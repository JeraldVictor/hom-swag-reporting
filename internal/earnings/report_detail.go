package earnings

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/payables"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ReportDetailStore interface {
	LoadReportDetail(context.Context, primitive.ObjectID, primitive.ObjectID, string, string, string) (ReportDetail, error)
}

type ReportDetail struct {
	Orders      []ReportOrder      `json:"orders"`
	Trips       []ReportTrip       `json:"trips"`
	Payouts     []ReportPayout     `json:"payouts"`
	Adjustments []ReportAdjustment `json:"adjustments"`
}

type ReportOrder struct {
	ID                      primitive.ObjectID     `bson:"_id" json:"_id"`
	OrderNumber             string                 `bson:"order_number" json:"order_number"`
	ServiceDate             string                 `bson:"service_date" json:"service_date"`
	BookingInfo             map[string]interface{} `bson:"booking_info,omitempty" json:"booking_info,omitempty"`
	Customer                map[string]interface{} `bson:"customer,omitempty" json:"customer,omitempty"`
	OrderCost               float64                `bson:"order_cost" json:"order_cost"`
	Total                   float64                `bson:"total" json:"total"`
	Subtotal                float64                `bson:"subtotal" json:"subtotal"`
	DiscountTotal           float64                `bson:"discount_total" json:"discount_total"`
	MembershipDiscountTotal float64                `bson:"membership_discount_total" json:"membership_discount_total"`
	OneTimeDiscountAmount   float64                `bson:"one_time_discount_amount" json:"one_time_discount_amount"`
	CommissionDetails       map[string]interface{} `bson:"commission_details" json:"commission_details"`
	CommissionSnapshot      map[string]interface{} `bson:"commission_snapshot,omitempty" json:"commission_snapshot,omitempty"`
	Tip                     float64                `bson:"tip" json:"tip"`
}

type ReportTrip struct {
	ID                     primitive.ObjectID     `bson:"_id" json:"_id"`
	TripNumber             string                 `bson:"trip_number" json:"trip_number"`
	ServiceDate            string                 `bson:"service_date" json:"service_date"`
	IsTwoWay               bool                   `bson:"is_two_way" json:"is_two_way"`
	IsCommissionApplicable bool                   `bson:"is_commission_applicable" json:"is_commission_applicable"`
	PayableDistanceKM      float64                `bson:"payable_distance_km" json:"payable_distance_km"`
	CommissionPayable      float64                `bson:"commission_payable" json:"commission_payable"`
	PetrolPayable          float64                `bson:"petrol_payable" json:"petrol_payable"`
	PayableSnapshot        map[string]interface{} `bson:"payable_snapshot,omitempty" json:"payable_snapshot,omitempty"`
	FareCalculation        map[string]interface{} `bson:"fare_calculation,omitempty" json:"fare_calculation,omitempty"`
}

type ReportPayout struct {
	ID              primitive.ObjectID `json:"_id"`
	PayoutDate      time.Time          `json:"payout_date"`
	PayoutType      SettlementBucket   `json:"payout_type"`
	PeriodStart     string             `json:"period_start"`
	PeriodEnd       string             `json:"period_end"`
	Amount          float64            `json:"amount"`
	PaymentMethod   string             `json:"payment_method"`
	ReferenceNumber string             `json:"reference_number"`
	Remarks         string             `json:"remarks,omitempty"`
}

type ReportAdjustment struct {
	ID         primitive.ObjectID  `json:"_id"`
	PayoutType SettlementBucket    `json:"payout_type"`
	Date       string              `json:"date"`
	Amount     float64             `json:"amount"`
	Reason     string              `json:"reason"`
	OrderID    *primitive.ObjectID `json:"order_id,omitempty"`
}

func (a *API) getReportDetail(w http.ResponseWriter, r *http.Request, officeID string) {
	workerID := strings.TrimSpace(r.URL.Query().Get("worker_id"))
	if err := validateObjectID("worker_id", workerID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	role := strings.TrimSpace(r.URL.Query().Get("role"))
	if role != "beautician" && role != "rider" {
		writeError(w, http.StatusBadRequest, errors.New("role must be beautician or rider"))
		return
	}
	startDate, endDate := r.URL.Query().Get("start_date"), r.URL.Query().Get("end_date")
	if err := validateDateRange(startDate, endDate); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	officeObjectID, _ := primitive.ObjectIDFromHex(officeID)
	workerObjectID, _ := primitive.ObjectIDFromHex(workerID)
	belongs, err := a.repo.WorkerBelongsToOffice(r.Context(), role, workerObjectID, officeObjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !belongs {
		writeError(w, http.StatusUnprocessableEntity, errors.New("worker does not belong to the selected office"))
		return
	}
	detailStore, ok := a.repo.(ReportDetailStore)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("report detail store is unavailable"))
		return
	}
	detail, err := detailStore.LoadReportDetail(r.Context(), officeObjectID, workerObjectID, role, startDate, endDate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func emptyReportDetail() ReportDetail {
	return ReportDetail{
		Orders: make([]ReportOrder, 0), Trips: make([]ReportTrip, 0),
		Payouts: make([]ReportPayout, 0), Adjustments: make([]ReportAdjustment, 0),
	}
}

func (r *Repository) LoadReportDetail(ctx context.Context, officeID, workerID primitive.ObjectID, role, startDate, endDate string) (ReportDetail, error) {
	detail := emptyReportDetail()
	var err error
	if role == "rider" {
		detail.Trips, err = r.loadReportTrips(ctx, officeID, workerID, startDate, endDate)
	} else {
		detail.Orders, err = r.loadReportOrders(ctx, officeID, workerID, startDate, endDate)
	}
	if err != nil {
		return detail, err
	}
	if detail.Payouts, err = r.loadReportPayouts(ctx, officeID, workerID, startDate, endDate); err != nil {
		return detail, err
	}
	if detail.Adjustments, err = r.loadReportAdjustments(ctx, officeID, workerID, startDate, endDate); err != nil {
		return detail, err
	}
	return detail, nil
}

func (r *Repository) loadReportTrips(ctx context.Context, officeID, workerID primitive.ObjectID, startDate, endDate string) ([]ReportTrip, error) {
	match := payables.TripBaseMatch(startDate, endDate)
	match["office_id"] = officeID
	distance := payables.PayableDistanceExpr()
	commission := bson.M{"$cond": bson.A{
		"$is_commission_applicable",
		bson.M{"$cond": bson.A{
			bson.M{"$gt": bson.A{bson.M{"$ifNull": bson.A{"$commission_amount", 0}}, 0}},
			"$commission_amount",
			bson.M{"$round": bson.A{bson.M{"$multiply": bson.A{distance, bson.M{"$ifNull": bson.A{"$payable_snapshot.rider_commission_rate_per_km", 1}}}}, 2}},
		}},
		0,
	}}
	petrolCost := bson.M{"$ifNull": bson.A{"$payable_snapshot.petrol_cost_per_liter", "$fare_calculation.petrol_cost_per_liter"}}
	mileage := bson.M{"$ifNull": bson.A{"$payable_snapshot.standard_mileage_per_liter", "$fare_calculation.standard_mileage_per_liter"}}
	petrol := bson.M{"$cond": bson.A{
		bson.M{"$and": bson.A{bson.M{"$gt": bson.A{petrolCost, 0}}, bson.M{"$gt": bson.A{mileage, 0}}}},
		bson.M{"$round": bson.A{bson.M{"$multiply": bson.A{bson.M{"$divide": bson.A{distance, mileage}}, petrolCost}}, 2}},
		bson.M{"$ifNull": bson.A{"$fare_calculation.calculated_fare", 0}},
	}}
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$addFields", Value: bson.M{"allowance_worker_id": payables.AllowanceWorkerIDExpr()}}},
		{{Key: "$match", Value: bson.M{"allowance_worker_id": workerID}}},
		{{Key: "$project", Value: bson.M{
			"_id": 1, "trip_number": 1, "service_date": "$date", "is_two_way": 1, "is_commission_applicable": 1,
			"payable_distance_km": payables.PaidSnapshotOrCanonicalExpr("payable_distance_km", distance),
			"commission_payable":  payables.PaidSnapshotOrCanonicalExpr("commission_payable", commission),
			"petrol_payable":      payables.PaidSnapshotOrCanonicalExpr("petrol_payable", petrol),
			"payable_snapshot":    1, "fare_calculation": 1,
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "service_date", Value: -1}, {Key: "trip_number", Value: 1}}}},
		{{Key: "$limit", Value: 10000}},
	}
	cursor, err := r.db.Collection("trips").Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	rows := make([]ReportTrip, 0)
	return rows, cursor.All(ctx, &rows)
}

func (r *Repository) loadReportOrders(ctx context.Context, officeID, workerID primitive.ObjectID, startDate, endDate string) ([]ReportOrder, error) {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"office_id": officeID, "beautician_id": workerID, "status": "completed",
			"is_deleted": bson.M{"$ne": true}, "booking_info.date": bson.M{"$gte": startDate, "$lte": endDate},
		}}},
		{{Key: "$project", Value: bson.M{
			"_id": 1, "order_number": 1, "service_date": "$booking_info.date", "booking_info": 1, "customer": 1,
			"order_cost": bson.M{"$ifNull": bson.A{"$commission_snapshot.order_cost", bson.M{"$ifNull": bson.A{"$subtotal", "$total"}}}},
			"total":      bson.M{"$ifNull": bson.A{"$total", 0}}, "subtotal": bson.M{"$ifNull": bson.A{"$subtotal", 0}},
			"discount_total":            bson.M{"$ifNull": bson.A{"$discount_total", 0}},
			"membership_discount_total": bson.M{"$ifNull": bson.A{"$membership_discount_total", 0}},
			"one_time_discount_amount":  bson.M{"$ifNull": bson.A{"$one_time_discount_amount", 0}},
			"commission_details": bson.M{
				"special_commission":       bson.M{"$ifNull": bson.A{"$commission_snapshot.special_commission", 0}},
				"general_commission":       bson.M{"$ifNull": bson.A{"$commission_snapshot.general_commission", 0}},
				"upgrade_addon_commission": bson.M{"$ifNull": bson.A{"$commission_snapshot.upgrade_addon_commission", 0}},
				"total_commission":         bson.M{"$ifNull": bson.A{"$commission_snapshot.total_commission", 0}},
			},
			"commission_snapshot": 1, "tip": bson.M{"$ifNull": bson.A{"$tip", 0}},
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "service_date", Value: -1}, {Key: "order_number", Value: 1}}}},
		{{Key: "$limit", Value: 10000}},
	}
	cursor, err := r.db.Collection("orders").Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	rows := make([]ReportOrder, 0)
	return rows, cursor.All(ctx, &rows)
}

func reportDateBounds(startDate, endDate string) (time.Time, time.Time, error) {
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return start, end.Add(24*time.Hour - time.Nanosecond), nil
}

func (r *Repository) loadReportPayouts(ctx context.Context, officeID, workerID primitive.ObjectID, startDate, endDate string) ([]ReportPayout, error) {
	rows := make([]ReportPayout, 0)
	current, _, err := r.ListSettlements(ctx, SettlementFilter{OfficeID: officeID.Hex(), WorkerID: workerID.Hex(), StartDate: startDate, EndDate: endDate, Page: 1, Limit: 10000})
	if err != nil {
		return nil, err
	}
	for _, settlement := range current {
		rows = append(rows, ReportPayout{
			ID: settlement.ID, PayoutDate: settlement.CreatedAt, PayoutType: settlement.Bucket,
			PeriodStart: settlement.StartDate, PeriodEnd: settlement.EndDate, Amount: float64(settlement.AmountPaise) / 100,
			PaymentMethod: settlement.PaymentMethod, ReferenceNumber: settlement.Reference, Remarks: settlement.Remarks,
		})
	}
	start, end, err := reportDateBounds(startDate, endDate)
	if err != nil {
		return nil, err
	}
	type legacyPayout struct {
		ID              primitive.ObjectID `bson:"_id"`
		PayoutType      SettlementBucket   `bson:"payout_type"`
		PeriodStart     time.Time          `bson:"period_start"`
		PeriodEnd       time.Time          `bson:"period_end"`
		Amount          float64            `bson:"amount"`
		PayoutDate      time.Time          `bson:"payout_date"`
		PaymentMethod   string             `bson:"payment_method"`
		ReferenceNumber string             `bson:"reference_number"`
		Remarks         string             `bson:"remarks"`
	}
	cursor, err := r.db.Collection("payouts").Find(ctx, bson.M{
		"office_id": officeID, "staff_id": workerID, "is_deleted": bson.M{"$ne": true},
		"$or": bson.A{
			bson.M{"payout_date": bson.M{"$gte": start, "$lte": end}},
			bson.M{"period_start": bson.M{"$lte": end}, "period_end": bson.M{"$gte": start}},
		},
	}, options.Find().SetSort(bson.D{{Key: "payout_date", Value: -1}}).SetLimit(10000))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	legacy := make([]legacyPayout, 0)
	if err := cursor.All(ctx, &legacy); err != nil {
		return nil, err
	}
	for _, payout := range legacy {
		rows = append(rows, ReportPayout{
			ID: payout.ID, PayoutDate: payout.PayoutDate, PayoutType: payout.PayoutType,
			PeriodStart: payout.PeriodStart.Format("2006-01-02"), PeriodEnd: payout.PeriodEnd.Format("2006-01-02"),
			Amount: payout.Amount, PaymentMethod: payout.PaymentMethod,
			ReferenceNumber: payout.ReferenceNumber, Remarks: payout.Remarks,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].PayoutDate.After(rows[j].PayoutDate) })
	return rows, nil
}

func (r *Repository) loadReportAdjustments(ctx context.Context, officeID, workerID primitive.ObjectID, startDate, endDate string) ([]ReportAdjustment, error) {
	rows := make([]ReportAdjustment, 0)
	entries, _, err := r.ListEntries(ctx, LedgerFilter{
		OfficeID: officeID.Hex(), WorkerID: workerID.Hex(), StartDate: startDate, EndDate: endDate, Page: 1, Limit: 10000,
	})
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Component != ComponentCommissionAdjustment && entry.Component != ComponentPetrolAdjustment && entry.Component != ComponentComplaintDeduction {
			continue
		}
		rows = append(rows, ReportAdjustment{
			ID: entry.ID, PayoutType: entry.SettlementBucket, Date: entry.ServiceDateKey,
			Amount: float64(entry.AmountPaise) / 100, Reason: firstNonEmpty(entry.Reason, string(entry.Component)),
		})
	}
	start, end, err := reportDateBounds(startDate, endDate)
	if err != nil {
		return nil, err
	}
	type legacyAdjustment struct {
		ID         primitive.ObjectID  `bson:"_id"`
		PayoutType SettlementBucket    `bson:"payout_type"`
		Date       time.Time           `bson:"date"`
		Amount     float64             `bson:"amount"`
		Reason     string              `bson:"reason"`
		OrderID    *primitive.ObjectID `bson:"order_id"`
	}
	cursor, err := r.db.Collection("commissionadjustments").Find(ctx, bson.M{
		"office_id": officeID, "staff_id": workerID, "is_deleted": bson.M{"$ne": true},
		"date": bson.M{"$gte": start, "$lte": end},
	}, options.Find().SetSort(bson.D{{Key: "date", Value: -1}}).SetLimit(10000))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	legacy := make([]legacyAdjustment, 0)
	if err := cursor.All(ctx, &legacy); err != nil {
		return nil, err
	}
	for _, adjustment := range legacy {
		rows = append(rows, ReportAdjustment{
			ID: adjustment.ID, PayoutType: adjustment.PayoutType, Date: adjustment.Date.Format("2006-01-02"),
			Amount: adjustment.Amount, Reason: adjustment.Reason, OrderID: adjustment.OrderID,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Date > rows[j].Date })
	return rows, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "Adjustment"
}
