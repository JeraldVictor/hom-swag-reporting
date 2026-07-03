package static

import (
	"context"
	"fmt"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type CODPendingExecutor struct {
	db *mongo.Database
}

func NewCODPendingExecutor(db *mongo.Database) *CODPendingExecutor {
	return &CODPendingExecutor{db: db}
}

func (e *CODPendingExecutor) Key() string {
	return "cod_pending"
}

func (e *CODPendingExecutor) Version() int {
	return 1
}

func (e *CODPendingExecutor) Columns() []reports.Column {
	return withColumnDescriptions(codPendingColumns)
}

func (e *CODPendingExecutor) Validate(ctx context.Context, req reports.Request) error {
	if _, ok := req.Parameters["start_date"]; !ok {
		return fmt.Errorf("start_date is required")
	}
	if _, ok := req.Parameters["end_date"]; !ok {
		return fmt.Errorf("end_date is required")
	}
	return nil
}

func (e *CODPendingExecutor) Run(ctx context.Context, req reports.Request, sink reports.RowSink) error {
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

	match := bson.M{
		"is_deleted": false,
		"booking_info.date": bson.M{
			"$gte": startDate.Format("2006-01-02"),
			"$lte": endDate.Format("2006-01-02"),
		},
		"cod_status": bson.M{"$in": bson.A{"collected", "pending_return"}},
	}
	if officeID, ok := dailySalesOfficeID(req.Parameters); ok {
		match["office_id"] = officeID
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$project", Value: bson.M{
			"order_number":         "$order_number",
			"customer_name":        "$customer.full_name",
			"order_date":           "$booking_info.date",
			"cod_status":           "$cod_status",
			"cod_collected_amount": bson.M{"$ifNull": bson.A{"$cod_collected_amount", 0}},
		}}},
		{{Key: "$sort", Value: bson.M{"order_date": -1, "order_number": -1}}},
	}

	if req.Limit > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: req.Limit}})
	}

	cursor, err := e.db.Collection("orders").Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	if err := sink.WriteRow([]interface{}{
		"Order Number",
		"Customer Name",
		"Order Date",
		"COD Status",
		"COD Collected Amount",
	}); err != nil {
		return err
	}

	for cursor.Next(ctx) {
		var row struct {
			OrderNumber        string  `bson:"order_number"`
			CustomerName       string  `bson:"customer_name"`
			OrderDate          string  `bson:"order_date"`
			CODStatus          string  `bson:"cod_status"`
			CODCollectedAmount float64 `bson:"cod_collected_amount"`
		}
		if err := cursor.Decode(&row); err != nil {
			return err
		}
		if err := sink.WriteRow([]interface{}{
			row.OrderNumber,
			row.CustomerName,
			row.OrderDate,
			row.CODStatus,
			money2(row.CODCollectedAmount),
		}); err != nil {
			return err
		}
	}

	return cursor.Err()
}
