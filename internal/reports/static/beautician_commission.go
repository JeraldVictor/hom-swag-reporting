package static

import (
	"context"
	"fmt"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type BeauticianCommissionExecutor struct {
	db *mongo.Database
}

func NewBeauticianCommissionExecutor(db *mongo.Database) *BeauticianCommissionExecutor {
	return &BeauticianCommissionExecutor{db: db}
}

func (e *BeauticianCommissionExecutor) Key() string {
	return "beautician_commission"
}

func (e *BeauticianCommissionExecutor) Version() int {
	return 1
}

func (e *BeauticianCommissionExecutor) Validate(ctx context.Context, req reports.Request) error {
	if _, ok := req.Parameters["start_date"]; !ok {
		return fmt.Errorf("start_date is required")
	}
	if _, ok := req.Parameters["end_date"]; !ok {
		return fmt.Errorf("end_date is required")
	}
	return nil
}

func (e *BeauticianCommissionExecutor) Run(ctx context.Context, req reports.Request, sink reports.RowSink) error {
	startDateStr := req.Parameters["start_date"].(string)
	endDateStr := req.Parameters["end_date"].(string)

	startDate, err := parseReportDate(startDateStr, false)
	if err != nil {
		return fmt.Errorf("invalid start_date: %w", err)
	}
	endDate, err := parseReportDate(endDateStr, true)
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
			"_id": "$beautician_id",
			"total_commission": bson.M{
				"$sum": bson.M{
					"$ifNull": bson.A{
						"$commission_details.total_commission",
						"$beautician_commission",
					},
				},
			},
			"total_revenue": bson.M{
				"$sum": bson.M{
					"$ifNull": bson.A{
						"$order_cost",
						"$revenue",
					},
				},
			},
			"order_count": bson.M{"$sum": 1},
		}}},
		{{Key: "$lookup", Value: bson.M{
			"from":         "beauticians",
			"localField":   "_id",
			"foreignField": "_id",
			"as":           "beautician",
		}}},
		{{Key: "$unwind", Value: "$beautician"}},
	}

	if officeIDStr, ok := req.Parameters["office_id"].(string); ok && officeIDStr != "" {
		if officeID, err := primitive.ObjectIDFromHex(officeIDStr); err == nil {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{"beautician.office_id": officeID}}})
		}
	}

	pipeline = append(pipeline, bson.D{{Key: "$project", Value: bson.M{
		"name":             "$beautician.name",
		"emp_code":         "$beautician.emp_code",
		"total_commission": 1,
		"total_revenue":    1,
		"order_count":      1,
	}}})

	if req.Limit > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: req.Limit}})
	}

	cursor, err := e.db.Collection("orders").Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	// Header
	sink.WriteRow([]interface{}{"Employee Code", "Beautician Name", "Order Count", "Total Revenue", "Total Commission"})

	for cursor.Next(ctx) {
		var result struct {
			Name            string  `bson:"name"`
			EmpCode         string  `bson:"emp_code"`
			TotalCommission float64 `bson:"total_commission"`
			TotalRevenue    float64 `bson:"total_revenue"`
			OrderCount      int     `bson:"order_count"`
		}
		if err := cursor.Decode(&result); err != nil {
			return err
		}
		sink.WriteRow([]interface{}{
			result.EmpCode,
			result.Name,
			result.OrderCount,
			fmt.Sprintf("%.2f", result.TotalRevenue),
			fmt.Sprintf("%.2f", result.TotalCommission),
		})
	}

	return nil
}
