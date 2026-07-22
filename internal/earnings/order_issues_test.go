package earnings

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func orderIssueFixture(total float64, tip float64, history ...orderIssuePaymentHistory) orderIssueSource {
	return orderIssueSource{
		ID: primitive.NewObjectID(), OfficeID: primitive.NewObjectID(), OrderNumber: "20260721-0001",
		Status: "completed", Booking: orderIssueBooking{Date: "2026-07-21"}, Subtotal: 1000,
		Total: &total, Payment: orderIssuePayment{Method: "online", Tip: tip, History: history},
	}
}

func TestDetectOrderIssuesSupportsBothTipModels(t *testing.T) {
	now := time.Now().UTC()
	for _, fixture := range []struct {
		name   string
		source orderIssueSource
	}{
		{
			name: "tip included in order total",
			source: orderIssueFixture(1100, 100,
				orderIssuePaymentHistory{Label: "Payment", Method: "online", Amount: 1100}),
		},
		{
			name: "separate tip is already included in main payment",
			source: orderIssueFixture(1000, 100,
				orderIssuePaymentHistory{Label: "Payment", Method: "online", Amount: 1100},
				orderIssuePaymentHistory{Label: "Tip", Method: "cash", Amount: 100}),
		},
		{
			name: "tip is collected as a separate payment",
			source: orderIssueFixture(1000, 100,
				orderIssuePaymentHistory{Label: "Payment", Method: "online", Amount: 1000},
				orderIssuePaymentHistory{Label: "Tip", Method: "cash", Amount: 100}),
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if issues := detectOrderIssues(fixture.source, now); len(issues) != 0 {
				t.Fatalf("issues = %#v, want none", issues)
			}
		})
	}
}

func TestOrderIssueReceivedDoesNotDoubleCountTipBreakdowns(t *testing.T) {
	source := orderIssueFixture(1971, 29,
		orderIssuePaymentHistory{Label: "Online payment", Method: "online", Amount: 2000},
		orderIssuePaymentHistory{Label: "Tip", Method: "online", Amount: 29},
		orderIssuePaymentHistory{Label: "Legacy payment transaction", Method: "tips", Amount: 10},
	)
	source.Subtotal = 1797
	source.ConvenienceFees = 115
	source.HygieneFees = 59
	if got := orderIssueReceivedPaise(source); got != 200000 {
		t.Fatalf("received = %d, want 200000", got)
	}
	if issues := detectOrderIssues(source, time.Now().UTC()); len(issues) != 0 {
		t.Fatalf("issues = %#v, want none", issues)
	}
}

func TestDetectOrderIssuesClassifiesPaymentProblems(t *testing.T) {
	now := time.Now().UTC()
	overpaid := orderIssueFixture(1000, 0, orderIssuePaymentHistory{Label: "Payment", Method: "online", Amount: 1045})
	issues := detectOrderIssues(overpaid, now)
	if len(issues) != 1 || issues[0].IssueType != OrderIssuePaymentMismatch || issues[0].DifferencePaise != 4500 || issues[0].Severity != "high" {
		t.Fatalf("issues = %#v", issues)
	}

	missing := orderIssueFixture(1000, 0)
	missing.Payment.History = []orderIssuePaymentHistory{{Label: "Tip", Amount: 0}}
	issues = detectOrderIssues(missing, now)
	if len(issues) != 1 || issues[0].IssueType != OrderIssueMissingPayment || issues[0].DifferencePaise != -100000 {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestDetectOrderIssuesFlagsInvalidTotalFormula(t *testing.T) {
	source := orderIssueFixture(950, 0, orderIssuePaymentHistory{Label: "Payment", Amount: 950})
	issues := detectOrderIssues(source, time.Now().UTC())
	if len(issues) != 1 || issues[0].IssueType != OrderIssueInvalidTotal || issues[0].ExpectedPaise != 100000 || issues[0].ActualPaise != 95000 {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestOrderIssueReceivedIncludesAuditedSignedCorrection(t *testing.T) {
	source := orderIssueFixture(1000, 0,
		orderIssuePaymentHistory{Label: "Payment", Amount: 1045},
		orderIssuePaymentHistory{Label: "Reconciliation adjustment", Amount: -45},
		orderIssuePaymentHistory{Label: "Refund", Amount: -20},
	)
	if got := orderIssueReceivedPaise(source); got != 100000 {
		t.Fatalf("received = %d, want 100000", got)
	}
}

func TestValidOrderIssueSourceRejectsUnsafeRecords(t *testing.T) {
	total := 1000.0
	source := orderIssueSource{ID: primitive.NewObjectID(), OfficeID: primitive.NewObjectID(), Status: "completed", Booking: orderIssueBooking{Date: "2026-07-21"}, Total: &total}
	if !validOrderIssueSource(source) {
		t.Fatal("expected valid source")
	}
	source.Status = "cancelled"
	if validOrderIssueSource(source) {
		t.Fatal("cancelled source must be rejected")
	}
	source.Status, source.IsDeleted = "completed", true
	if validOrderIssueSource(source) {
		t.Fatal("deleted source must be rejected")
	}
}
