package static

import (
	"context"
	"fmt"
	"sort"
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

func (e *RiderCommissionExecutor) Columns() []reports.Column {
	return riderCommissionColumns
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
	match := bson.M{
		"date": bson.M{
			"$gte": startDateKey,
			"$lte": endDateKey,
		},
		"is_deleted":   false,
		"kanban_state": "trip_completed",
	}
	if staffID, ok := reportObjectID(req.Parameters, "staff_id"); ok {
		match["$or"] = bson.A{
			bson.M{"rider_id": staffID},
			bson.M{"beautician_id": staffID, "is_self_drive": true},
		}
	}

	var officeID primitive.ObjectID
	if officeIDStr, ok := req.Parameters["office_id"].(string); ok && officeIDStr != "" {
		parsedOfficeID, err := primitive.ObjectIDFromHex(officeIDStr)
		if err != nil {
			return fmt.Errorf("invalid office_id: %w", err)
		}
		officeID = parsedOfficeID
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
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

	if !officeID.IsZero() {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{"rider.office_id": officeID}}})
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

	rankedRows := append([]riderCommissionRow(nil), rows...)
	sort.Slice(rankedRows, func(i, j int) bool {
		if rankedRows[i].TripCount == rankedRows[j].TripCount {
			return rankedRows[i].TotalDistanceKM > rankedRows[j].TotalDistanceKM
		}
		return rankedRows[i].TripCount > rankedRows[j].TripCount
	})

	prizes, err := e.getRiderLeaderboardPrizes(ctx, officeID)
	if err != nil {
		return nil, err
	}

	for index, row := range rankedRows {
		bonus := 0.0
		if index < len(prizes) {
			bonus = prizes[index]
		}
		bonusByRider[row.ID] = riderLeaderboardBonus{
			Rank:  index + 1,
			Bonus: bonus,
		}
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
