package fieldsettlements

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JeraldVictor/hom-swag-reporting/internal/earnings"
)

const (
	testOfficeID = "507f1f77bcf86cd799439012"
	testWorkerID = "507f1f77bcf86cd799439013"
)

type settlementStoreStub struct {
	rows  []earnings.Settlement
	total int64
	err   error
}

func (s settlementStoreStub) ListSettlements(context.Context, earnings.SettlementFilter) ([]earnings.Settlement, int64, error) {
	return s.rows, s.total, s.err
}

func performRequest(store Store, method, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/field-settlements", strings.NewReader(body))
	response := httptest.NewRecorder()
	NewAPI(store).Handler().ServeHTTP(response, request)
	return response
}

func validBody(extra ...string) string {
	body := `{"office_id":"` + testOfficeID + `","worker_id":"` + testWorkerID + `"}`
	if len(extra) > 0 {
		return extra[0]
	}
	return body
}

func TestHandlerReturnsEmptyArrayForRestoredDatabase(t *testing.T) {
	response := performRequest(settlementStoreStub{}, http.MethodPost, validBody())
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"settlements":[]`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandlerValidationAndStoreFailures(t *testing.T) {
	tests := []struct {
		name   string
		method string
		body   string
		store  Store
		want   int
	}{
		{name: "method", method: http.MethodGet, body: validBody(), store: settlementStoreStub{}, want: http.StatusMethodNotAllowed},
		{name: "malformed json", method: http.MethodPost, body: `{`, store: settlementStoreStub{}, want: http.StatusBadRequest},
		{name: "unknown field", method: http.MethodPost, body: validBody(`{"office_id":"` + testOfficeID + `","worker_id":"` + testWorkerID + `","extra":true}`), store: settlementStoreStub{}, want: http.StatusBadRequest},
		{name: "multiple objects", method: http.MethodPost, body: validBody() + `{}`, store: settlementStoreStub{}, want: http.StatusBadRequest},
		{name: "invalid office", method: http.MethodPost, body: validBody(`{"office_id":"bad","worker_id":"` + testWorkerID + `"}`), store: settlementStoreStub{}, want: http.StatusBadRequest},
		{name: "invalid worker", method: http.MethodPost, body: validBody(`{"office_id":"` + testOfficeID + `","worker_id":"bad"}`), store: settlementStoreStub{}, want: http.StatusBadRequest},
		{name: "store failure", method: http.MethodPost, body: validBody(), store: settlementStoreStub{err: errors.New("database unavailable")}, want: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performRequest(test.store, test.method, test.body)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

func TestHandlerUsesPaginationDefaultsAndValues(t *testing.T) {
	row := earnings.Settlement{}
	response := performRequest(settlementStoreStub{rows: []earnings.Settlement{row}, total: 1}, http.MethodPost,
		`{"office_id":"`+testOfficeID+`","worker_id":"`+testWorkerID+`","page":2,"limit":24}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"page":2`) || !strings.Contains(response.Body.String(), `"limit":24`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	response = performRequest(settlementStoreStub{}, http.MethodPost,
		`{"office_id":"`+testOfficeID+`","worker_id":"`+testWorkerID+`","page":-1,"limit":25}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"page":1`) || !strings.Contains(response.Body.String(), `"limit":10`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}
