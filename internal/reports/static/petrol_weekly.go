package static

import (
	"context"
	"fmt"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
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

func (e *PetrolWeeklyExecutor) Columns() []reports.Column {
	return withColumnDescriptions(petrolWeeklyColumns)
}

func (e *PetrolWeeklyExecutor) Validate(ctx context.Context, req reports.Request) error {
	return validateReportDateRange(req.Parameters, parseReportDate)
}

func (e *PetrolWeeklyExecutor) Run(ctx context.Context, req reports.Request, sink reports.RowSink) error {
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
	matchStart := startDate.Format("2006-01-02")
	matchEnd := endDate.Format("2006-01-02")
	match := payableTripBaseMatch(matchStart, matchEnd)
	if officeID, ok, err := dailySalesOfficeID(req.Parameters); err != nil {
		return err
	} else if ok {
		match["office_id"] = officeID
	}

	payableDistanceExpr := tripPayableDistanceExpr()
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
	}
	pipeline = append(pipeline, tripOfficeLookupStages()...)
	pipeline = append(pipeline,
		bson.D{{Key: "$addFields", Value: bson.M{
			"allowance_worker_id": bson.M{"$cond": bson.A{
				bson.M{"$and": bson.A{
					"$is_self_drive",
					bson.M{"$ne": bson.A{"$beautician_id", nil}},
				}},
				"$beautician_id",
				"$rider_id",
			}},
			"payable_distance_km": payableDistanceExpr,
			"petrol_payable":      tripPetrolPayableExpr(payableDistanceExpr),
		}}},
		bson.D{{Key: "$group", Value: bson.M{
			"_id":            "$allowance_worker_id",
			"total_distance": bson.M{"$sum": "$payable_distance_km"},
			"total_amount":   bson.M{"$sum": "$petrol_payable"},
		}}},
		bson.D{{Key: "$match", Value: bson.M{
			"_id": bson.M{"$ne": nil},
		}}},
		bson.D{{Key: "$lookup", Value: bson.M{
			"from":         "riders",
			"localField":   "_id",
			"foreignField": "_id",
			"as":           "rider",
		}}},
		bson.D{{Key: "$unwind", Value: bson.M{
			"path":                       "$rider",
			"preserveNullAndEmptyArrays": true,
		}}},
		bson.D{{Key: "$lookup", Value: bson.M{
			"from":         "beauticians",
			"localField":   "_id",
			"foreignField": "_id",
			"as":           "beautician",
		}}},
		bson.D{{Key: "$unwind", Value: bson.M{
			"path":                       "$beautician",
			"preserveNullAndEmptyArrays": true,
		}}},
		bson.D{{Key: "$project", Value: bson.M{
			"rider_name":     bson.M{"$ifNull": bson.A{"$rider.name", "$beautician.name"}},
			"emp_code":       bson.M{"$ifNull": bson.A{"$rider.emp_code", "$beautician.emp_code"}},
			"total_distance": 1,
			"total_amount":   1,
		}}},
	)

	if req.Limit > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: req.Limit}})
	}

	cursor, err := e.db.Collection("trips").Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	// Header
	sink.WriteRow([]interface{}{"Staff ID", "Employee Code", "Rider Name", "Total Distance (KM)", "Payable Amount"})

	for cursor.Next(ctx) {
		var result struct {
			ID            primitive.ObjectID `bson:"_id"`
			RiderName     string             `bson:"rider_name"`
			EmpCode       string             `bson:"emp_code"`
			TotalDistance float64            `bson:"total_distance"`
			TotalAmount   float64            `bson:"total_amount"`
		}
		if err := cursor.Decode(&result); err != nil {
			return err
		}
		sink.WriteRow([]interface{}{
			result.ID.Hex(),
			result.EmpCode,
			result.RiderName,
			fmt.Sprintf("%.2f", result.TotalDistance),
			fmt.Sprintf("%.2f", result.TotalAmount),
		})
	}

	return nil
}
