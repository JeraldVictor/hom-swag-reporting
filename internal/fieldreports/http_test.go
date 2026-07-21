package fieldreports

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JeraldVictor/hom-swag-reporting/internal/earnings"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

const validBody = `{"office_id":"507f1f77bcf86cd799439012","worker_id":"507f1f77bcf86cd799439013","role":"rider","start_date":"2026-07-01","end_date":"2026-07-31"}`

type storeStub struct {
	belongs bool
	detail  earnings.ReportDetail
	err     error
}

func (s storeStub) WorkerBelongsToOffice(context.Context, string, primitive.ObjectID, primitive.ObjectID) (bool, error) {
	return s.belongs, s.err
}

func (s storeStub) LoadReportDetail(context.Context, primitive.ObjectID, primitive.ObjectID, string, string, string) (earnings.ReportDetail, error) {
	return s.detail, s.err
}

func perform(store Store, method, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/field-report-detail", strings.NewReader(body))
	res := httptest.NewRecorder()
	NewAPI(store).Handler().ServeHTTP(res, req)
	return res
}

func TestHandlerReturnsCanonicalDetail(t *testing.T) {
	detail := earnings.ReportDetail{Orders: []earnings.ReportOrder{}, Trips: []earnings.ReportTrip{}, Payouts: []earnings.ReportPayout{}, Adjustments: []earnings.ReportAdjustment{}}
	res := perform(storeStub{belongs: true, detail: detail}, http.MethodPost, validBody)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"trips":[]`) || !strings.Contains(res.Body.String(), `"payouts":[]`) {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestHandlerRejectsInvalidAndCrossOfficeRequests(t *testing.T) {
	tests := []struct {
		name   string
		method string
		body   string
		store  Store
		want   int
	}{
		{name: "method", method: http.MethodGet, body: validBody, store: storeStub{}, want: http.StatusMethodNotAllowed},
		{name: "malformed", method: http.MethodPost, body: `{`, store: storeStub{}, want: http.StatusBadRequest},
		{name: "unknown field", method: http.MethodPost, body: strings.TrimSuffix(validBody, "}") + `,"extra":true}`, store: storeStub{}, want: http.StatusBadRequest},
		{name: "invalid role", method: http.MethodPost, body: strings.Replace(validBody, `"rider"`, `"staff"`, 1), store: storeStub{}, want: http.StatusBadRequest},
		{name: "invalid date", method: http.MethodPost, body: strings.Replace(validBody, `"2026-07-01"`, `"July"`, 1), store: storeStub{}, want: http.StatusBadRequest},
		{name: "reversed dates", method: http.MethodPost, body: strings.Replace(strings.Replace(validBody, `"2026-07-01"`, `"2026-08-01"`, 1), `"2026-07-31"`, `"2026-07-01"`, 1), store: storeStub{}, want: http.StatusBadRequest},
		{name: "cross office", method: http.MethodPost, body: validBody, store: storeStub{belongs: false}, want: http.StatusUnprocessableEntity},
		{name: "store failure", method: http.MethodPost, body: validBody, store: storeStub{belongs: true, err: errors.New("db")}, want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			res := perform(test.store, test.method, test.body)
			if res.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", res.Code, test.want, res.Body.String())
			}
		})
	}
}
