package static

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/leaderboard"
	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type RiderCommissionExecutor struct {
	db           *mongo.Database
	mode         string
	modeProvider EarningsModeProvider
}

func NewRiderCommissionExecutor(db *mongo.Database) *RiderCommissionExecutor {
	return NewRiderCommissionExecutorWithMode(db, "shadow")
}

// NewRiderCommissionExecutorWithMode preserves the trip-backed calculation in
// shadow mode. Only the explicit authoritative mode reads payable amounts and
// leaderboard awards from the immutable earnings ledger.
func NewRiderCommissionExecutorWithMode(db *mongo.Database, mode string) *RiderCommissionExecutor {
	return &RiderCommissionExecutor{db: db, mode: mode}
}

func NewRiderCommissionExecutorWithModeProvider(db *mongo.Database, mode string, provider EarningsModeProvider) *RiderCommissionExecutor {
	return &RiderCommissionExecutor{db: db, mode: mode, modeProvider: provider}
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
	staffID, hasStaffID := reportObjectID(req.Parameters, "staff_id")

	var officeID primitive.ObjectID
	if officeIDStr, ok := req.Parameters["office_id"].(string); ok && officeIDStr != "" {
		parsedOfficeID, err := primitive.ObjectIDFromHex(officeIDStr)
		if err != nil {
			return fmt.Errorf("invalid office_id: %w", err)
		}
		officeID = parsedOfficeID
		match["office_id"] = officeID
	}
	mode, err := resolveEarningsMode(ctx, e.mode, e.modeProvider, officeID)
	if err != nil {
		return fmt.Errorf("resolve earnings mode: %w", err)
	}
	if mode == "authoritative" {
		filteredMatch := bson.M{}
		for key, value := range match {
			filteredMatch[key] = value
		}
		if hasStaffID {
			andConditions := append(bson.A{}, match["$and"].(bson.A)...)
			filteredMatch["$and"] = append(andConditions, bson.M{"$or": bson.A{
				bson.M{"rider_id": staffID},
				bson.M{"driver_beautician_id": staffID},
				bson.M{"beautician_id": staffID, "is_self_drive": true},
			}})
		}
		return e.runLedger(ctx, req, sink, startDateKey, endDateKey, officeID, !officeID.IsZero(), staffID, hasStaffID, filteredMatch, match)
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

	if req.Limit > 0 && !hasStaffID {
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
	sink.WriteRow(riderCommissionHeader())

	for _, result := range rows {
		if hasStaffID && result.ID != staffID {
			continue
		}
		leaderboard := leaderboardByRider[result.ID]
		totalCommission := roundPayment(result.CommissionPayable + leaderboard.Bonus)
		totalRiderPayable := result.PetrolPayable + result.CommissionPayable + leaderboard.Bonus
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
			fmt.Sprintf("%.2f", totalRiderPayable),
		})
	}

	return nil
}

type riderLedgerAmounts struct {
	ID                     primitive.ObjectID `bson:"_id"`
	Name                   string             `bson:"name"`
	EmpCode                string             `bson:"emp_code"`
	PetrolPayablePaise     int64              `bson:"petrol_payable_paise"`
	CommissionPayablePaise int64              `bson:"commission_payable_paise"`
	LeaderboardBonusPaise  int64              `bson:"leaderboard_bonus_paise"`
	LeaderboardRank        int                `bson:"leaderboard_rank"`
}

func (e *RiderCommissionExecutor) runLedger(
	ctx context.Context,
	req reports.Request,
	sink reports.RowSink,
	startDate, endDate string,
	officeID primitive.ObjectID,
	hasOfficeID bool,
	staffID primitive.ObjectID,
	hasStaffID bool,
	tripMatch bson.M,
	rankingTripMatch bson.M,
) error {
	ledgerMatch := bson.M{
		"service_date_key": bson.M{"$gte": startDate, "$lte": endDate},
		"status":           bson.M{"$ne": "void"},
		"$or": bson.A{
			bson.M{"component": bson.M{"$in": bson.A{"trip_commission", "petrol"}}, "source_type": "trips", "source_id": bson.M{"$ne": nil}},
			bson.M{
				"component": "leaderboard_bonus", "configuration_snapshot.leaderboard_type": "rider",
				"configuration_snapshot.period_start": startDate, "configuration_snapshot.period_end": endDate,
			},
		},
	}
	if hasOfficeID {
		ledgerMatch["office_id"] = officeID
	}
	if hasStaffID {
		ledgerMatch["worker_id"] = staffID
	}

	ledgerPipeline := mongo.Pipeline{
		{{Key: "$match", Value: ledgerMatch}},
		{{Key: "$group", Value: bson.M{
			"_id": "$worker_id",
			"petrol_payable_paise": bson.M{"$sum": bson.M{"$cond": bson.A{
				bson.M{"$eq": bson.A{"$component", "petrol"}}, "$amount_paise", 0,
			}}},
			"commission_payable_paise": bson.M{"$sum": bson.M{"$cond": bson.A{
				bson.M{"$eq": bson.A{"$component", "trip_commission"}}, "$amount_paise", 0,
			}}},
			"leaderboard_bonus_paise": bson.M{"$sum": bson.M{"$cond": bson.A{
				bson.M{"$eq": bson.A{"$component", "leaderboard_bonus"}}, "$amount_paise", 0,
			}}},
			"leaderboard_rank": bson.M{"$min": bson.M{"$cond": bson.A{
				bson.M{"$eq": bson.A{"$component", "leaderboard_bonus"}}, "$configuration_snapshot.rank", 2147483647,
			}}},
		}}},
		{{Key: "$lookup", Value: bson.M{"from": "riders", "localField": "_id", "foreignField": "_id", "as": "rider"}}},
		{{Key: "$unwind", Value: bson.M{"path": "$rider", "preserveNullAndEmptyArrays": true}}},
		{{Key: "$lookup", Value: bson.M{"from": "beauticians", "localField": "_id", "foreignField": "_id", "as": "beautician"}}},
		{{Key: "$unwind", Value: bson.M{"path": "$beautician", "preserveNullAndEmptyArrays": true}}},
		{{Key: "$project", Value: bson.M{
			"name":                 bson.M{"$ifNull": bson.A{"$rider.name", "$beautician.name"}},
			"emp_code":             bson.M{"$ifNull": bson.A{"$rider.emp_code", "$beautician.emp_code"}},
			"petrol_payable_paise": 1, "commission_payable_paise": 1,
			"leaderboard_bonus_paise": 1,
			"leaderboard_rank": bson.M{"$cond": bson.A{
				bson.M{"$eq": bson.A{"$leaderboard_rank", 2147483647}}, 0, "$leaderboard_rank",
			}},
		}}},
	}
	ledgerCursor, err := e.db.Collection("earnings_ledger").Aggregate(ctx, ledgerPipeline)
	if err != nil {
		return err
	}
	defer ledgerCursor.Close(ctx)

	amountsByWorker := make(map[primitive.ObjectID]riderLedgerAmounts)
	for ledgerCursor.Next(ctx) {
		var amount riderLedgerAmounts
		if err := ledgerCursor.Decode(&amount); err != nil {
			return err
		}
		amountsByWorker[amount.ID] = amount
	}
	if err := ledgerCursor.Err(); err != nil {
		return err
	}

	// Trips contribute descriptive columns only. They are deliberately queried
	// independently because zero-value trip/payable entries are not materialized
	// in the ledger and must still count as trips in the report.
	distanceExpr := tripSnapshotOrLegacyExpr("payable_distance_km", tripPayableDistanceExpr())
	tripPipeline := mongo.Pipeline{{{Key: "$match", Value: tripMatch}}}
	tripPipeline = append(tripPipeline, tripOfficeLookupStages()...)
	tripPipeline = append(tripPipeline,
		bson.D{{Key: "$addFields", Value: bson.M{"allowance_worker_id": tripAllowanceWorkerIDExpr(), "payable_distance_km": distanceExpr}}},
		bson.D{{Key: "$group", Value: bson.M{
			"_id": "$allowance_worker_id", "total_distance_km": bson.M{"$sum": "$payable_distance_km"}, "trip_count": bson.M{"$sum": 1},
		}}},
		bson.D{{Key: "$match", Value: bson.M{"_id": bson.M{"$ne": nil}}}},
		bson.D{{Key: "$lookup", Value: bson.M{"from": "riders", "localField": "_id", "foreignField": "_id", "as": "rider"}}},
		bson.D{{Key: "$unwind", Value: bson.M{"path": "$rider", "preserveNullAndEmptyArrays": true}}},
		bson.D{{Key: "$lookup", Value: bson.M{"from": "beauticians", "localField": "_id", "foreignField": "_id", "as": "beautician"}}},
		bson.D{{Key: "$unwind", Value: bson.M{"path": "$beautician", "preserveNullAndEmptyArrays": true}}},
		bson.D{{Key: "$project", Value: bson.M{
			"name":              bson.M{"$ifNull": bson.A{"$rider.name", "$beautician.name"}},
			"emp_code":          bson.M{"$ifNull": bson.A{"$rider.emp_code", "$beautician.emp_code"}},
			"total_distance_km": 1, "trip_count": 1,
		}}},
	)
	tripCursor, err := e.db.Collection("trips").Aggregate(ctx, tripPipeline)
	if err != nil {
		return err
	}
	defer tripCursor.Close(ctx)

	rowsByWorker := make(map[primitive.ObjectID]riderCommissionRow)
	for tripCursor.Next(ctx) {
		var row riderCommissionRow
		if err := tripCursor.Decode(&row); err != nil {
			return err
		}
		rowsByWorker[row.ID] = row
	}
	if err := tripCursor.Err(); err != nil {
		return err
	}

	// Winning ranks are snapshotted on bonus ledger entries, but non-winning
	// staff intentionally have no bonus entry. A staff-filtered authoritative
	// report must therefore rank the selected rider against the whole office's
	// trip statistics, not display an empty rank or rank the one visible row as
	// first.
	ranksByWorker := rankRiderRows(rowsByWorker)
	if hasStaffID && amountsByWorker[staffID].LeaderboardRank == 0 {
		rankingRows, err := e.loadRiderRankingRows(ctx, rankingTripMatch)
		if err != nil {
			return err
		}
		ranksByWorker = rankRiderRows(rankingRows)
	}

	workerIDs := make([]primitive.ObjectID, 0, len(rowsByWorker)+len(amountsByWorker))
	seen := make(map[primitive.ObjectID]struct{}, len(rowsByWorker)+len(amountsByWorker))
	for id := range rowsByWorker {
		seen[id] = struct{}{}
		workerIDs = append(workerIDs, id)
	}
	for id := range amountsByWorker {
		if _, exists := seen[id]; !exists {
			workerIDs = append(workerIDs, id)
		}
	}
	sort.Slice(workerIDs, func(i, j int) bool { return workerIDs[i].Hex() < workerIDs[j].Hex() })
	if req.Limit > 0 && len(workerIDs) > req.Limit {
		workerIDs = workerIDs[:req.Limit]
	}

	if err := sink.WriteRow(riderCommissionHeader()); err != nil {
		return err
	}
	for _, id := range workerIDs {
		row := rowsByWorker[id]
		amount := amountsByWorker[id]
		if row.Name == "" {
			row.Name, row.EmpCode = amount.Name, amount.EmpCode
		}
		petrol := float64(amount.PetrolPayablePaise) / 100
		commission := float64(amount.CommissionPayablePaise) / 100
		bonus := float64(amount.LeaderboardBonusPaise) / 100
		rank := amount.LeaderboardRank
		if sourceRank := ranksByWorker[id]; sourceRank > 0 && (!hasStaffID || rank == 0) {
			rank = sourceRank
		}
		if err := sink.WriteRow([]interface{}{
			id.Hex(), row.EmpCode, row.Name, row.TripCount, fmt.Sprintf("%.2f", row.TotalDistanceKM),
			fmt.Sprintf("%.2f", petrol), fmt.Sprintf("%.2f", commission), formatRank(rank),
			fmt.Sprintf("%.2f", bonus), fmt.Sprintf("%.2f", commission+bonus),
			fmt.Sprintf("%.2f", petrol+commission+bonus),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (e *RiderCommissionExecutor) loadRiderRankingRows(ctx context.Context, match bson.M) (map[primitive.ObjectID]riderCommissionRow, error) {
	distanceExpr := tripSnapshotOrLegacyExpr("payable_distance_km", tripPayableDistanceExpr())
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$addFields", Value: bson.M{"allowance_worker_id": tripAllowanceWorkerIDExpr(), "payable_distance_km": distanceExpr}}},
		{{Key: "$group", Value: bson.M{
			"_id": "$allowance_worker_id", "total_distance_km": bson.M{"$sum": "$payable_distance_km"}, "trip_count": bson.M{"$sum": 1},
		}}},
		{{Key: "$match", Value: bson.M{"_id": bson.M{"$ne": nil}}}},
	}
	cursor, err := e.db.Collection("trips").Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	rows := map[primitive.ObjectID]riderCommissionRow{}
	for cursor.Next(ctx) {
		var row riderCommissionRow
		if err := cursor.Decode(&row); err != nil {
			return nil, err
		}
		rows[row.ID] = row
	}
	return rows, cursor.Err()
}

func rankRiderRows(rows map[primitive.ObjectID]riderCommissionRow) map[primitive.ObjectID]int {
	scores := make([]leaderboard.RiderScore, 0, len(rows))
	for _, row := range rows {
		scores = append(scores, leaderboard.RiderScore{WorkerID: row.ID, TripCount: row.TripCount, TotalDistanceKM: row.TotalDistanceKM})
	}
	ranks := make(map[primitive.ObjectID]int, len(scores))
	for _, award := range leaderboard.RankRiders(scores, nil) {
		ranks[award.WorkerID] = award.Rank
	}
	return ranks
}

func riderCommissionHeader() []interface{} {
	return []interface{}{
		"Staff ID", "Employee Code", "Rider Name", "Trip Count", "Total Distance KM",
		"Petrol Payable", "Trip Commission", "Leaderboard Rank", "Leaderboard Bonus", "Total Commission",
		"Total Rider Payable",
	}
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
