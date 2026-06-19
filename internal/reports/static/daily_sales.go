package static

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type DailySalesExecutor struct {
	db *mongo.Database
}

func NewDailySalesExecutor(db *mongo.Database) *DailySalesExecutor {
	return &DailySalesExecutor{db: db}
}

func (e *DailySalesExecutor) Key() string {
	return "daily_sales"
}

func (e *DailySalesExecutor) Version() int {
	return 2
}

func (e *DailySalesExecutor) Validate(ctx context.Context, req reports.Request) error {
	if _, ok := req.Parameters["start_date"]; !ok {
		return fmt.Errorf("start_date is required")
	}
	if _, ok := req.Parameters["end_date"]; !ok {
		return fmt.Errorf("end_date is required")
	}
	return nil
}

func (e *DailySalesExecutor) Run(ctx context.Context, req reports.Request, sink reports.RowSink) error {
	startDateStr := req.Parameters["start_date"].(string)
	endDateStr := req.Parameters["end_date"].(string)

	startDate, err := parseDailySalesDate(startDateStr, false)
	if err != nil {
		return fmt.Errorf("invalid start_date: %w", err)
	}
	endDate, err := parseDailySalesDate(endDateStr, true)
	if err != nil {
		return fmt.Errorf("invalid end_date: %w", err)
	}
	matchStart := startDate.Format("2006-01-02")
	matchEnd := endDate.Format("2006-01-02")

	match := bson.M{
		"is_deleted": false,
		"$or": bson.A{
			bson.M{"booking_info.date": bson.M{"$gte": matchStart, "$lte": matchEnd}},
		},
	}
	if officeID, ok := dailySalesOfficeID(req.Parameters); ok {
		match["office_id"] = officeID
	}
	if statuses := dailySalesStatuses(req.Parameters); len(statuses) > 0 {
		match["status"] = bson.M{"$in": statuses}
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$lookup", Value: bson.M{
			"from":         "beauticians",
			"localField":   "beautician_id",
			"foreignField": "_id",
			"as":           "beautician",
		}}},
		{{Key: "$unwind", Value: bson.M{"path": "$beautician", "preserveNullAndEmptyArrays": true}}},
		{{Key: "$project", Value: bson.M{
			"customer_name":          "$customer.full_name",
			"invoice_number":         "$invoice_number",
			"invoice_date":           bson.M{"$ifNull": bson.A{"$status_updated_at", "$updated_at"}},
			"order_number":           "$order_number",
			"order_date":             "$booking_info.date",
			"beautician_unique_code": "$beautician.emp_code",
			"beautician_pan":         "$beautician.pan_number",
			"beautician_name":        "$beautician.name",
			"total_services_cost":    bson.M{"$ifNull": bson.A{"$subtotal", 0}},
			"convenience_fees":       bson.M{"$ifNull": bson.A{"$convenience_fees", 0}},
			"hygiene_fees":           bson.M{"$ifNull": bson.A{"$hygiene_fees", 0}},
			"surge_charges":          bson.M{"$ifNull": bson.A{"$booking_info.surge_amount", 0}},
			"membership_charges":     bson.M{"$ifNull": bson.A{"$membership_charge", 0}},
			"cancellation_charge": bson.M{"$ifNull": bson.A{
				"$cancellation_charge",
				bson.M{"$ifNull": bson.A{"$payment.cancellation_charge", 0}},
			}},
			"total":                  bson.M{"$ifNull": bson.A{"$total", 0}},
			"status":                 "$status",
			"membership_discount":    bson.M{"$ifNull": bson.A{"$membership_discount_total", 0}},
			"special_discount":       bson.M{"$ifNull": bson.A{"$one_time_discount_amount", 0}},
			"discount_total":         bson.M{"$ifNull": bson.A{"$discount_total", 0}},
			"tips":                   bson.M{"$ifNull": bson.A{"$payment.tip", bson.M{"$ifNull": bson.A{"$tip", 0}}}},
			"online":                 bson.M{"$add": bson.A{paymentUpiExpr(), paymentOnlineExpr()}},
			"cash":                   paymentCashExpr(),
			"bank_transfer":          paymentBankTransferExpr(),
			"total_received":         paymentReceivedExpr(),
			"payment_gateway_tips":   bson.M{"$literal": 0},
			"payment_gateway_others": bson.M{"$literal": 0},
		}}},
		{{Key: "$sort", Value: bson.M{"order_date": 1, "invoice_date": 1}}},
	}

	if req.Limit > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: req.Limit}})
	}

	cursor, err := e.db.Collection("orders").Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	header := []interface{}{
		"Customer Name",
		"Invoice Number",
		"Invoice Date",
		"Order Num#",
		"Order Date",
		"Unique Code of Beautician",
		"PAN of Beautician",
		"Beautician Name",
		"SAC Code",
		"Total Services cost",
		"Convenience Fees",
		"Hygiene Fees",
		"Surge Charges",
		"Membership Charges",
		"Cancellation Charges",
		"Total Value",
		"Membership discount",
		"Special discount",
		"Coupon Discount",
		"Total Discount Provided",
		"Net Receivable including GST",
		"Tips",
		"Total receivable including tips",
		"Online",
		"Cash",
		"Bank Transfer",
		"Total Received",
		"Payment Gateway Charges-Tips",
		"Payment Gateway Charges-Others",
		"Net Receivable Bank",
		"Net tips Payable",
	}
	if err := sink.WriteRow(header); err != nil {
		return err
	}

	totals := make([]float64, len(header))
	for cursor.Next(ctx) {
		var row dailySalesRow
		if err := cursor.Decode(&row); err != nil {
			return err
		}
		values := row.values()
		for index, value := range values {
			if index >= 9 {
				if amount, ok := value.(float64); ok {
					totals[index] += amount
				}
			}
		}
		if err := sink.WriteRow(values); err != nil {
			return err
		}
	}
	if err := cursor.Err(); err != nil {
		return err
	}

	totalRow := make([]interface{}, len(header))
	totalRow[0] = "Total"
	for index := 1; index < len(totalRow); index++ {
		if index >= 9 {
			totalRow[index] = money2(totals[index])
		} else {
			totalRow[index] = ""
		}
	}
	return sink.WriteRow(totalRow)
}

type dailySalesRow struct {
	CustomerName         string    `bson:"customer_name"`
	InvoiceNumber        any       `bson:"invoice_number"`
	InvoiceDate          time.Time `bson:"invoice_date"`
	OrderNumber          string    `bson:"order_number"`
	OrderDate            any       `bson:"order_date"`
	BeauticianUniqueCode string    `bson:"beautician_unique_code"`
	BeauticianPAN        string    `bson:"beautician_pan"`
	BeauticianName       string    `bson:"beautician_name"`
	TotalServicesCost    float64   `bson:"total_services_cost"`
	ConvenienceFees      float64   `bson:"convenience_fees"`
	HygieneFees          float64   `bson:"hygiene_fees"`
	SurgeCharges         float64   `bson:"surge_charges"`
	MembershipCharges    float64   `bson:"membership_charges"`
	CancellationCharge   float64   `bson:"cancellation_charge"`
	Total                float64   `bson:"total"`
	Status               string    `bson:"status"`
	MembershipDiscount   float64   `bson:"membership_discount"`
	SpecialDiscount      float64   `bson:"special_discount"`
	DiscountTotal        float64   `bson:"discount_total"`
	Tips                 float64   `bson:"tips"`
	Online               float64   `bson:"online"`
	Cash                 float64   `bson:"cash"`
	BankTransfer         float64   `bson:"bank_transfer"`
	TotalReceived        float64   `bson:"total_received"`
	PaymentGatewayTips   float64   `bson:"payment_gateway_tips"`
	PaymentGatewayOthers float64   `bson:"payment_gateway_others"`
}

func (r dailySalesRow) values() []interface{} {
	cancellationCharges := 0.0
	if isCancelledDailySalesStatus(r.Status) {
		cancellationCharges = money2(r.CancellationCharge)
	}
	totalValue := money2(r.TotalServicesCost + r.ConvenienceFees + r.HygieneFees + r.SurgeCharges + r.MembershipCharges + cancellationCharges)
	couponDiscount := money2(math.Max(r.DiscountTotal-r.MembershipDiscount-r.SpecialDiscount, 0))
	totalDiscount := money2(r.MembershipDiscount + r.SpecialDiscount + couponDiscount)
	netReceivable := money2(math.Max(r.Total-r.Tips, 0))
	if isCancelledDailySalesStatus(r.Status) {
		netReceivable = cancellationCharges
	}
	totalReceivable := money2(netReceivable + r.Tips)
	netReceivableBank := money2(r.Online + r.BankTransfer - r.PaymentGatewayOthers)
	netTipsPayable := money2(r.Tips - r.PaymentGatewayTips)

	return []interface{}{
		r.CustomerName,
		valueToString(r.InvoiceNumber),
		formatReportDate(r.InvoiceDate),
		r.OrderNumber,
		formatAnyDate(r.OrderDate),
		r.BeauticianUniqueCode,
		r.BeauticianPAN,
		r.BeauticianName,
		"",
		money2(r.TotalServicesCost),
		money2(r.ConvenienceFees),
		money2(r.HygieneFees),
		money2(r.SurgeCharges),
		money2(r.MembershipCharges),
		cancellationCharges,
		totalValue,
		money2(r.MembershipDiscount),
		money2(r.SpecialDiscount),
		couponDiscount,
		totalDiscount,
		netReceivable,
		money2(r.Tips),
		totalReceivable,
		money2(r.Online),
		money2(r.Cash),
		money2(r.BankTransfer),
		money2(r.TotalReceived),
		money2(r.PaymentGatewayTips),
		money2(r.PaymentGatewayOthers),
		netReceivableBank,
		netTipsPayable,
	}
}

func dailySalesOfficeID(parameters map[string]interface{}) (primitive.ObjectID, bool) {
	officeIDStr, ok := parameters["office_id"].(string)
	if !ok || officeIDStr == "" {
		return primitive.NilObjectID, false
	}
	officeID, err := primitive.ObjectIDFromHex(officeIDStr)
	if err != nil {
		return primitive.NilObjectID, false
	}
	return officeID, true
}

func dailySalesStatuses(parameters map[string]interface{}) []interface{} {
	raw, ok := parameters["order_status"]
	if !ok {
		return []interface{}{"completed"}
	}
	switch value := raw.(type) {
	case string:
		if value == "" || value == "all" {
			return nil
		}
		return []interface{}{value}
	case []interface{}:
		statuses := make([]interface{}, 0, len(value))
		for _, item := range value {
			status, ok := item.(string)
			if ok && status != "" && status != "all" {
				statuses = append(statuses, status)
			}
		}
		return statuses
	default:
		return []interface{}{"completed"}
	}
}

func isCancelledDailySalesStatus(status string) bool {
	switch status {
	case "cancelled", "cancelled_and_refunded", "arrived_and_cancelled":
		return true
	default:
		return false
	}
}

func money2(value float64) float64 {
	return math.Round(value*100) / 100
}

func valueToString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%v", value)
}

func formatReportDate(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format("2006-01-02")
}

func formatAnyDate(value any) string {
	switch date := value.(type) {
	case time.Time:
		return formatReportDate(date)
	case string:
		return date
	default:
		return valueToString(date)
	}
}

func parseDailySalesDate(value string, endOfDay bool) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}

	parsed, err := time.ParseInLocation("2006-01-02", value, time.FixedZone("IST", 5*60*60+30*60))
	if err != nil {
		return time.Time{}, err
	}
	if endOfDay {
		return parsed.Add(24*time.Hour - time.Nanosecond), nil
	}
	return parsed, nil
}
