package static

import (
	"context"
	"fmt"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type RiderCommissionExecutor struct {
	db *mongo.Database
}

func NewRiderCommissionExecutor(db *mongo.Database) *RiderCommissionExecutor {
	return &RiderCommissionExecutor{db: db}
}

func (e *RiderCommissionExecutor) Key() string {
	return "rider_commission"
}

func (e *RiderCommissionExecutor) Version() int {
	return 1
}

func (e *RiderCommissionExecutor) Validate(ctx context.Context, req reports.Request) error {
	if _, ok := req.Parameters["start_date"]; !ok {
		return fmt.Errorf("start_date is required")
	}
	if _, ok := req.Parameters["end_date"]; !ok {
		return fmt.Errorf("end_date is required")
	}
	return nil
}

func (e *RiderCommissionExecutor) Run(ctx context.Context, req reports.Request, sink reports.RowSink) error {
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
	startDateKey := startDate.Format("2006-01-02")
	endDateKey := endDate.Format("2006-01-02")

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"$or": bson.A{
				bson.M{"date": bson.M{"$gte": startDateKey, "$lte": endDateKey}},
				bson.M{"end_time": bson.M{"$gte": startDate, "$lte": endDate}},
				bson.M{
					"end_time": bson.M{"$exists": false},
					"created_at": bson.M{
						"$gte": startDate,
						"$lte": endDate,
					},
				},
			},
			"is_deleted":   false,
			"kanban_state": "trip_completed",
		}}},
		{{Key: "$addFields", Value: bson.M{
			"payable_distance_km": bson.M{
				"$cond": bson.A{
					bson.M{"$gt": bson.A{"$fare_calculation.trip_distance_km", 0}},
					"$fare_calculation.trip_distance_km",
					bson.M{
						"$cond": bson.A{
							"$is_two_way",
							bson.M{"$multiply": bson.A{bson.M{"$ifNull": bson.A{"$auto_distance_km", 0}}, 2}},
							bson.M{"$ifNull": bson.A{"$auto_distance_km", 0}},
						},
					},
				},
			},
			"petrol_payable": bson.M{"$ifNull": bson.A{"$fare_calculation.calculated_fare", 0}},
		}}},
		{{Key: "$addFields", Value: bson.M{
			"commission_payable": bson.M{
				"$cond": bson.A{
					"$is_commission_applicable",
					bson.M{
						"$ifNull": bson.A{
							"$commission_amount",
							"$payable_distance_km",
						},
					},
					0,
				},
			},
		}}},
		{{Key: "$group", Value: bson.M{
			"_id":                "$rider_id",
			"total_distance_km":  bson.M{"$sum": "$payable_distance_km"},
			"petrol_payable":     bson.M{"$sum": "$petrol_payable"},
			"commission_payable": bson.M{"$sum": "$commission_payable"},
			"trip_count":         bson.M{"$sum": 1},
		}}},
		{{Key: "$match", Value: bson.M{
			"_id": bson.M{"$ne": nil},
		}}},
		{{Key: "$lookup", Value: bson.M{
			"from":         "riders",
			"localField":   "_id",
			"foreignField": "_id",
			"as":           "rider",
		}}},
		{{Key: "$unwind", Value: "$rider"}},
	}

	if officeIDStr, ok := req.Parameters["office_id"].(string); ok && officeIDStr != "" {
		if officeID, err := primitive.ObjectIDFromHex(officeIDStr); err == nil {
			pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{"rider.office_id": officeID}}})
		}
	}

	pipeline = append(pipeline, bson.D{{Key: "$project", Value: bson.M{
		"name":               "$rider.name",
		"emp_code":           "$rider.emp_code",
		"total_distance_km":  1,
		"petrol_payable":     1,
		"commission_payable": 1,
		"trip_count":         1,
	}}})

	if req.Limit > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: req.Limit}})
	}

	cursor, err := e.db.Collection("trips").Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	// Header
	sink.WriteRow([]interface{}{"Employee Code", "Rider Name", "Trip Count", "Total Distance KM", "Petrol Payable", "Commission Payable"})

	for cursor.Next(ctx) {
		var result struct {
			Name              string  `bson:"name"`
			EmpCode           string  `bson:"emp_code"`
			TripCount         int     `bson:"trip_count"`
			TotalDistanceKM   float64 `bson:"total_distance_km"`
			PetrolPayable     float64 `bson:"petrol_payable"`
			CommissionPayable float64 `bson:"commission_payable"`
		}
		if err := cursor.Decode(&result); err != nil {
			return err
		}
		sink.WriteRow([]interface{}{
			result.EmpCode,
			result.Name,
			result.TripCount,
			fmt.Sprintf("%.2f", result.TotalDistanceKM),
			fmt.Sprintf("%.2f", result.PetrolPayable),
			fmt.Sprintf("%.2f", result.CommissionPayable),
		})
	}

	return nil
}

func parseReportDate(value string, endOfDay bool) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}

	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, err
	}
	if endOfDay {
		return parsed.Add(24*time.Hour - time.Nanosecond), nil
	}
	return parsed, nil
}
