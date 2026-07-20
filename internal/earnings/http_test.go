package earnings

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateDateRange(t *testing.T) {
	tests := []struct {
		name, start, end string
		wantErr          bool
	}{
		{name: "valid", start: "2026-07-01", end: "2026-07-31"},
		{name: "invalid calendar date", start: "2026-02-30", end: "2026-03-01", wantErr: true},
		{name: "reversed", start: "2026-08-01", end: "2026-07-31", wantErr: true},
		{name: "missing", start: "", end: "2026-07-31", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateDateRange(test.start, test.end); (err != nil) != test.wantErr {
				t.Fatalf("validateDateRange() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestBoundedPagination(t *testing.T) {
	request := httptest.NewRequest("GET", "/?page=2&limit=999", nil)
	page, limit := boundedPagination(request)
	if page != 2 || limit != 200 {
		t.Fatalf("boundedPagination() = (%d, %d), want (2, 200)", page, limit)
	}
}

func TestDecodeJSONRejectsTrailingObject(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"value":1}{"value":2}`))
	var target struct {
		Value int `json:"value"`
	}
	if err := decodeJSON(request, &target); err == nil {
		t.Fatal("decodeJSON() should reject multiple JSON objects")
	}
}

func TestAPIAuthorizationStopsBeforeRepositoryAccess(t *testing.T) {
	const secret = "test-secret"
	officeID := "507f1f77bcf86cd799439012"

	t.Run("missing permission", func(t *testing.T) {
		claims := validTestClaims()
		claims["payload"].(map[string]interface{})["permissions"] = []string{}
		token := signTestToken(t, secret, "HS256", claims)
		request := httptest.NewRequest(http.MethodGet, "/api/earnings/status?office_id="+officeID, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		NewAPI(nil, secret, ModeShadow).Handler().ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "ledger.read") {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})

	t.Run("invalid office", func(t *testing.T) {
		token := signTestToken(t, secret, "HS256", validTestClaims())
		request := httptest.NewRequest(http.MethodGet, "/api/earnings/status?office_id=invalid", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		NewAPI(nil, secret, ModeShadow).Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})

	t.Run("zero paise adjustment", func(t *testing.T) {
		token := signTestToken(t, secret, "HS256", validTestClaims())
		body := bytes.NewBufferString(`{
			"worker_id":"507f1f77bcf86cd799439013",
			"worker_type":"beautician",
			"bucket":"commission",
			"amount_paise":0,
			"service_date":"2026-07-20",
			"reason":"test",
			"idempotency_key":"test-zero"
		}`)
		request := httptest.NewRequest(http.MethodPost, "/api/earnings/adjustments?office_id="+officeID, body)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		NewAPI(nil, secret, ModeShadow).Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "cannot be zero") {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})
}
