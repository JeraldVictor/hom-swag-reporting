package static

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/leaderboard"
	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type BeauticianCommissionExecutor struct {
	db           *mongo.Database
	mode         string
	modeProvider EarningsModeProvider
}

func NewBeauticianCommissionExecutor(db *mongo.Database) *BeauticianCommissionExecutor {
	return NewBeauticianCommissionExecutorWithMode(db, "shadow")
}

// NewBeauticianCommissionExecutorWithMode preserves the order-backed report
// unless earnings has explicitly been promoted to the authoritative source.
func NewBeauticianCommissionExecutorWithMode(db *mongo.Database, mode string) *BeauticianCommissionExecutor {
	return &BeauticianCommissionExecutor{db: db, mode: mode}
}

func NewBeauticianCommissionExecutorWithModeProvider(db *mongo.Database, mode string, provider EarningsModeProvider) *BeauticianCommissionExecutor {
	return &BeauticianCommissionExecutor{db: db, mode: mode, modeProvider: provider}
}

func (e *BeauticianCommissionExecutor) Key() string {
	return "beautician_commission"
}

func (e *BeauticianCommissionExecutor) Version() int {
	return 1
}

func (e *BeauticianCommissionExecutor) Columns() []reports.Column {
	return withColumnDescriptions(beauticianCommissionColumns)
}

func (e *BeauticianCommissionExecutor) Validate(ctx context.Context, req reports.Request) error {
	return validateReportDateRange(req.Parameters, parseReportDate)
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
	startDateKey := startDate.Format("2006-01-02")
	endDateKey := endDate.Format("2006-01-02")
	match := orderBookingDateOnlyMatch(startDateKey, endDateKey)
	match["status"] = bson.M{"$in": bson.A{"completed", "cancelled_and_refunded"}}
	var reportStaffID primitive.ObjectID
	if staffID, ok := reportObjectID(req.Parameters, "staff_id"); ok {
		reportStaffID = staffID
		match["beautician_id"] = staffID
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
	mode, err := resolveEarningsMode(ctx, e.mode, e.modeProvider, officeID)
	if err != nil {
		return fmt.Errorf("resolve earnings mode: %w", err)
	}

	ledgerByBeautician := map[primitive.ObjectID]beauticianLedgerTotals{}
	if mode == "authoritative" {
		ledgerByBeautician, err = e.getLedgerTotalsByBeautician(
			ctx, officeID, reportStaffID, startDateKey, endDateKey, monthEndDateKey(endDate),
		)
		if err != nil {
			return err
		}
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$group", Value: bson.M{
			"_id": "$beautician_id",
			"total_special_commission": bson.M{"$sum": completedOnlyExpr(bson.M{"$ifNull": bson.A{
				"$commission_snapshot.special_commission",
				"$commission_details.special_commission",
				0,
			}})},
			"total_general_commission": bson.M{"$sum": completedOnlyExpr(bson.M{"$ifNull": bson.A{
				"$commission_snapshot.general_commission",
				"$commission_details.general_commission",
				0,
			}})},
			"total_upgrade_addon_commission": bson.M{"$sum": completedOnlyExpr(bson.M{"$ifNull": bson.A{
				"$commission_snapshot.upgrade_addon_commission",
				"$commission_details.upgrade_addon_commission",
				0,
			}})},
			"total_revenue": bson.M{
				"$sum": completedOnlyExpr(bson.M{
					"$ifNull": bson.A{
						"$commission_snapshot.order_cost",
						"$order_cost",
						"$revenue",
					},
				}),
			},
			"total_refund": bson.M{"$sum": paymentRefundExpr()},
			"order_count":  bson.M{"$sum": completedOnlyExpr(1)},
		}}},
		{{Key: "$lookup", Value: bson.M{
			"from":         "beauticians",
			"localField":   "_id",
			"foreignField": "_id",
			"as":           "beautician",
		}}},
		{{Key: "$unwind", Value: "$beautician"}},
	}

	pipeline = append(pipeline, bson.D{{Key: "$project", Value: bson.M{
		"name":                           "$beautician.name",
		"emp_code":                       "$beautician.emp_code",
		"monthly_target1":                "$beautician.monthly_target1",
		"monthly_target2":                "$beautician.monthly_target2",
		"total_special_commission":       1,
		"total_general_commission":       1,
		"total_upgrade_addon_commission": 1,
		"total_revenue":                  1,
		"total_refund":                   1,
		"order_count":                    1,
	}}})

	if req.Limit > 0 && mode != "authoritative" {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: req.Limit}})
	}

	cursor, err := e.db.Collection("orders").Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	var rows []beauticianCommissionRow
	var beauticianIDs []primitive.ObjectID
	for cursor.Next(ctx) {
		var result beauticianCommissionRow
		if err := cursor.Decode(&result); err != nil {
			return err
		}
		rows = append(rows, result)
		beauticianIDs = append(beauticianIDs, result.ID)
	}
	if err := cursor.Err(); err != nil {
		return err
	}
	if mode == "authoritative" {
		seen := make(map[primitive.ObjectID]struct{}, len(rows))
		for _, row := range rows {
			seen[row.ID] = struct{}{}
		}
		for workerID, ledger := range ledgerByBeautician {
			if _, exists := seen[workerID]; exists {
				continue
			}
			rows = append(rows, beauticianCommissionRow{
				ID: workerID, Name: ledger.Name, EmpCode: ledger.EmpCode,
				MonthlyTarget1: ledger.MonthlyTarget1, MonthlyTarget2: ledger.MonthlyTarget2,
			})
			beauticianIDs = append(beauticianIDs, workerID)
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].ID.Hex() < rows[j].ID.Hex() })
		if req.Limit > 0 && len(rows) > req.Limit {
			rows = rows[:req.Limit]
			beauticianIDs = beauticianIDs[:0]
			for _, row := range rows {
				beauticianIDs = append(beauticianIDs, row.ID)
			}
		}
	}
	// A staff-filtered overview must still expose configured targets when the
	// worker has no orders in the selected period. This avoids making clients
	// recreate target defaults outside the reporting service.
	if len(rows) == 0 && !reportStaffID.IsZero() {
		profileFilter := bson.M{"_id": reportStaffID, "is_deleted": bson.M{"$ne": true}}
		if !officeID.IsZero() {
			profileFilter["office_id"] = officeID
		}
		var profile struct {
			ID             primitive.ObjectID `bson:"_id"`
			Name           string             `bson:"name"`
			EmpCode        string             `bson:"emp_code"`
			MonthlyTarget1 float64            `bson:"monthly_target1"`
			MonthlyTarget2 float64            `bson:"monthly_target2"`
		}
		if err := e.db.Collection("beauticians").FindOne(ctx, profileFilter).Decode(&profile); err != nil {
			if err != mongo.ErrNoDocuments {
				return err
			}
		} else {
			rows = append(rows, beauticianCommissionRow{
				ID: profile.ID, Name: profile.Name, EmpCode: profile.EmpCode,
				MonthlyTarget1: profile.MonthlyTarget1, MonthlyTarget2: profile.MonthlyTarget2,
			})
			beauticianIDs = append(beauticianIDs, profile.ID)
		}
	}

	monthStart, monthEnd := commissionTargetMonthBounds(endDate)
	monthlyRevenueByBeautician, err := e.getMonthlyRevenueByBeautician(ctx, beauticianIDs, officeID, monthStart, monthEnd)
	if err != nil {
		return err
	}
	leaderboardByBeautician := map[primitive.ObjectID]beauticianLeaderboardBonus{}
	if mode != "authoritative" {
		leaderboardByBeautician, err = e.getLeaderboardBonusByBeautician(ctx, officeID, startDate, endDate)
		if err != nil {
			return err
		}
	}
	target2Bonus, err := e.getOfficeTarget2Bonus(ctx, officeID)
	if err != nil {
		return err
	}

	// Header
	sink.WriteRow([]interface{}{
		"Staff ID",
		"Employee Code",
		"Beautician Name",
		"Order Count",
		"Total Revenue",
		"Target 1",
		"Target 1 Achieved",
		"Target 2",
		"Target 2 Achieved",
		"Special Commission",
		"Payable General Commission",
		"Potential General Commission",
		"Upgrade/Add-on Commission",
		"Refunded",
		"Target 2 Bonus",
		"Potential Target 2 Bonus",
		"Leaderboard Rank",
		"Leaderboard Bonus",
		"Total Commission",
		"Estimated Commission at Target 1",
		"Estimated Commission at Target 2",
	})

	for _, result := range rows {
		monthlyRevenue := monthlyRevenueByBeautician[result.ID]
		target1Achieved := monthlyRevenue >= result.MonthlyTarget1
		target2Achieved := result.MonthlyTarget2 > 0 && monthlyRevenue >= result.MonthlyTarget2
		payableGeneralCommission := 0.0
		if target1Achieved {
			payableGeneralCommission = result.TotalGeneralCommission
		}
		payableTarget2Bonus := 0.0
		if target2Achieved {
			payableTarget2Bonus = target2Bonus
		}
		leaderboard := leaderboardByBeautician[result.ID]
		if mode == "authoritative" {
			ledger := ledgerByBeautician[result.ID]
			result.TotalSpecialCommission = paiseToMoney(ledger.SpecialCommissionPaise)
			payableGeneralCommission = paiseToMoney(ledger.GeneralCommissionPaise)
			result.TotalUpgradeAddonCommission = paiseToMoney(ledger.UpgradeCommissionPaise)
			payableTarget2Bonus = paiseToMoney(ledger.TargetBonusPaise)
			leaderboard = beauticianLeaderboardBonus{
				Rank: ledger.LeaderboardRank, Bonus: paiseToMoney(ledger.LeaderboardBonusPaise),
			}
		}
		totalCommission := roundPayment(
			result.TotalSpecialCommission +
				payableGeneralCommission +
				result.TotalUpgradeAddonCommission +
				payableTarget2Bonus +
				leaderboard.Bonus,
		)
		estimatedTarget1Commission := math.Max(totalCommission, roundPayment(
			result.TotalSpecialCommission+
				result.TotalGeneralCommission+
				result.TotalUpgradeAddonCommission+
				payableTarget2Bonus+
				leaderboard.Bonus,
		))
		estimatedTarget2Commission := math.Max(estimatedTarget1Commission, roundPayment(
			result.TotalSpecialCommission+
				result.TotalGeneralCommission+
				result.TotalUpgradeAddonCommission+
				target2Bonus+
				leaderboard.Bonus,
		))
		sink.WriteRow([]interface{}{
			result.ID.Hex(),
			result.EmpCode,
			result.Name,
			result.OrderCount,
			fmt.Sprintf("%.2f", result.TotalRevenue),
			fmt.Sprintf("%.2f", result.MonthlyTarget1),
			formatBool(target1Achieved),
			fmt.Sprintf("%.2f", result.MonthlyTarget2),
			formatBool(target2Achieved),
			fmt.Sprintf("%.2f", result.TotalSpecialCommission),
			fmt.Sprintf("%.2f", payableGeneralCommission),
			fmt.Sprintf("%.2f", result.TotalGeneralCommission),
			fmt.Sprintf("%.2f", result.TotalUpgradeAddonCommission),
			fmt.Sprintf("%.2f", result.TotalRefund),
			fmt.Sprintf("%.2f", payableTarget2Bonus),
			fmt.Sprintf("%.2f", target2Bonus),
			formatRank(leaderboard.Rank),
			fmt.Sprintf("%.2f", leaderboard.Bonus),
			fmt.Sprintf("%.2f", totalCommission),
			fmt.Sprintf("%.2f", estimatedTarget1Commission),
			fmt.Sprintf("%.2f", estimatedTarget2Commission),
		})
	}

	return nil
}

type beauticianCommissionRow struct {
	ID                          primitive.ObjectID `bson:"_id"`
	Name                        string             `bson:"name"`
	EmpCode                     string             `bson:"emp_code"`
	MonthlyTarget1              float64            `bson:"monthly_target1"`
	MonthlyTarget2              float64            `bson:"monthly_target2"`
	TotalSpecialCommission      float64            `bson:"total_special_commission"`
	TotalGeneralCommission      float64            `bson:"total_general_commission"`
	TotalUpgradeAddonCommission float64            `bson:"total_upgrade_addon_commission"`
	TotalRevenue                float64            `bson:"total_revenue"`
	TotalRefund                 float64            `bson:"total_refund"`
	OrderCount                  int                `bson:"order_count"`
}

type beauticianLedgerTotals struct {
	ID                     primitive.ObjectID `bson:"_id"`
	Name                   string             `bson:"name"`
	EmpCode                string             `bson:"emp_code"`
	MonthlyTarget1         float64            `bson:"monthly_target1"`
	MonthlyTarget2         float64            `bson:"monthly_target2"`
	SpecialCommissionPaise int64              `bson:"special_commission_paise"`
	GeneralCommissionPaise int64              `bson:"general_commission_paise"`
	UpgradeCommissionPaise int64              `bson:"upgrade_commission_paise"`
	TargetBonusPaise       int64              `bson:"target_bonus_paise"`
	LeaderboardBonusPaise  int64              `bson:"leaderboard_bonus_paise"`
	LeaderboardRank        int                `bson:"leaderboard_rank"`
}

func (e *BeauticianCommissionExecutor) getLedgerTotalsByBeautician(
	ctx context.Context,
	officeID primitive.ObjectID,
	staffID primitive.ObjectID,
	startDate string,
	endDate string,
	targetMonthEnd string,
) (map[primitive.ObjectID]beauticianLedgerTotals, error) {
	componentAmount := func(component string) bson.M {
		return bson.M{"$cond": bson.A{
			bson.M{"$eq": bson.A{"$component", component}}, "$amount_paise", 0,
		}}
	}
	match := bson.M{
		"worker_type":       "beautician",
		"settlement_bucket": "commission",
		"status":            bson.M{"$ne": "void"},
		"$or": bson.A{
			bson.M{
				"component":        bson.M{"$in": bson.A{"special_commission", "general_commission", "upgrade_addon_commission"}},
				"service_date_key": bson.M{"$gte": startDate, "$lte": endDate},
				"source_type":      "orders",
				"source_id":        bson.M{"$ne": nil},
			},
			bson.M{"component": "target_bonus", "service_date_key": targetMonthEnd, "source_type": "targets"},
			bson.M{
				"component":                               "leaderboard_bonus",
				"source_type":                             "leaderboards",
				"configuration_snapshot.period_start":     startDate,
				"configuration_snapshot.period_end":       endDate,
				"configuration_snapshot.leaderboard_type": "beautician",
			},
		},
	}
	if !officeID.IsZero() {
		match["office_id"] = officeID
	}
	if !staffID.IsZero() {
		match["worker_id"] = staffID
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$group", Value: bson.M{
			"_id":                      "$worker_id",
			"special_commission_paise": bson.M{"$sum": componentAmount("special_commission")},
			"general_commission_paise": bson.M{"$sum": componentAmount("general_commission")},
			"upgrade_commission_paise": bson.M{"$sum": componentAmount("upgrade_addon_commission")},
			"target_bonus_paise":       bson.M{"$sum": componentAmount("target_bonus")},
			"leaderboard_bonus_paise":  bson.M{"$sum": componentAmount("leaderboard_bonus")},
			"leaderboard_rank": bson.M{"$max": bson.M{"$cond": bson.A{
				bson.M{"$eq": bson.A{"$component", "leaderboard_bonus"}},
				bson.M{"$ifNull": bson.A{"$configuration_snapshot.rank", 0}}, 0,
			}}},
		}}},
		{{Key: "$lookup", Value: bson.M{
			"from": "beauticians", "localField": "_id", "foreignField": "_id", "as": "beautician",
		}}},
		{{Key: "$unwind", Value: bson.M{"path": "$beautician", "preserveNullAndEmptyArrays": true}}},
		{{Key: "$project", Value: bson.M{
			"name": "$beautician.name", "emp_code": "$beautician.emp_code",
			"monthly_target1": "$beautician.monthly_target1", "monthly_target2": "$beautician.monthly_target2",
			"special_commission_paise": 1, "general_commission_paise": 1,
			"upgrade_commission_paise": 1, "target_bonus_paise": 1,
			"leaderboard_bonus_paise": 1, "leaderboard_rank": 1,
		}}},
	}
	cursor, err := e.db.Collection("earnings_ledger").Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	totals := map[primitive.ObjectID]beauticianLedgerTotals{}
	for cursor.Next(ctx) {
		var row beauticianLedgerTotals
		if err := cursor.Decode(&row); err != nil {
			return nil, err
		}
		totals[row.ID] = row
	}
	return totals, cursor.Err()
}

func paiseToMoney(value int64) float64 { return float64(value) / 100 }

func monthEndDateKey(date time.Time) string {
	return time.Date(date.Year(), date.Month()+1, 1, 0, 0, 0, 0, date.Location()).AddDate(0, 0, -1).Format("2006-01-02")
}

func completedOnlyExpr(value interface{}) bson.M {
	return bson.M{"$cond": bson.A{bson.M{"$eq": bson.A{"$status", "completed"}}, value, 0}}
}

type beauticianLeaderboardBonus struct {
	Rank  int
	Bonus float64
}

func (e *BeauticianCommissionExecutor) getMonthlyRevenueByBeautician(
	ctx context.Context,
	beauticianIDs []primitive.ObjectID,
	officeID primitive.ObjectID,
	startDate time.Time,
	endDate time.Time,
) (map[primitive.ObjectID]float64, error) {
	startDateKey := startDate.Format("2006-01-02")
	endDateKey := endDate.Format("2006-01-02")
	revenueByBeautician := map[primitive.ObjectID]float64{}
	if len(beauticianIDs) == 0 {
		return revenueByBeautician, nil
	}

	match := orderBookingDateOnlyMatch(startDateKey, endDateKey)
	match["beautician_id"] = bson.M{"$in": beauticianIDs}
	match["status"] = "completed"
	if !officeID.IsZero() {
		match["office_id"] = officeID
	}

	cursor, err := e.db.Collection("orders").Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$group", Value: bson.M{
			"_id": "$beautician_id",
			"total_revenue": bson.M{"$sum": bson.M{"$ifNull": bson.A{
				"$commission_snapshot.order_cost",
				"$order_cost",
				"$revenue",
			}}},
		}}},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var row struct {
			ID           primitive.ObjectID `bson:"_id"`
			TotalRevenue float64            `bson:"total_revenue"`
		}
		if err := cursor.Decode(&row); err != nil {
			return nil, err
		}
		revenueByBeautician[row.ID] = row.TotalRevenue
	}
	return revenueByBeautician, cursor.Err()
}

func (e *BeauticianCommissionExecutor) getLeaderboardBonusByBeautician(
	ctx context.Context,
	officeID primitive.ObjectID,
	startDate time.Time,
	endDate time.Time,
) (map[primitive.ObjectID]beauticianLeaderboardBonus, error) {
	bonusByBeautician := map[primitive.ObjectID]beauticianLeaderboardBonus{}
	if officeID.IsZero() {
		return bonusByBeautician, nil
	}

	cursor, err := e.db.Collection("leaderboards").Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"office_id": officeID,
			"date":      bson.M{"$gte": startDate, "$lte": endDate},
		}}},
		{{Key: "$group", Value: bson.M{
			"_id":         "$beautician_id",
			"revenue":     bson.M{"$sum": "$revenue"},
			"order_count": bson.M{"$sum": "$order_count"},
		}}},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	type leaderboardRow struct {
		ID         primitive.ObjectID `bson:"_id"`
		Revenue    float64            `bson:"revenue"`
		OrderCount int                `bson:"order_count"`
	}
	var rows []leaderboardRow
	for cursor.Next(ctx) {
		var row leaderboardRow
		if err := cursor.Decode(&row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}

	prizes, err := e.getBeauticianLeaderboardPrizes(ctx, officeID)
	if err != nil {
		return nil, err
	}
	scores := make([]leaderboard.BeauticianScore, len(rows))
	for index, row := range rows {
		scores[index] = leaderboard.BeauticianScore{WorkerID: row.ID, Revenue: row.Revenue, OrderCount: row.OrderCount}
	}
	for _, award := range leaderboard.RankBeauticians(scores, prizes) {
		bonusByBeautician[award.WorkerID] = beauticianLeaderboardBonus{Rank: award.Rank, Bonus: award.Bonus}
	}
	return bonusByBeautician, nil
}

func (e *BeauticianCommissionExecutor) getOfficeTarget2Bonus(ctx context.Context, officeID primitive.ObjectID) (float64, error) {
	if officeID.IsZero() {
		return 0, nil
	}
	var office struct {
		MonthlyTarget2Bonus float64 `bson:"monthly_target2_bonus"`
	}
	err := e.db.Collection("offices").FindOne(ctx, bson.M{"_id": officeID}).Decode(&office)
	if err == mongo.ErrNoDocuments {
		return 0, nil
	}
	return office.MonthlyTarget2Bonus, err
}

func (e *BeauticianCommissionExecutor) getBeauticianLeaderboardPrizes(ctx context.Context, officeID primitive.ObjectID) ([]float64, error) {
	var office struct {
		LeaderboardPrizes struct {
			Beautician []float64 `bson:"beutician"`
		} `bson:"leaderboard_prizes"`
	}
	err := e.db.Collection("offices").FindOne(ctx, bson.M{"_id": officeID}).Decode(&office)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return office.LeaderboardPrizes.Beautician, err
}

func commissionTargetMonthBounds(date time.Time) (time.Time, time.Time) {
	start := time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, date.Location())
	end := start.AddDate(0, 1, 0).Add(-time.Nanosecond)
	return start, end
}

func roundPayment(value float64) float64 {
	return math.Round(value)
}

func formatBool(value bool) string {
	if value {
		return "Yes"
	}
	return "No"
}

func formatRank(rank int) string {
	if rank <= 0 {
		return "-"
	}
	return fmt.Sprintf("#%d", rank)
}
