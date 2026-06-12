package static

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

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

	var officeID primitive.ObjectID
	if officeIDStr, ok := req.Parameters["office_id"].(string); ok && officeIDStr != "" {
		parsedOfficeID, err := primitive.ObjectIDFromHex(officeIDStr)
		if err != nil {
			return fmt.Errorf("invalid office_id: %w", err)
		}
		officeID = parsedOfficeID
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"service_date": bson.M{
				"$gte": startDate,
				"$lte": endDate,
			},
			"is_deleted": false,
			"status":     bson.M{"$in": bson.A{"completed", "cancelled_and_refunded"}},
		}}},
		{{Key: "$group", Value: bson.M{
			"_id": "$beautician_id",
			"total_special_commission": bson.M{"$sum": completedOnlyExpr(bson.M{"$ifNull": bson.A{
				"$commission_details.special_commission",
				0,
			}})},
			"total_general_commission": bson.M{"$sum": completedOnlyExpr(bson.M{"$ifNull": bson.A{
				"$commission_details.general_commission",
				0,
			}})},
			"total_upgrade_addon_commission": bson.M{"$sum": completedOnlyExpr(bson.M{"$ifNull": bson.A{
				"$commission_details.upgrade_addon_commission",
				0,
			}})},
			"total_revenue": bson.M{
				"$sum": completedOnlyExpr(bson.M{
					"$ifNull": bson.A{
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

	if !officeID.IsZero() {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{"beautician.office_id": officeID}}})
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

	if req.Limit > 0 {
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

	monthStart, monthEnd := commissionTargetMonthBounds(endDate)
	monthlyRevenueByBeautician, err := e.getMonthlyRevenueByBeautician(ctx, beauticianIDs, officeID, monthStart, monthEnd)
	if err != nil {
		return err
	}
	leaderboardByBeautician, err := e.getLeaderboardBonusByBeautician(ctx, officeID, startDate, endDate)
	if err != nil {
		return err
	}
	target2Bonus, err := e.getOfficeTarget2Bonus(ctx, officeID)
	if err != nil {
		return err
	}

	// Header
	sink.WriteRow([]interface{}{
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
		"Upgrade/Add-on Commission",
		"Refunded",
		"Target 2 Bonus",
		"Leaderboard Rank",
		"Leaderboard Bonus",
		"Total Commission",
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
		totalCommission := roundPayment(
			result.TotalSpecialCommission +
				payableGeneralCommission +
				result.TotalUpgradeAddonCommission +
				payableTarget2Bonus +
				leaderboard.Bonus,
		)
		sink.WriteRow([]interface{}{
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
			fmt.Sprintf("%.2f", result.TotalUpgradeAddonCommission),
			fmt.Sprintf("%.2f", result.TotalRefund),
			fmt.Sprintf("%.2f", payableTarget2Bonus),
			formatRank(leaderboard.Rank),
			fmt.Sprintf("%.2f", leaderboard.Bonus),
			fmt.Sprintf("%.2f", totalCommission),
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
	revenueByBeautician := map[primitive.ObjectID]float64{}
	if len(beauticianIDs) == 0 {
		return revenueByBeautician, nil
	}

	match := bson.M{
		"beautician_id": bson.M{"$in": beauticianIDs},
		"service_date":  bson.M{"$gte": startDate, "$lte": endDate},
		"is_deleted":    false,
		"status":        "completed",
	}
	if !officeID.IsZero() {
		match["office_id"] = officeID
	}

	cursor, err := e.db.Collection("orders").Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$group", Value: bson.M{
			"_id": "$beautician_id",
			"total_revenue": bson.M{"$sum": bson.M{"$ifNull": bson.A{
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

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Revenue == rows[j].Revenue {
			return rows[i].OrderCount > rows[j].OrderCount
		}
		return rows[i].Revenue > rows[j].Revenue
	})

	prizes, err := e.getBeauticianLeaderboardPrizes(ctx, officeID)
	if err != nil {
		return nil, err
	}
	for index, row := range rows {
		bonus := 0.0
		if index < len(prizes) {
			bonus = prizes[index]
		}
		bonusByBeautician[row.ID] = beauticianLeaderboardBonus{
			Rank:  index + 1,
			Bonus: bonus,
		}
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
