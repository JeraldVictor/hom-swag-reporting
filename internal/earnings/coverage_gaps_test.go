package earnings

import (
	"context"
	"math"
	"net/http"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestPermissionWildcardForms(t *testing.T) {
	for _, permission := range []string{"ledger:read", "*:*", "*.*", "ledger:*", "ledger:**", "ledger.*", "ledger.**"} {
		required := "ledger:read"
		if permission == "ledger.*" || permission == "ledger.**" || permission == "*.*" {
			required = "ledger.read"
		}
		if !(Principal{Permissions: []string{permission}}).HasPermission(required) {
			t.Fatalf("permission %q should allow %q", permission, required)
		}
	}
}

func TestOrderIssueEndpointRemainingFailures(t *testing.T) {
	for _, test := range []struct {
		name, method, path, body string
	}{
		{"scan unavailable", http.MethodPost, "/api/earnings/order-issues/scan?office_id=" + testOfficeID, `{}`},
		{"action unavailable", http.MethodPost, "/api/earnings/order-issues/" + primitive.NewObjectID().Hex() + "/actions?office_id=" + testOfficeID, `{"action":"recheck"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performAPIRequest(t, newMockStore(), test.method, test.path, test.body, validTestClaims())
			if response.Code != http.StatusNotImplemented {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
	for _, test := range []struct{ name, body string }{
		{"malformed scan", `{`},
		{"invalid scan range", `{"start_date":"bad","end_date":"2026-07-31"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performAPIRequest(t, newMockOrderIssueStore(), http.MethodPost, "/api/earnings/order-issues/scan?office_id="+testOfficeID, test.body, validTestClaims())
			if response.Code != http.StatusBadRequest {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
	response := performAPIRequest(t, newMockOrderIssueStore(), http.MethodGet, "/api/earnings/order-issues?office_id="+testOfficeID+"&start_date=bad&end_date=2026-07-31", "", validTestClaims())
	if response.Code != http.StatusBadRequest {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestOrderIssuePureFunctionBranches(t *testing.T) {
	now := timeNowUTC()
	missingTotal := orderIssueFixture(1000, 0)
	missingTotal.Total = nil
	issues := detectOrderIssues(missingTotal, now)
	if len(issues) != 1 || issues[0].IssueType != OrderIssueInvalidTotal || issues[0].Severity != "critical" {
		t.Fatalf("issues=%+v", issues)
	}
	invalid := math.NaN()
	missingTotal.Total = &invalid
	if issues := detectOrderIssues(missingTotal, now); len(issues) != 1 || issues[0].IssueType != OrderIssueInvalidTotal {
		t.Fatalf("issues=%+v", issues)
	}
	if orderIssueSeverity(999) != "warning" {
		t.Fatal("small variances should be warnings")
	}

	paid, amountPaid, cod, upi, online, bank := 100.0, 90.0, 30.0, 20.0, 10.0, 5.0
	legacy := orderIssueFixture(1000, 0)
	legacy.Payment = orderIssuePayment{ActualPaidAmount: &paid, AmountPaid: &amountPaid}
	if got := orderIssueReceivedPaise(legacy); got != 10000 {
		t.Fatalf("paid=%d", got)
	}
	legacy.Payment = orderIssuePayment{CODAmount: &cod, UPIAmount: &upi, OnlineAmount: &online, BankTransferAmount: &bank}
	if got := orderIssueReceivedPaise(legacy); got != 6500 {
		t.Fatalf("split=%d", got)
	}
	legacy.Payment.CODAmount = nil
	legacy.CODCollectedAmount = 40
	if got := orderIssueReceivedPaise(legacy); got != 7500 {
		t.Fatalf("legacy cod=%d", got)
	}
	if firstOrderIssueMoney(nil, &paid) != paid || firstOrderIssueMoney(nil) != 0 || pointerMoney(nil) != 0 || pointerMoney(&paid) != paid {
		t.Fatal("money pointer fallbacks are incorrect")
	}
	legacy.Payment = orderIssuePayment{History: []orderIssuePaymentHistory{{Label: "zero", Amount: 0}, {Label: "Cancellation Fee", Amount: 10}, {Label: "Cancellation Charge", Amount: 10}}}
	if got := orderIssueReceivedPaise(legacy); got != 0 {
		t.Fatalf("ignored history=%d", got)
	}
}

func timeNowUTC() time.Time { return time.Now().UTC() }

func TestRepositoryTripRateFallbackBranches(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID, tripID := primitive.NewObjectID(), primitive.NewObjectID()
	tripDoc := bson.D{{Key: "_id", Value: tripID}, {Key: "office_id", Value: officeID}, {Key: "payable_snapshot", Value: bson.D{{Key: "is_paid", Value: false}}}}
	officeDoc := bson.D{{Key: "_id", Value: officeID}, {Key: "petrol_cost_per_liter", Value: 100.0}, {Key: "standard_mileage_per_liter", Value: 25.0}}

	mt.Run("list loads office rates", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".trips", mtest.FirstBatch, tripDoc), mtest.CreateCursorResponse(0, mt.DB.Name()+".offices", mtest.FirstBatch, officeDoc))
		rows, err := NewRepository(mt.DB).LoadTripSources(context.Background(), officeID, "2026-07-01", "2026-07-31")
		if err != nil || len(rows) != 1 || rows[0].OfficePetrolCostPerLiter != 100 || rows[0].OfficeStandardMileagePerLiter != 25 {
			mt.Fatalf("rows=%+v err=%v", rows, err)
		}
	})
	mt.Run("list office rate error", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".trips", mtest.FirstBatch, tripDoc), commandError())
		if _, err := NewRepository(mt.DB).LoadTripSources(context.Background(), officeID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected rate lookup error")
		}
	})
	mt.Run("single loads office rates", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".trips", mtest.FirstBatch, tripDoc), mtest.CreateCursorResponse(0, mt.DB.Name()+".offices", mtest.FirstBatch, officeDoc))
		row, err := NewRepository(mt.DB).LoadTripSource(context.Background(), tripID)
		if err != nil || row.OfficePetrolCostPerLiter != 100 {
			mt.Fatalf("row=%+v err=%v", row, err)
		}
	})
	mt.Run("missing and failed office", func(mt *mtest.T) {
		repo := NewRepository(mt.DB)
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".offices", mtest.FirstBatch))
		cost, mileage, err := repo.loadOfficeTripRates(context.Background(), officeID)
		if err != nil || cost != 0 || mileage != 0 {
			mt.Fatalf("cost=%v mileage=%v err=%v", cost, mileage, err)
		}
		mt.AddMockResponses(commandError())
		if _, _, err := repo.loadOfficeTripRates(context.Background(), officeID); err == nil {
			mt.Fatal("expected lookup error")
		}
	})
}

func TestRepositoryPutSourceEntryUpdateFailures(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	office, worker, entryID := primitive.NewObjectID(), primitive.NewObjectID(), primitive.NewObjectID()
	stored := bson.D{
		{Key: "_id", Value: entryID}, {Key: "office_id", Value: office}, {Key: "worker_id", Value: worker},
		{Key: "status", Value: StatusOpen}, {Key: "settled_amount_paise", Value: int64(0)}, {Key: "amount_paise", Value: int64(1)}, {Key: "idempotency_key", Value: "source"},
	}
	entry := LedgerEntry{OfficeID: office, WorkerID: worker, Status: StatusOpen, AmountPaise: 2, IdempotencyKey: "source"}
	mt.Run("update error", func(mt *mtest.T) {
		mt.AddMockResponses(findAndModifyResponse(stored), commandError())
		if _, _, err := NewRepository(mt.DB).PutSourceEntry(context.Background(), entry); err == nil {
			mt.Fatal("expected update error")
		}
	})
	mt.Run("concurrent state change", func(mt *mtest.T) {
		mt.AddMockResponses(findAndModifyResponse(stored), mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 0}, bson.E{Key: "nModified", Value: 0}))
		got, created, err := NewRepository(mt.DB).PutSourceEntry(context.Background(), entry)
		if err != nil || created || got.ID != entryID || got.AmountPaise != 1 {
			mt.Fatalf("got=%+v created=%t err=%v", got, created, err)
		}
	})
}

func TestTripPayableRemainingBranches(t *testing.T) {
	commission, petrol := effectiveTripPayables(TripSource{})
	if commission != nil || petrol != nil {
		t.Fatal("missing snapshot must produce no payables")
	}
	commissionValue := 12.0
	riderID := primitive.NewObjectID()
	trip := TripSource{
		Snapshot: &PayableSnapshot{IsPaid: true, CommissionPayable: &commissionValue},
		RiderID:  &riderID, Date: "2026-07-21", IsCommissionable: true,
		FareCalculation: TripFareCalculation{TripDistanceKM: 10, PetrolCostPerLiter: 100, StandardMileagePerLiter: 20},
	}
	commission, petrol = effectiveTripPayables(trip)
	if commission == nil || *commission != 12 || petrol == nil || *petrol != 50 {
		t.Fatalf("commission=%v petrol=%v", commission, petrol)
	}
	b := &rebuildBackend{job: RebuildJob{ID: primitive.NewObjectID(), OfficeID: primitive.NewObjectID(), Scope: "petrol", StartDate: "2026-07-01", EndDate: "2026-07-31"}, trips: []TripSource{{ID: primitive.NewObjectID(), RiderID: &riderID, Date: "2026-07-21"}}}
	processed, err := NewProcessor(b).ProcessNext(context.Background())
	if err != nil || !processed || b.stats.MissingSnapshots != 1 {
		t.Fatalf("processed=%t stats=%+v err=%v", processed, b.stats, err)
	}
}
