package earnings

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testSecret   = "test-secret"
	testOfficeID = "507f1f77bcf86cd799439012"
	testWorkerID = "507f1f77bcf86cd799439013"
)

func performAPIRequest(t *testing.T, store Store, method, target, body string, claims map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if claims != nil {
		request.Header.Set("Authorization", "Bearer "+signTestToken(t, testSecret, "HS256", claims))
	}
	response := httptest.NewRecorder()
	NewAPI(store, testSecret, ModeAuthoritative).Handler().ServeHTTP(response, request)
	return response
}

func adjustmentBody(overrides ...string) string {
	body := `{"worker_id":"` + testWorkerID + `","worker_type":"beautician","bucket":"commission","amount_paise":1234,"service_date":"2026-07-20","reason":"correction","idempotency_key":"adjust-1"}`
	if len(overrides) > 0 {
		return overrides[0]
	}
	return body
}

type storeWithoutReportDetail struct{ Store }

func TestAPIRoutingAndReadEndpoints(t *testing.T) {
	t.Run("missing token", func(t *testing.T) {
		response := performAPIRequest(t, newMockStore(), http.MethodGet, "/api/earnings/status?office_id="+testOfficeID, "", nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d", response.Code)
		}
	})
	t.Run("invalid subject", func(t *testing.T) {
		claims := validTestClaims()
		claims["payload"].(map[string]interface{})["sub"] = "bad"
		response := performAPIRequest(t, newMockStore(), http.MethodGet, "/api/earnings/status?office_id="+testOfficeID, "", claims)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d", response.Code)
		}
	})
	t.Run("office from header", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/api/earnings/status", nil)
		request.Header.Set("X-Office-ID", testOfficeID)
		request.Header.Set("Authorization", "Bearer "+signTestToken(t, testSecret, "HS256", validTestClaims()))
		response := httptest.NewRecorder()
		NewAPI(newMockStore(), testSecret, "invalid-mode").Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"mode":"shadow"`) {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})
	t.Run("office denied", func(t *testing.T) {
		claims := validTestClaims()
		payload := claims["payload"].(map[string]interface{})
		payload["is_admin"] = false
		payload["office_id"] = "507f1f77bcf86cd799439099"
		response := performAPIRequest(t, newMockStore(), http.MethodGet, "/api/earnings/status?office_id="+testOfficeID, "", claims)
		if response.Code != http.StatusForbidden {
			t.Fatalf("status = %d", response.Code)
		}
	})
	t.Run("status repository error", func(t *testing.T) {
		store := newMockStore()
		store.statusErr = errStore
		response := performAPIRequest(t, store, http.MethodGet, "/api/earnings/status?office_id="+testOfficeID, "", validTestClaims())
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d", response.Code)
		}
	})
	t.Run("status mode repository error", func(t *testing.T) {
		store := newMockStore()
		store.modeStateErr = errStore
		response := performAPIRequest(t, store, http.MethodGet, "/api/earnings/status?office_id="+testOfficeID, "", validTestClaims())
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d", response.Code)
		}
	})
	t.Run("unknown endpoint and method", func(t *testing.T) {
		for _, target := range []struct{ method, path string }{{http.MethodGet, "/api/earnings/missing"}, {http.MethodDelete, "/api/earnings/status"}} {
			response := performAPIRequest(t, newMockStore(), target.method, target.path+"?office_id="+testOfficeID, "", validTestClaims())
			if response.Code != http.StatusNotFound {
				t.Fatalf("%s %s status = %d", target.method, target.path, response.Code)
			}
		}
	})
	t.Run("rebuild history requires rebuild permission and validates status", func(t *testing.T) {
		store := newMockStore()
		store.rebuilds = []RebuildJob{{Status: "completed"}}
		store.rebuildsTotal = 1
		response := performAPIRequest(t, store, http.MethodGet, "/api/earnings/rebuilds?office_id="+testOfficeID+"&status=completed&page=2&limit=20", "", validTestClaims())
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"total":1`) {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
		response = performAPIRequest(t, newMockStore(), http.MethodGet, "/api/earnings/rebuilds?office_id="+testOfficeID+"&status=bogus", "", validTestClaims())
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid status code = %d", response.Code)
		}
		store.rebuildsErr = errStore
		response = performAPIRequest(t, store, http.MethodGet, "/api/earnings/rebuilds?office_id="+testOfficeID, "", validTestClaims())
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("repository error code = %d", response.Code)
		}
	})

	t.Run("ledger validation and repository", func(t *testing.T) {
		tests := []struct {
			name, query string
			store       *mockStore
			want        int
		}{
			{name: "invalid worker", query: "&worker_id=bad", store: newMockStore(), want: 400},
			{name: "invalid dates", query: "&start_date=bad&end_date=2026-07-31", store: newMockStore(), want: 400},
			{name: "repository error", query: "&start_date=2026-07-01&end_date=2026-07-31", store: func() *mockStore { s := newMockStore(); s.entriesErr = errStore; return s }(), want: 500},
			{name: "success", query: "&start_date=2026-07-01&end_date=2026-07-31&worker_id=" + testWorkerID + "&bucket=petrol&status=open&component=petrol&page=2&limit=20", store: func() *mockStore { s := newMockStore(); s.entries = []LedgerEntry{{}}; s.entriesTotal = 1; return s }(), want: 200},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				response := performAPIRequest(t, test.store, http.MethodGet, "/api/earnings/ledger?office_id="+testOfficeID+test.query, "", validTestClaims())
				if response.Code != test.want {
					t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
				}
			})
		}
	})

	t.Run("report detail validation and repository", func(t *testing.T) {
		validQuery := "?office_id=" + testOfficeID + "&worker_id=" + testWorkerID + "&role=rider&start_date=2026-07-01&end_date=2026-07-31"
		tests := []struct {
			name, query string
			store       Store
			want        int
			contains    string
		}{
			{name: "invalid worker", query: strings.Replace(validQuery, testWorkerID, "bad", 1), store: newMockStore(), want: 400},
			{name: "invalid role", query: strings.Replace(validQuery, "role=rider", "role=staff", 1), store: newMockStore(), want: 400},
			{name: "invalid dates", query: strings.Replace(validQuery, "2026-07-01", "bad", 1), store: newMockStore(), want: 400},
			{name: "worker lookup error", query: validQuery, store: func() *mockStore { s := newMockStore(); s.workerErr = errStore; return s }(), want: 500},
			{name: "worker outside office", query: validQuery, store: func() *mockStore { s := newMockStore(); s.workerExists = false; return s }(), want: 422},
			{name: "detail store unavailable", query: validQuery, store: storeWithoutReportDetail{Store: newMockStore()}, want: 500},
			{name: "repository error", query: validQuery, store: func() *mockStore { s := newMockStore(); s.reportDetailErr = errStore; return s }(), want: 500},
			{name: "success", query: validQuery, store: func() *mockStore { s := newMockStore(); s.reportDetail = emptyReportDetail(); return s }(), want: 200, contains: `"trips":[]`},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				response := performAPIRequest(t, test.store, http.MethodGet, "/api/earnings/report-detail"+test.query, "", validTestClaims())
				if response.Code != test.want || test.contains != "" && !strings.Contains(response.Body.String(), test.contains) {
					t.Fatalf("status = %d, want %d, body = %s", response.Code, test.want, response.Body.String())
				}
			})
		}
	})

	t.Run("empty restored database collections are arrays", func(t *testing.T) {
		store := newMockStore()
		query := "?office_id=" + testOfficeID + "&start_date=2026-07-01&end_date=2026-07-31"
		ledger := performAPIRequest(t, store, http.MethodGet, "/api/earnings/ledger"+query, "", validTestClaims())
		if ledger.Code != http.StatusOK || !strings.Contains(ledger.Body.String(), `"entries":[]`) {
			t.Fatalf("ledger status = %d, body = %s", ledger.Code, ledger.Body.String())
		}

		settlements := performAPIRequest(t, store, http.MethodGet, "/api/earnings/settlements"+query, "", validTestClaims())
		if settlements.Code != http.StatusOK || !strings.Contains(settlements.Body.String(), `"settlements":[]`) {
			t.Fatalf("settlements status = %d, body = %s", settlements.Code, settlements.Body.String())
		}
	})

	t.Run("summary success and error", func(t *testing.T) {
		store := newMockStore()
		store.summary = []SummaryRow{{AmountPaise: 1000, SettledAmountPaise: 300}, {AmountPaise: -100, SettledAmountPaise: 0}}
		path := "/api/earnings/summary?office_id=" + testOfficeID + "&start_date=2026-07-01&end_date=2026-07-31"
		response := performAPIRequest(t, store, http.MethodGet, path, "", validTestClaims())
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"pending_paise":600`) {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
		store.summaryErr = errStore
		response = performAPIRequest(t, store, http.MethodGet, path, "", validTestClaims())
		if response.Code != 500 {
			t.Fatalf("status = %d", response.Code)
		}
		response = performAPIRequest(t, store, http.MethodGet, "/api/earnings/summary?office_id="+testOfficeID, "", validTestClaims())
		if response.Code != 400 {
			t.Fatalf("status = %d", response.Code)
		}
	})
}

func TestChangeModeNegativePathsAndSuccess(t *testing.T) {
	authoritativeBody := `{"mode":"authoritative","start_date":"2026-07-01","end_date":"2026-07-31","reason":"approved cutover"}`
	shadowBody := `{"mode":"shadow","reason":"incident rollback"}`

	t.Run("requires cutover permission", func(t *testing.T) {
		claims := validTestClaims()
		payload := claims["payload"].(map[string]interface{})
		payload["is_admin"] = false
		payload["permissions"] = []string{"ledger.read"}
		response := performAPIRequest(t, newMockStore(), http.MethodPost, "/api/earnings/mode?office_id="+testOfficeID, shadowBody, claims)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "ledger.cutover") {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})

	tests := []struct {
		name string
		body string
		set  func(*mockStore)
		want int
	}{
		{name: "malformed json", body: `{`, want: 400},
		{name: "invalid mode", body: `{"mode":"live","reason":"x"}`, want: 400},
		{name: "missing reason", body: `{"mode":"shadow","reason":" "}`, want: 400},
		{name: "long reason", body: `{"mode":"shadow","reason":"` + strings.Repeat("x", 501) + `"}`, want: 400},
		{name: "staff lookup error", body: shadowBody, set: func(s *mockStore) { s.activeStaffErr = errStore }, want: 500},
		{name: "inactive staff", body: shadowBody, set: func(s *mockStore) { s.activeStaff = false }, want: 403},
		{name: "office lookup error", body: shadowBody, set: func(s *mockStore) { s.officeErr = errStore }, want: 500},
		{name: "office missing", body: shadowBody, set: func(s *mockStore) { s.officeExists = false }, want: 404},
		{name: "current mode error", body: shadowBody, set: func(s *mockStore) { s.modeStateErr = errStore }, want: 500},
		{name: "unchanged", body: shadowBody, set: func(s *mockStore) { s.modeState.Mode = ModeShadow }, want: 200},
		{name: "invalid cutover range", body: `{"mode":"authoritative","start_date":"bad","end_date":"2026-07-31","reason":"x"}`, want: 400},
		{name: "active rebuild lookup error", body: authoritativeBody, set: func(s *mockStore) { s.activeRebuildErr = errStore }, want: 500},
		{name: "active rebuild", body: authoritativeBody, set: func(s *mockStore) { s.activeRebuild = true }, want: 409},
		{name: "reconciliation error", body: authoritativeBody, set: func(s *mockStore) { s.reconciliationErr = errStore }, want: 500},
		{name: "reconciliation not ready", body: authoritativeBody, want: 409},
		{name: "set mode error", body: shadowBody, set: func(s *mockStore) { s.setModeErr = errStore }, want: 500},
		{name: "shadow success", body: shadowBody, want: 200},
		{name: "authoritative success", body: authoritativeBody, set: func(s *mockStore) { s.reconciliation.Ready = true }, want: 200},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMockStore()
			if test.set != nil {
				test.set(store)
			}
			response := performAPIRequest(t, store, http.MethodPost, "/api/earnings/mode?office_id="+testOfficeID, test.body, validTestClaims())
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, test.want, response.Body.String())
			}
			if test.name == "shadow success" && (store.lastMode != ModeShadow || store.lastModeReason != "incident rollback") {
				t.Fatalf("mode=%q reason=%q", store.lastMode, store.lastModeReason)
			}
			if test.name == "authoritative success" && !strings.Contains(response.Body.String(), `"reconciliation"`) {
				t.Fatalf("response missing reconciliation: %s", response.Body.String())
			}
		})
	}
}

func TestCreateAdjustmentNegativePathsAndSuccess(t *testing.T) {
	tests := []struct {
		name string
		body string
		set  func(*mockStore)
		want int
	}{
		{name: "malformed json", body: `{`, want: 400},
		{name: "invalid worker id", body: adjustmentBody(`{"worker_id":"bad","worker_type":"beautician","bucket":"commission","amount_paise":1,"service_date":"2026-07-20","reason":"x","idempotency_key":"x"}`), want: 400},
		{name: "invalid worker type", body: adjustmentBody(`{"worker_id":"` + testWorkerID + `","worker_type":"staff","bucket":"commission","amount_paise":1,"service_date":"2026-07-20","reason":"x","idempotency_key":"x"}`), want: 400},
		{name: "invalid bucket", body: adjustmentBody(`{"worker_id":"` + testWorkerID + `","worker_type":"rider","bucket":"cash","amount_paise":1,"service_date":"2026-07-20","reason":"x","idempotency_key":"x"}`), want: 400},
		{name: "invalid date", body: adjustmentBody(`{"worker_id":"` + testWorkerID + `","worker_type":"rider","bucket":"petrol","amount_paise":1,"service_date":"2026-02-30","reason":"x","idempotency_key":"x"}`), want: 400},
		{name: "missing reason", body: adjustmentBody(`{"worker_id":"` + testWorkerID + `","worker_type":"rider","bucket":"petrol","amount_paise":1,"service_date":"2026-07-20","reason":"","idempotency_key":"x"}`), want: 400},
		{name: "long fields", body: adjustmentBody(`{"worker_id":"` + testWorkerID + `","worker_type":"rider","bucket":"petrol","amount_paise":1,"service_date":"2026-07-20","reason":"` + strings.Repeat("x", 501) + `","idempotency_key":"x"}`), want: 400},
		{name: "zero", body: adjustmentBody(`{"worker_id":"` + testWorkerID + `","worker_type":"rider","bucket":"petrol","amount_paise":0,"service_date":"2026-07-20","reason":"x","idempotency_key":"x"}`), want: 400},
		{name: "staff lookup error", body: adjustmentBody(), set: func(s *mockStore) { s.activeStaffErr = errStore }, want: 500},
		{name: "inactive staff", body: adjustmentBody(), set: func(s *mockStore) { s.activeStaff = false }, want: 403},
		{name: "office error", body: adjustmentBody(), set: func(s *mockStore) { s.officeErr = errStore }, want: 500},
		{name: "office missing", body: adjustmentBody(), set: func(s *mockStore) { s.officeExists = false }, want: 404},
		{name: "worker error", body: adjustmentBody(), set: func(s *mockStore) { s.workerErr = errStore }, want: 500},
		{name: "worker missing", body: adjustmentBody(), set: func(s *mockStore) { s.workerExists = false }, want: 422},
		{name: "closed lookup error", body: adjustmentBody(), set: func(s *mockStore) { s.dateClosedErr = errStore }, want: 500},
		{name: "closed date", body: adjustmentBody(), set: func(s *mockStore) { s.dateClosed = true }, want: 409},
		{name: "create error", body: adjustmentBody(), set: func(s *mockStore) { s.adjustmentErr = errStore }, want: 500},
		{name: "created", body: adjustmentBody(), want: 201},
		{name: "idempotent replay", body: adjustmentBody(), set: func(s *mockStore) { s.adjustmentCreated = false }, want: 200},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMockStore()
			if test.set != nil {
				test.set(store)
			}
			response := performAPIRequest(t, store, http.MethodPost, "/api/earnings/adjustments?office_id="+testOfficeID, test.body, validTestClaims())
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, test.want, response.Body.String())
			}
			if test.name == "created" && (store.lastAdjustment.Component != ComponentCommissionAdjustment || store.lastAdjustment.AmountPaise != 1234) {
				t.Fatalf("unexpected entry: %#v", store.lastAdjustment)
			}
		})
	}
	t.Run("petrol component", func(t *testing.T) {
		store := newMockStore()
		body := `{"worker_id":"` + testWorkerID + `","worker_type":"rider","bucket":"petrol","amount_paise":-50,"service_date":"2026-07-20","reason":"x","idempotency_key":"p"}`
		response := performAPIRequest(t, store, http.MethodPost, "/api/earnings/adjustments?office_id="+testOfficeID, body, validTestClaims())
		if response.Code != 201 || store.lastAdjustment.Component != ComponentPetrolAdjustment {
			t.Fatalf("status = %d, entry = %#v", response.Code, store.lastAdjustment)
		}
	})
}

func TestClosePeriodAndRebuildPaths(t *testing.T) {
	periodBody := `{"kind":"monthly","start_date":"2026-07-01","end_date":"2026-07-31"}`
	rebuildBody := `{"scope":"all","start_date":"2026-07-01","end_date":"2026-07-31","idempotency_key":"r1"}`

	t.Run("close period", func(t *testing.T) {
		tests := []struct {
			name, body string
			set        func(*mockStore)
			want       int
		}{
			{name: "bad json", body: `{`, want: 400},
			{name: "bad kind", body: `{"kind":"daily","start_date":"2026-07-01","end_date":"2026-07-31"}`, want: 400},
			{name: "bad range", body: `{"kind":"weekly","start_date":"2026-08-01","end_date":"2026-07-01"}`, want: 400},
			{name: "staff error", body: periodBody, set: func(s *mockStore) { s.activeStaffErr = errStore }, want: 500},
			{name: "inactive", body: periodBody, set: func(s *mockStore) { s.activeStaff = false }, want: 403},
			{name: "office error", body: periodBody, set: func(s *mockStore) { s.officeErr = errStore }, want: 500},
			{name: "office missing", body: periodBody, set: func(s *mockStore) { s.officeExists = false }, want: 404},
			{name: "overlap error", body: periodBody, set: func(s *mockStore) { s.activeRebuildErr = errStore }, want: 500},
			{name: "overlap", body: periodBody, set: func(s *mockStore) { s.activeRebuild = true }, want: 409},
			{name: "close error", body: periodBody, set: func(s *mockStore) { s.periodErr = errStore }, want: 500},
			{name: "created", body: periodBody, want: 201},
			{name: "existing", body: periodBody, set: func(s *mockStore) { s.periodCreated = false }, want: 200},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				store := newMockStore()
				if test.set != nil {
					test.set(store)
				}
				response := performAPIRequest(t, store, http.MethodPost, "/api/earnings/periods/close?office_id="+testOfficeID, test.body, validTestClaims())
				if response.Code != test.want {
					t.Fatalf("status = %d, want %d, body = %s", response.Code, test.want, response.Body.String())
				}
			})
		}
	})

	t.Run("queue rebuild", func(t *testing.T) {
		tests := []struct {
			name, body string
			set        func(*mockStore)
			want       int
		}{
			{name: "bad json", body: `{`, want: 400},
			{name: "bad range", body: `{"scope":"all","start_date":"bad","end_date":"2026-07-31","idempotency_key":"x"}`, want: 400},
			{name: "bad scope", body: `{"scope":"sales","start_date":"2026-07-01","end_date":"2026-07-31","idempotency_key":"x"}`, want: 400},
			{name: "missing key", body: `{"scope":"petrol","start_date":"2026-07-01","end_date":"2026-07-31","idempotency_key":""}`, want: 400},
			{name: "staff error", body: rebuildBody, set: func(s *mockStore) { s.activeStaffErr = errStore }, want: 500},
			{name: "inactive", body: rebuildBody, set: func(s *mockStore) { s.activeStaff = false }, want: 403},
			{name: "office error", body: rebuildBody, set: func(s *mockStore) { s.officeErr = errStore }, want: 500},
			{name: "office missing", body: rebuildBody, set: func(s *mockStore) { s.officeExists = false }, want: 404},
			{name: "closed error", body: rebuildBody, set: func(s *mockStore) { s.closedOverlapErr = errStore }, want: 500},
			{name: "closed", body: rebuildBody, set: func(s *mockStore) { s.closedOverlap = true }, want: 409},
			{name: "queue error", body: rebuildBody, set: func(s *mockStore) { s.rebuildErr = errStore }, want: 500},
			{name: "created", body: rebuildBody, want: 202},
			{name: "existing", body: rebuildBody, set: func(s *mockStore) { s.rebuildCreated = false }, want: 200},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				store := newMockStore()
				if test.set != nil {
					test.set(store)
				}
				response := performAPIRequest(t, store, http.MethodPost, "/api/earnings/rebuilds?office_id="+testOfficeID, test.body, validTestClaims())
				if response.Code != test.want {
					t.Fatalf("status = %d, want %d, body = %s", response.Code, test.want, response.Body.String())
				}
			})
		}
	})
}
