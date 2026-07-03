package static

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func TestGoldenDailySalesFixturesCoverOrderMathEdgeCases(t *testing.T) {
	fixtures := []struct {
		name     string
		row      dailySalesRow
		expected map[int]float64
	}{
		{
			name: "completed modern order with discounts tips and split collections",
			row: dailySalesRow{
				Status:               "completed",
				InvoiceDate:          time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC),
				TotalServicesCost:    1000,
				ConvenienceFees:      50,
				HygieneFees:          20,
				SurgeCharges:         30,
				MembershipCharges:    100,
				Total:                1100,
				MembershipDiscount:   100,
				SpecialDiscount:      40,
				DiscountTotal:        180,
				Tips:                 75,
				Online:               700,
				Cash:                 200,
				BankTransfer:         200,
				TotalReceived:        1100,
				PaymentGatewayTips:   5,
				PaymentGatewayOthers: 15,
			},
			expected: map[int]float64{
				15: 1200,
				18: 40,
				19: 180,
				20: 1025,
				22: 1100,
				23: 700,
				24: 200,
				25: 200,
				26: 1100,
				29: 885,
				30: 70,
			},
		},
		{
			name: "legacy completed order with missing optional money fields",
			row: dailySalesRow{
				Status:            "completed",
				InvoiceDate:       time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC),
				TotalServicesCost: 500,
				Total:             500,
				Cash:              500,
				TotalReceived:     500,
			},
			expected: map[int]float64{
				9:  500,
				15: 500,
				18: 0,
				19: 0,
				20: 500,
				22: 500,
				24: 500,
				26: 500,
				29: 0,
				30: 0,
			},
		},
		{
			name: "arrived and cancelled order counts cancellation charge as receivable",
			row: dailySalesRow{
				Status:             "arrived_and_cancelled",
				InvoiceDate:        time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC),
				TotalServicesCost:  900,
				CancellationCharge: 150,
				Total:              900,
				Tips:               25,
				Online:             150,
				TotalReceived:      150,
			},
			expected: map[int]float64{
				14: 150,
				15: 1050,
				20: 150,
				22: 175,
				23: 150,
				26: 150,
			},
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			values := fixture.row.values()
			for index, expected := range fixture.expected {
				assertFloatCell(t, values, index, expected)
			}
		})
	}
}

func TestGoldenPaymentFixturesCoverHistoryAndLegacyBuckets(t *testing.T) {
	fixtures := []struct {
		name         string
		document     map[string]any
		cash         float64
		upi          float64
		online       float64
		bankTransfer float64
		total        float64
		refund       float64
	}{
		{
			name: "history labels without methods still bucket into payments",
			document: map[string]any{
				"payment": map[string]any{
					"history": []any{
						map[string]any{"label": "UPI payment", "amount": 300},
						map[string]any{"label": "Bank transfer", "amount": 200},
						map[string]any{"label": "Card payment", "amount": 150},
						map[string]any{"label": "Cash collected", "amount": 100},
						map[string]any{"label": "Wallet payment", "amount": 50},
					},
				},
			},
			cash:         100,
			upi:          300,
			online:       200,
			bankTransfer: 200,
			total:        800,
		},
		{
			name: "history excludes tips cancellation labels and refunds from received totals",
			document: map[string]any{
				"payment": map[string]any{
					"history": []any{
						map[string]any{"label": "UPI payment", "method": "upi", "amount": 500},
						map[string]any{"label": "Tip", "method": "upi", "amount": 60},
						map[string]any{"label": "Cancellation Fee", "method": "cash", "amount": 125},
						map[string]any{"label": "Cancellation-charge", "method": "cash", "amount": 75},
						map[string]any{"label": "Refund", "method": "upi", "amount": 40},
						map[string]any{"label": "Partial refund", "method": "upi", "amount": -35},
					},
				},
			},
			upi:    500,
			total:  500,
			refund: 75,
		},
		{
			name: "legacy split payment places paid remainder in declared method",
			document: map[string]any{
				"payment": map[string]any{
					"actual_paid_amount": 1200,
					"cod_amount":         200,
					"upi_amount":         300,
					"method":             "bank-transfer",
				},
			},
			cash:         200,
			upi:          300,
			bankTransfer: 700,
			total:        1200,
		},
		{
			name: "legacy refund fields include root cancellation fallback",
			document: map[string]any{
				"payment": map[string]any{
					"refund_amount":         100,
					"partial_refund_amount": 25,
				},
				"cancellation_refund_amount": 30,
			},
			refund: 155,
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			assertEvaluatedMoney(t, "cash", paymentCashExpr(), fixture.document, fixture.cash)
			assertEvaluatedMoney(t, "upi", paymentUpiExpr(), fixture.document, fixture.upi)
			assertEvaluatedMoney(t, "online", paymentOnlineExpr(), fixture.document, fixture.online)
			assertEvaluatedMoney(t, "bank_transfer", paymentBankTransferExpr(), fixture.document, fixture.bankTransfer)
			assertEvaluatedMoney(t, "total_received", paymentReceivedExpr(), fixture.document, fixture.total)
			assertEvaluatedMoney(t, "refund", paymentRefundExpr(), fixture.document, fixture.refund)
		})
	}
}

func TestGoldenBeauticianCommissionFixturesOnlyCompletedOrdersContributeToCommission(t *testing.T) {
	completed := map[string]any{
		"status":     "completed",
		"order_cost": 1500,
		"commission_details": map[string]any{
			"special_commission":       75,
			"general_commission":       120,
			"upgrade_addon_commission": 45,
		},
	}
	cancelledAndRefunded := map[string]any{
		"status":     "cancelled_and_refunded",
		"order_cost": 1500,
		"commission_details": map[string]any{
			"special_commission":       75,
			"general_commission":       120,
			"upgrade_addon_commission": 45,
		},
		"payment": map[string]any{
			"partial_refund_amount": 300,
		},
	}

	assertEvaluatedMoney(t, "completed special commission", completedOnlyExpr("$commission_details.special_commission"), completed, 75)
	assertEvaluatedMoney(t, "completed revenue", completedOnlyExpr("$order_cost"), completed, 1500)
	assertEvaluatedMoney(t, "completed order count", completedOnlyExpr(1), completed, 1)

	assertEvaluatedMoney(t, "cancelled special commission", completedOnlyExpr("$commission_details.special_commission"), cancelledAndRefunded, 0)
	assertEvaluatedMoney(t, "cancelled revenue", completedOnlyExpr("$order_cost"), cancelledAndRefunded, 0)
	assertEvaluatedMoney(t, "cancelled order count", completedOnlyExpr(1), cancelledAndRefunded, 0)
	assertEvaluatedMoney(t, "cancelled refund still reported", paymentRefundExpr(), cancelledAndRefunded, 300)
}

func TestGoldenTripFixturesCoverPetrolAndCommissionPayableInputs(t *testing.T) {
	fixtures := []struct {
		name          string
		document      map[string]any
		distance      float64
		petrolPayable float64
	}{
		{
			name: "fare calculation wins over auto distance and office fallback",
			document: map[string]any{
				"fare_calculation": map[string]any{
					"trip_distance_km": 18,
					"calculated_fare":  90,
				},
				"auto_distance_km": 10,
				"extra_km":         4,
				"is_two_way":       true,
				"office": map[string]any{
					"petrol_cost_per_liter":      110,
					"standard_mileage_per_liter": 55,
				},
			},
			distance:      18,
			petrolPayable: 90,
		},
		{
			name: "self drive style trip uses auto distance two way extra km and office fare",
			document: map[string]any{
				"auto_distance_km": 12,
				"extra_km":         3,
				"is_two_way":       true,
				"office": map[string]any{
					"petrol_cost_per_liter":      120,
					"standard_mileage_per_liter": 40,
				},
			},
			distance:      27,
			petrolPayable: 81,
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			distanceExpr := tripPayableDistanceExpr()
			assertEvaluatedMoney(t, "payable_distance_km", distanceExpr, fixture.document, fixture.distance)
			assertEvaluatedMoney(t, "petrol_payable", tripPetrolPayableExpr(distanceExpr), fixture.document, fixture.petrolPayable)
		})
	}
}

func TestGoldenInclusionRulesKeepLegacyRecordsAndExcludeExplicitDeletes(t *testing.T) {
	orderMatch := orderReportBaseMatch(
		time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 31, 23, 59, 59, 0, time.UTC),
		"2026-07-01",
		"2026-07-31",
	)
	assertNotExplicitlyDeletedRule(t, orderMatch["is_deleted"])

	tripMatch := payableTripBaseMatch("2026-07-01", "2026-07-31")
	assertNotExplicitlyDeletedRule(t, tripMatch["is_deleted"])
}

func assertNotExplicitlyDeletedRule(t *testing.T, rule any) {
	t.Helper()
	match, ok := rule.(bson.M)
	if !ok {
		t.Fatalf("deleted rule should be bson.M, got %#v", rule)
	}
	if match["$ne"] != true {
		t.Fatalf("deleted rule = %#v, want only is_deleted true records excluded", rule)
	}
}
