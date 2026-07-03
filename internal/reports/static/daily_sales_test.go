package static

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func TestDailySalesRowValuesCalculatesCustomerFriendlyTotals(t *testing.T) {
	row := dailySalesRow{
		CustomerName:         "Asha",
		InvoiceNumber:        "INV-1",
		InvoiceDate:          time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC),
		OrderNumber:          "ORD-1",
		OrderDate:            "2026-07-03",
		BeauticianUniqueCode: "EMP-1",
		BeauticianName:       "Meera",
		TotalServicesCost:    1000,
		ConvenienceFees:      50,
		HygieneFees:          25,
		SurgeCharges:         75,
		MembershipCharges:    100,
		Total:                1050,
		MembershipDiscount:   100,
		SpecialDiscount:      50,
		DiscountTotal:        200,
		Tips:                 80,
		Online:               600,
		Cash:                 300,
		BankTransfer:         150,
		TotalReceived:        1050,
		PaymentGatewayTips:   5,
		PaymentGatewayOthers: 20,
	}

	values := row.values()

	assertFloatCell(t, values, 15, 1250)
	assertFloatCell(t, values, 18, 50)
	assertFloatCell(t, values, 19, 200)
	assertFloatCell(t, values, 20, 970)
	assertFloatCell(t, values, 22, 1050)
	assertFloatCell(t, values, 29, 730)
	assertFloatCell(t, values, 30, 75)
}

func TestDailySalesRowValuesUsesCancellationChargeForCancelledOrders(t *testing.T) {
	row := dailySalesRow{
		Status:             "cancelled",
		TotalServicesCost:  1000,
		CancellationCharge: 125,
		Total:              1000,
		Tips:               40,
	}

	values := row.values()

	assertFloatCell(t, values, 14, 125)
	assertFloatCell(t, values, 15, 1125)
	assertFloatCell(t, values, 20, 125)
	assertFloatCell(t, values, 22, 165)
}

func TestDailySalesDateClausesIncludeNewAndLegacyOrderDates(t *testing.T) {
	start := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
	end := start.Add(24*time.Hour - time.Nanosecond)

	clauses := dailySalesDateClauses(start, end, "2026-07-03", "2026-07-03")

	if len(clauses) != 4 {
		t.Fatalf("expected 4 date clauses, got %d", len(clauses))
	}
	if _, ok := clauses[0].(bson.M)["service_date"]; !ok {
		t.Fatalf("first clause should match modern service_date: %#v", clauses[0])
	}
	if got := clauses[1].(bson.M)["service_date"]; got == nil {
		t.Fatalf("second clause should guard legacy missing service_date: %#v", clauses[1])
	}
	if got := clauses[2].(bson.M)["service_date"]; got != nil {
		t.Fatalf("third clause should match legacy null service_date: %#v", clauses[2])
	}
	if _, ok := clauses[3].(bson.M)["booking_info.date"]; !ok {
		t.Fatalf("fourth clause should keep booking_info.date fallback: %#v", clauses[3])
	}
}

func assertFloatCell(t *testing.T, values []interface{}, index int, expected float64) {
	t.Helper()
	got, ok := values[index].(float64)
	if !ok {
		t.Fatalf("cell %d should be float64, got %#v", index, values[index])
	}
	if got != expected {
		t.Fatalf("cell %d = %v, want %v", index, got, expected)
	}
}
