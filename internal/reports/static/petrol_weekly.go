package static

import (
	"context"
	"fmt"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type PetrolWeeklyExecutor struct {
	db *mongo.Database
}

func NewPetrolWeeklyExecutor(db *mongo.Database) *PetrolWeeklyExecutor {
	return &PetrolWeeklyExecutor{db: db}
}

func (e *PetrolWeeklyExecutor) Key() string {
	return "petrol_weekly"
}

func (e *PetrolWeeklyExecutor) Version() int {
	return 1
}

func (e *PetrolWeeklyExecutor) Validate(ctx context.Context, req reports.Request) error {
	if _, ok := req.Parameters["start_date"]; !ok {
		return fmt.Errorf("start_date is required")
	}
	if _, ok := req.Parameters["end_date"]; !ok {
		return fmt.Errorf("end_date is required")
	}
	return nil
}

func (e *PetrolWeeklyExecutor) Run(ctx context.Context, req reports.Request, sink reports.RowSink) error {
	startDateStr := req.Parameters["start_date"].(string)
	endDateStr := req.Parameters["end_date"].(string)

	startDate, _ := time.Parse(time.RFC3339, startDateStr)
	endDate, _ := time.Parse(time.RFC3339, endDateStr)

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"created_at": bson.M{
				"$gte": startDate,
				"$lte": endDate,
			},
			"is_deleted": false,
			"status":     "completed", // Assuming only completed trips count
		}}},
		{{Key: "$group", Value: bson.M{
			"_id": "$rider_id",
			"total_distance": bson.M{"$sum": "$fare_calculation.trip_distance_km"},
			"total_amount":   bson.M{"$sum": "$fare_calculation.calculated_fare"},
		}}},
		{{Key: "$lookup", Value: bson.M{
			"from":         "riders",
			"localField":   "_id",
			"foreignField": "_id",
			"as":           "rider",
		}}},
		{{Key: "$unwind", Value: "$rider"}},
		{{Key: "$project", Value: bson.M{
			"rider_name":     "$rider.name",
			"emp_code":       "$rider.emp_code",
			"total_distance": 1,
			"total_amount":   1,
		}}},
	}

	cursor, err := e.db.Collection("trips").Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	// Header
	sink.WriteRow([]interface{}{"Employee Code", "Rider Name", "Total Distance (KM)", "Payable Amount"})

	for cursor.Next(ctx) {
		var result struct {
			RiderName     string  `bson:"rider_name"`
			EmpCode       string  `bson:"emp_code"`
			TotalDistance float64 `bson:"total_distance"`
			TotalAmount   float64 `bson:"total_amount"`
		}
		if err := cursor.Decode(&result); err != nil {
			return err
		}
		sink.WriteRow([]interface{}{
			result.EmpCode,
			result.RiderName,
			fmt.Sprintf("%.2f", result.TotalDistance),
			fmt.Sprintf("%.2f", result.TotalAmount),
		})
	}

	return nil
}
