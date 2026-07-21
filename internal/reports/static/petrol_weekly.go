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
	db           *mongo.Database
	mode         string
	modeProvider EarningsModeProvider
}

func NewPetrolWeeklyExecutor(db *mongo.Database) *PetrolWeeklyExecutor {
	return NewPetrolWeeklyExecutorWithMode(db, "shadow")
}

// NewPetrolWeeklyExecutorWithMode keeps the existing trip-backed report in
// shadow mode and switches the payable amount to the earnings ledger only
// after the service is explicitly made authoritative.
func NewPetrolWeeklyExecutorWithMode(db *mongo.Database, mode string) *PetrolWeeklyExecutor {
	return &PetrolWeeklyExecutor{db: db, mode: mode}
}

func NewPetrolWeeklyExecutorWithModeProvider(db *mongo.Database, mode string, provider EarningsModeProvider) *PetrolWeeklyExecutor {
	return &PetrolWeeklyExecutor{db: db, mode: mode, modeProvider: provider}
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
	officeID, hasOfficeID, err := dailySalesOfficeID(req.Parameters)
	if err != nil {
		return err
	}
	if hasOfficeID {
		match["office_id"] = officeID
	}

	mode, err := resolveEarningsMode(ctx, e.mode, e.modeProvider, officeID)
	if err != nil {
		return fmt.Errorf("resolve earnings mode: %w", err)
	}
	if mode == "authoritative" {
		return e.runLedger(ctx, req, sink, matchStart, matchEnd, officeID, hasOfficeID)
	}
	return e.runLegacy(ctx, req, sink, match)
}

func (e *PetrolWeeklyExecutor) runLegacy(ctx context.Context, req reports.Request, sink reports.RowSink, match bson.M) error {
	legacyPayableDistanceExpr := tripPayableDistanceExpr()
	payableDistanceExpr := tripSnapshotOrLegacyExpr("payable_distance_km", legacyPayableDistanceExpr)
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
	}
	pipeline = append(pipeline, tripOfficeLookupStages()...)
	pipeline = append(pipeline,
		bson.D{{Key: "$addFields", Value: bson.M{
			"allowance_worker_id": tripAllowanceWorkerIDExpr(),
			"payable_distance_km": payableDistanceExpr,
			"petrol_payable":      tripSnapshotOrLegacyExpr("petrol_payable", tripPetrolPayableExpr(payableDistanceExpr)),
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

func (e *PetrolWeeklyExecutor) runLedger(ctx context.Context, req reports.Request, sink reports.RowSink, startDate, endDate string, officeID primitive.ObjectID, hasOfficeID bool) error {
	match := bson.M{
		"component":         "petrol",
		"settlement_bucket": "petrol",
		"service_date_key":  bson.M{"$gte": startDate, "$lte": endDate},
		"source_type":       "trips",
		"source_id":         bson.M{"$ne": nil},
		"status":            bson.M{"$ne": "void"},
	}
	if hasOfficeID {
		match["office_id"] = officeID
	}

	// Distance remains a descriptive report column and is reconstructed from
	// the trip referenced by the ledger entry. The payable amount itself is
	// never recalculated here: amount_paise is the sole monetary source.
	distanceExpr := tripSnapshotOrLegacyExpr("payable_distance_km", tripPayableDistanceExpr())
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$lookup", Value: bson.M{
			"from": "trips", "let": bson.M{"trip_id": "$source_id"},
			"pipeline": bson.A{
				bson.M{"$match": bson.M{"$expr": bson.M{"$eq": bson.A{"$_id", "$$trip_id"}}}},
				bson.M{"$project": bson.M{"payable_distance_km": distanceExpr}},
			},
			"as": "source_trip",
		}}},
		{{Key: "$unwind", Value: bson.M{"path": "$source_trip", "preserveNullAndEmptyArrays": true}}},
		{{Key: "$group", Value: bson.M{
			"_id":                "$worker_id",
			"total_distance":     bson.M{"$sum": bson.M{"$ifNull": bson.A{"$source_trip.payable_distance_km", 0}}},
			"total_amount_paise": bson.M{"$sum": "$amount_paise"},
		}}},
		{{Key: "$lookup", Value: bson.M{"from": "riders", "localField": "_id", "foreignField": "_id", "as": "rider"}}},
		{{Key: "$unwind", Value: bson.M{"path": "$rider", "preserveNullAndEmptyArrays": true}}},
		{{Key: "$lookup", Value: bson.M{"from": "beauticians", "localField": "_id", "foreignField": "_id", "as": "beautician"}}},
		{{Key: "$unwind", Value: bson.M{"path": "$beautician", "preserveNullAndEmptyArrays": true}}},
		{{Key: "$project", Value: bson.M{
			"rider_name":         bson.M{"$ifNull": bson.A{"$rider.name", "$beautician.name"}},
			"emp_code":           bson.M{"$ifNull": bson.A{"$rider.emp_code", "$beautician.emp_code"}},
			"total_distance":     1,
			"total_amount_paise": 1,
		}}},
	}
	if req.Limit > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: req.Limit}})
	}

	cursor, err := e.db.Collection("earnings_ledger").Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	if err := sink.WriteRow([]interface{}{"Staff ID", "Employee Code", "Rider Name", "Total Distance (KM)", "Payable Amount"}); err != nil {
		return err
	}
	for cursor.Next(ctx) {
		var result struct {
			ID               primitive.ObjectID `bson:"_id"`
			RiderName        string             `bson:"rider_name"`
			EmpCode          string             `bson:"emp_code"`
			TotalDistance    float64            `bson:"total_distance"`
			TotalAmountPaise int64              `bson:"total_amount_paise"`
		}
		if err := cursor.Decode(&result); err != nil {
			return err
		}
		if err := sink.WriteRow([]interface{}{
			result.ID.Hex(), result.EmpCode, result.RiderName,
			fmt.Sprintf("%.2f", result.TotalDistance),
			fmt.Sprintf("%.2f", float64(result.TotalAmountPaise)/100),
		}); err != nil {
			return err
		}
	}
	return cursor.Err()
}
