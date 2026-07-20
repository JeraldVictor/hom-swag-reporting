package static

import (
	"context"
	"fmt"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/leaderboard"
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

func (e *RiderCommissionExecutor) Columns() []reports.Column {
	return withColumnDescriptions(riderCommissionColumns)
}

func (e *RiderCommissionExecutor) Validate(ctx context.Context, req reports.Request) error {
	return validateReportDateRange(req.Parameters, parseReportDate)
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
	match := payableTripBaseMatch(startDateKey, endDateKey)
	if staffID, ok := reportObjectID(req.Parameters, "staff_id"); ok {
		match["$and"] = append(match["$and"].(bson.A), bson.M{"$or": bson.A{
			bson.M{"rider_id": staffID},
			bson.M{"driver_beautician_id": staffID},
			bson.M{"beautician_id": staffID, "is_self_drive": true},
		}})
	}

	var officeID primitive.ObjectID
	if officeIDStr, ok := req.Parameters["office_id"].(string); ok && officeIDStr != "" {
		parsedOfficeID, err := primitive.ObjectIDFromHex(officeIDStr)
		if err != nil {
			return fmt.Errorf("invalid office_id: %w", err)
		}
		officeID = parsedOfficeID
		match["office_id"] = officeID
	}

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
		bson.D{{Key: "$addFields", Value: bson.M{
			"commission_payable": tripSnapshotOrLegacyExpr("commission_payable", bson.M{
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
			),
		}}},
		bson.D{{Key: "$group", Value: bson.M{
			"_id":                "$allowance_worker_id",
			"total_distance_km":  bson.M{"$sum": "$payable_distance_km"},
			"petrol_payable":     bson.M{"$sum": "$petrol_payable"},
			"commission_payable": bson.M{"$sum": "$commission_payable"},
			"trip_count":         bson.M{"$sum": 1},
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
			"name":               bson.M{"$ifNull": bson.A{"$rider.name", "$beautician.name"}},
			"emp_code":           bson.M{"$ifNull": bson.A{"$rider.emp_code", "$beautician.emp_code"}},
			"total_distance_km":  1,
			"petrol_payable":     1,
			"commission_payable": 1,
			"trip_count":         1,
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

	var rows []riderCommissionRow
	for cursor.Next(ctx) {
		var result riderCommissionRow
		if err := cursor.Decode(&result); err != nil {
			return err
		}
		rows = append(rows, result)
	}
	if err := cursor.Err(); err != nil {
		return err
	}

	leaderboardByRider, err := e.getLeaderboardBonusByRider(ctx, officeID, rows)
	if err != nil {
		return err
	}

	// Header
	sink.WriteRow([]interface{}{
		"Staff ID",
		"Employee Code",
		"Rider Name",
		"Trip Count",
		"Total Distance KM",
		"Petrol Payable",
		"Trip Commission",
		"Leaderboard Rank",
		"Leaderboard Bonus",
		"Total Commission",
	})

	for _, result := range rows {
		leaderboard := leaderboardByRider[result.ID]
		totalCommission := roundPayment(result.CommissionPayable + leaderboard.Bonus)
		sink.WriteRow([]interface{}{
			result.ID.Hex(),
			result.EmpCode,
			result.Name,
			result.TripCount,
			fmt.Sprintf("%.2f", result.TotalDistanceKM),
			fmt.Sprintf("%.2f", result.PetrolPayable),
			fmt.Sprintf("%.2f", result.CommissionPayable),
			formatRank(leaderboard.Rank),
			fmt.Sprintf("%.2f", leaderboard.Bonus),
			fmt.Sprintf("%.2f", totalCommission),
		})
	}

	return nil
}

type riderCommissionRow struct {
	ID                primitive.ObjectID `bson:"_id"`
	Name              string             `bson:"name"`
	EmpCode           string             `bson:"emp_code"`
	TripCount         int                `bson:"trip_count"`
	TotalDistanceKM   float64            `bson:"total_distance_km"`
	PetrolPayable     float64            `bson:"petrol_payable"`
	CommissionPayable float64            `bson:"commission_payable"`
}

func (e *RiderCommissionExecutor) getLeaderboardBonusByRider(
	ctx context.Context,
	officeID primitive.ObjectID,
	rows []riderCommissionRow,
) (map[primitive.ObjectID]riderLeaderboardBonus, error) {
	bonusByRider := map[primitive.ObjectID]riderLeaderboardBonus{}
	if officeID.IsZero() {
		return bonusByRider, nil
	}

	prizes, err := e.getRiderLeaderboardPrizes(ctx, officeID)
	if err != nil {
		return nil, err
	}

	scores := make([]leaderboard.RiderScore, len(rows))
	for index, row := range rows {
		scores[index] = leaderboard.RiderScore{WorkerID: row.ID, TripCount: row.TripCount, TotalDistanceKM: row.TotalDistanceKM}
	}
	for _, award := range leaderboard.RankRiders(scores, prizes) {
		bonusByRider[award.WorkerID] = riderLeaderboardBonus{Rank: award.Rank, Bonus: award.Bonus}
	}
	return bonusByRider, nil
}

type riderLeaderboardBonus struct {
	Rank  int
	Bonus float64
}

func (e *RiderCommissionExecutor) getRiderLeaderboardPrizes(ctx context.Context, officeID primitive.ObjectID) ([]float64, error) {
	var office struct {
		LeaderboardPrizes struct {
			Rider []float64 `bson:"rider"`
		} `bson:"leaderboard_prizes"`
	}
	err := e.db.Collection("offices").FindOne(ctx, bson.M{"_id": officeID}).Decode(&office)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return office.LeaderboardPrizes.Rider, err
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
