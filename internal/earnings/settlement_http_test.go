package earnings

import (
	"net/http"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func settlementBody(overrides ...string) string {
	if len(overrides) > 0 {
		return overrides[0]
	}
	return `{"worker_id":"` + testWorkerID + `","worker_type":"beautician","bucket":"commission","start_date":"2026-07-01","end_date":"2026-07-31","amount_paise":1234,"payment_method":"bank_transfer","reference":" bank-1 ","remarks":" July payout ","idempotency_key":" settle-1 "}`
}

func TestCreateSettlementValidationAndGuards(t *testing.T) {
	tests := []struct {
		name string
		body string
		set  func(*mockStore)
		want int
	}{
		{name: "bad json", body: `{`, want: 400},
		{name: "bad worker id", body: settlementBody(`{"worker_id":"bad","worker_type":"beautician","bucket":"commission","start_date":"2026-07-01","end_date":"2026-07-31","amount_paise":1,"payment_method":"cash","idempotency_key":"x"}`), want: 400},
		{name: "bad worker type", body: settlementBody(`{"worker_id":"` + testWorkerID + `","worker_type":"staff","bucket":"commission","start_date":"2026-07-01","end_date":"2026-07-31","amount_paise":1,"payment_method":"cash","idempotency_key":"x"}`), want: 400},
		{name: "bad bucket", body: settlementBody(`{"worker_id":"` + testWorkerID + `","worker_type":"rider","bucket":"bonus","start_date":"2026-07-01","end_date":"2026-07-31","amount_paise":1,"payment_method":"cash","idempotency_key":"x"}`), want: 400},
		{name: "bad dates", body: settlementBody(`{"worker_id":"` + testWorkerID + `","worker_type":"rider","bucket":"petrol","start_date":"2026-08-01","end_date":"2026-07-31","amount_paise":1,"payment_method":"cash","idempotency_key":"x"}`), want: 400},
		{name: "nonpositive amount", body: settlementBody(`{"worker_id":"` + testWorkerID + `","worker_type":"rider","bucket":"petrol","start_date":"2026-07-01","end_date":"2026-07-31","amount_paise":0,"payment_method":"cash","idempotency_key":"x"}`), want: 400},
		{name: "bad method", body: settlementBody(`{"worker_id":"` + testWorkerID + `","worker_type":"rider","bucket":"petrol","start_date":"2026-07-01","end_date":"2026-07-31","amount_paise":1,"payment_method":"card","idempotency_key":"x"}`), want: 400},
		{name: "missing key", body: settlementBody(`{"worker_id":"` + testWorkerID + `","worker_type":"rider","bucket":"petrol","start_date":"2026-07-01","end_date":"2026-07-31","amount_paise":1,"payment_method":"upi","idempotency_key":" "}`), want: 400},
		{name: "bad selected entry", body: settlementBody(`{"worker_id":"` + testWorkerID + `","worker_type":"rider","bucket":"petrol","start_date":"2026-07-01","end_date":"2026-07-31","amount_paise":1,"payment_method":"upi","idempotency_key":"x","entry_ids":["bad"]}`), want: 400},
		{name: "duplicate selected entry", body: settlementBody(`{"worker_id":"` + testWorkerID + `","worker_type":"rider","bucket":"petrol","start_date":"2026-07-01","end_date":"2026-07-31","amount_paise":1,"payment_method":"upi","idempotency_key":"x","entry_ids":["` + testWorkerID + `","` + testWorkerID + `"]}`), want: 400},
		{name: "long fields", body: settlementBody(`{"worker_id":"` + testWorkerID + `","worker_type":"rider","bucket":"petrol","start_date":"2026-07-01","end_date":"2026-07-31","amount_paise":1,"payment_method":"other","idempotency_key":"x","remarks":"` + strings.Repeat("r", 501) + `"}`), want: 400},
		{name: "staff error", body: settlementBody(), set: func(s *mockStore) { s.activeStaffErr = errStore }, want: 500},
		{name: "inactive staff", body: settlementBody(), set: func(s *mockStore) { s.activeStaff = false }, want: 403},
		{name: "office error", body: settlementBody(), set: func(s *mockStore) { s.officeErr = errStore }, want: 500},
		{name: "office missing", body: settlementBody(), set: func(s *mockStore) { s.officeExists = false }, want: 404},
		{name: "replay lookup error", body: settlementBody(), set: func(s *mockStore) { s.existingSettlementErr = errStore }, want: 500},
		{name: "replay bypasses later guards", body: settlementBody(), set: func(s *mockStore) {
			s.existingSettlementFound = true
			s.existingSettlement = Settlement{AmountPaise: 1234}
			s.workerExists = false
			s.closedOverlap = true
		}, want: 200},
		{name: "worker error", body: settlementBody(), set: func(s *mockStore) { s.workerErr = errStore }, want: 500},
		{name: "worker missing", body: settlementBody(), set: func(s *mockStore) { s.workerExists = false }, want: 422},
		{name: "period lookup error", body: settlementBody(), set: func(s *mockStore) { s.closedOverlapErr = errStore }, want: 500},
		{name: "closed overlap", body: settlementBody(), set: func(s *mockStore) { s.closedOverlap = true }, want: 409},
		{name: "rebuild lookup error", body: settlementBody(), set: func(s *mockStore) { s.activeRebuildErr = errStore }, want: 500},
		{name: "active rebuild", body: settlementBody(), set: func(s *mockStore) { s.activeRebuild = true }, want: 409},
		{name: "no pending", body: settlementBody(), set: func(s *mockStore) { s.settlementErr = ErrNoPendingEarnings }, want: 409},
		{name: "overpayment", body: settlementBody(), set: func(s *mockStore) { s.settlementErr = ErrSettlementExceedsPending }, want: 409},
		{name: "repository error", body: settlementBody(), set: func(s *mockStore) { s.settlementErr = errStore }, want: 500},
		{name: "created", body: settlementBody(), want: 201},
		{name: "idempotent replay", body: settlementBody(), set: func(s *mockStore) { s.settlementCreated = false }, want: 200},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMockStore()
			if test.set != nil {
				test.set(store)
			}
			response := performAPIRequest(t, store, http.MethodPost, "/api/earnings/settlements?office_id="+testOfficeID, test.body, validTestClaims())
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.want, response.Body.String())
			}
			if test.name == "created" {
				if store.lastSettlement.AmountPaise != 1234 || store.lastSettlement.IdempotencyKey != "settle-1" || store.lastSettlement.Reference != "bank-1" {
					t.Fatalf("settlement was not normalized: %#v", store.lastSettlement)
				}
			}
		})
	}
}

func TestCreateSettlementPassesSelectedEntries(t *testing.T) {
	entryID := primitive.NewObjectID()
	body := `{"worker_id":"` + testWorkerID + `","worker_type":"rider","bucket":"petrol","start_date":"2026-07-01","end_date":"2026-07-31","amount_paise":1234,"payment_method":"upi","reference":"","remarks":"","idempotency_key":"selected-trips","entry_ids":["` + entryID.Hex() + `"]}`
	store := newMockStore()
	response := performAPIRequest(t, store, http.MethodPost, "/api/earnings/settlements?office_id="+testOfficeID, body, validTestClaims())
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(store.lastSettlement.RequestedEntryIDs) != 1 || store.lastSettlement.RequestedEntryIDs[0] != entryID {
		t.Fatalf("requested entries=%v", store.lastSettlement.RequestedEntryIDs)
	}
}

func TestListSettlements(t *testing.T) {
	tests := []struct {
		name, query string
		set         func(*mockStore)
		want        int
	}{
		{name: "bad worker", query: "&worker_id=bad", want: 400},
		{name: "bad bucket", query: "&bucket=bonus", want: 400},
		{name: "one date", query: "&start_date=2026-07-01", want: 400},
		{name: "bad range", query: "&start_date=2026-08-01&end_date=2026-07-01", want: 400},
		{name: "store error", set: func(s *mockStore) { s.settlementsErr = errStore }, want: 500},
		{name: "success", query: "&worker_id=" + testWorkerID + "&bucket=petrol&start_date=2026-07-01&end_date=2026-07-31&page=2&limit=20", set: func(s *mockStore) {
			s.settlements = []Settlement{{AmountPaise: 10}}
			s.settlementsTotal = 1
		}, want: 200},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMockStore()
			if test.set != nil {
				test.set(store)
			}
			response := performAPIRequest(t, store, http.MethodGet, "/api/earnings/settlements?office_id="+testOfficeID+test.query, "", validTestClaims())
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.want, response.Body.String())
			}
			if test.name == "success" && (store.lastSettlementFilter.Page != 2 || store.lastSettlementFilter.Bucket != "petrol") {
				t.Fatalf("filter=%#v", store.lastSettlementFilter)
			}
		})
	}
}

func TestUpdateSettlement(t *testing.T) {
	settlementID := primitive.NewObjectID()
	existing := Settlement{
		ID: settlementID, OfficeID: mustObjectID(testOfficeID), WorkerID: mustObjectID(testWorkerID),
		WorkerType: "beautician", Bucket: BucketCommission, StartDate: "2026-07-01", EndDate: "2026-07-31",
		AmountPaise: 1234,
	}
	validBody := `{"amount_paise":1500,"payment_method":"upi","reference":" corrected ","remarks":" corrected payout "}`
	tests := []struct {
		name string
		id   string
		body string
		set  func(*mockStore)
		want int
	}{
		{name: "invalid id", id: "bad", body: validBody, want: http.StatusBadRequest},
		{name: "invalid body", id: settlementID.Hex(), body: `{`, want: http.StatusBadRequest},
		{name: "invalid amount", id: settlementID.Hex(), body: `{"amount_paise":0,"payment_method":"cash","reference":"","remarks":""}`, want: http.StatusBadRequest},
		{name: "missing", id: settlementID.Hex(), body: validBody, want: http.StatusNotFound},
		{name: "closed period", id: settlementID.Hex(), body: validBody, set: func(s *mockStore) {
			s.existingSettlement, s.existingSettlementFound, s.closedOverlap = existing, true, true
		}, want: http.StatusConflict},
		{name: "active rebuild", id: settlementID.Hex(), body: validBody, set: func(s *mockStore) {
			s.existingSettlement, s.existingSettlementFound, s.activeRebuild = existing, true, true
		}, want: http.StatusConflict},
		{name: "success", id: settlementID.Hex(), body: validBody, set: func(s *mockStore) { s.existingSettlement, s.existingSettlementFound = existing, true }, want: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMockStore()
			if test.set != nil {
				test.set(store)
			}
			response := performAPIRequest(t, store, http.MethodPatch, "/api/earnings/settlements/"+test.id+"?office_id="+testOfficeID, test.body, validTestClaims())
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.name == "success" && (store.lastSettlement.AmountPaise != 1500 || store.lastSettlement.Reference != "corrected") {
				t.Fatalf("update was not normalized: %#v", store.lastSettlement)
			}
		})
	}
}
