package static

import (
	"context"
	"fmt"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type DailySalesExecutor struct {
	db *mongo.Database
}

func NewDailySalesExecutor(db *mongo.Database) *DailySalesExecutor {
	return &DailySalesExecutor{db: db}
}

func (e *DailySalesExecutor) Key() string {
	return "daily_sales"
}

func (e *DailySalesExecutor) Version() int {
	return 2
}

func (e *DailySalesExecutor) Validate(ctx context.Context, req reports.Request) error {
	if _, ok := req.Parameters["start_date"]; !ok {
		return fmt.Errorf("start_date is required")
	}
	if _, ok := req.Parameters["end_date"]; !ok {
		return fmt.Errorf("end_date is required")
	}
	return nil
}

func (e *DailySalesExecutor) Run(ctx context.Context, req reports.Request, sink reports.RowSink) error {
	startDateStr := req.Parameters["start_date"].(string)
	endDateStr := req.Parameters["end_date"].(string)

	startDate, err := parseDailySalesDate(startDateStr, false)
	if err != nil {
		return fmt.Errorf("invalid start_date: %w", err)
	}
	endDate, err := parseDailySalesDate(endDateStr, true)
	if err != nil {
		return fmt.Errorf("invalid end_date: %w", err)
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"service_date": bson.M{
				"$gte": startDate,
				"$lte": endDate,
			},
			"is_deleted": false,
			"status":     "completed",
		}}},
		{{Key: "$group", Value: bson.M{
			"_id":         "$booking_info.date",
			"total_sales": bson.M{"$sum": "$total"},
			"order_count": bson.M{"$sum": 1},
			"total_tax":   bson.M{"$sum": "$tax_total"},
			"total_tip":   bson.M{"$sum": "$tip"},
		}}},
		{{Key: "$sort", Value: bson.M{"_id": 1}}},
	}

	if req.Limit > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: req.Limit}})
	}

	cursor, err := e.db.Collection("orders").Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	// Header
	sink.WriteRow([]interface{}{"Date", "Order Count", "Total Tax", "Total Tip", "Total Sales"})

	var totalOrderCount int
	var totalTax float64
	var totalTip float64
	var totalSales float64

	for cursor.Next(ctx) {
		var result struct {
			Date       string  `bson:"_id"`
			TotalSales float64 `bson:"total_sales"`
			OrderCount int     `bson:"order_count"`
			TotalTax   float64 `bson:"total_tax"`
			TotalTip   float64 `bson:"total_tip"`
		}
		if err := cursor.Decode(&result); err != nil {
			return err
		}
		totalOrderCount += result.OrderCount
		totalTax += result.TotalTax
		totalTip += result.TotalTip
		totalSales += result.TotalSales
		sink.WriteRow([]interface{}{
			result.Date,
			result.OrderCount,
			fmt.Sprintf("%.2f", result.TotalTax),
			fmt.Sprintf("%.2f", result.TotalTip),
			fmt.Sprintf("%.2f", result.TotalSales),
		})
	}

	if err := cursor.Err(); err != nil {
		return err
	}

	sink.WriteRow([]interface{}{
		"Total",
		totalOrderCount,
		fmt.Sprintf("%.2f", totalTax),
		fmt.Sprintf("%.2f", totalTip),
		fmt.Sprintf("%.2f", totalSales),
	})

	return nil
}

func parseDailySalesDate(value string, endOfDay bool) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}

	parsed, err := time.ParseInLocation("2006-01-02", value, time.FixedZone("IST", 5*60*60+30*60))
	if err != nil {
		return time.Time{}, err
	}
	if endOfDay {
		return parsed.Add(24*time.Hour - time.Nanosecond), nil
	}
	return parsed, nil
}
