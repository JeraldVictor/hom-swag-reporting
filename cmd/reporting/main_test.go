package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
)

func signedAdminToken(t *testing.T, secret string) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]interface{}{
		"payload": map[string]interface{}{"sub": "staff-1", "is_admin": true},
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := encodedHeader + "." + encodedPayload
	signature := hmac.New(sha256.New, []byte(secret))
	_, _ = signature.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature.Sum(nil))
}

func TestBearerAuthAcceptsAdminJWTOrInternalServiceToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-jwt-secret")
	t.Setenv("REPORTING_API_TOKEN", "internal-service-token")
	handler := withBearerAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name   string
		token  string
		status int
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "invalid", token: "invalid", status: http.StatusUnauthorized},
		{name: "service", token: "internal-service-token", status: http.StatusNoContent},
		{name: "admin JWT", token: signedAdminToken(t, "test-jwt-secret"), status: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/definitions", nil)
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

type summaryTestExecutor struct {
	validateErr error
}

func (summaryTestExecutor) Key() string  { return "daily_sales" }
func (summaryTestExecutor) Version() int { return 2 }
func (e summaryTestExecutor) Validate(context.Context, reports.Request) error {
	return e.validateErr
}
func (summaryTestExecutor) Run(_ context.Context, _ reports.Request, sink reports.RowSink) error {
	rows := [][]interface{}{
		{"Customer Name", "Cash", "Online", "PAN"},
		{"Asha", 100.0, 50.0, "PAN-1"},
		{"Mina", 25.0, 75.0, "PAN-2"},
		{"Total", 125.0, 125.0, ""},
	}
	for _, row := range rows {
		if err := sink.WriteRow(row); err != nil {
			return err
		}
	}
	return nil
}
func (summaryTestExecutor) Columns() []reports.Column {
	return []reports.Column{
		{Key: "customer_name", Label: "Customer Name"},
		{Key: "cash", Label: "Cash", ContributesToTotal: true},
		{Key: "online", Label: "Online", ContributesToTotal: true},
		{Key: "pan", Label: "PAN", Sensitive: true},
	}
}

func TestNormalizePreviewLimitDefaultsOnlyWhenOmitted(t *testing.T) {
	if got, err := normalizePreviewLimit(nil); err != nil || got != 100 {
		t.Fatalf("omitted limit = %d, %v; want 100, nil", got, err)
	}

	unlimited := 0
	if got, err := normalizePreviewLimit(&unlimited); err != nil || got != 0 {
		t.Fatalf("explicit zero limit = %d, %v; want 0, nil", got, err)
	}

	limited := 500
	if got, err := normalizePreviewLimit(&limited); err != nil || got != 500 {
		t.Fatalf("positive limit = %d, %v; want 500, nil", got, err)
	}

	invalid := -1
	if _, err := normalizePreviewLimit(&invalid); err == nil {
		t.Fatal("negative limit should be rejected")
	}
}

func TestSummarizeRowsUsesRawIndexesAndSkipsTotalRow(t *testing.T) {
	rows := [][]interface{}{
		{"Customer Name", "Cash", "Online"},
		{"Asha", 100.0, 50.0},
		{"Mina", 25.0, 75.0},
		{"Total", 125.0, 125.0},
	}

	summary := summarizeRows(summaryTestExecutor{}, []string{"online"}, rows)

	if summary.RowCount != 2 {
		t.Fatalf("RowCount = %d, want 2", summary.RowCount)
	}
	if summary.Totals["online"] != 125 {
		t.Fatalf("online total = %v, want 125", summary.Totals["online"])
	}
	if _, ok := summary.Totals["cash"]; ok {
		t.Fatalf("cash total should not be present when cash is not selected")
	}
}

func TestRunInMemoryReportProjectsSelectedColumnsForPreview(t *testing.T) {
	req := reports.Request{SelectedColumns: []string{"online", "customer_name"}}

	sink, err := runInMemoryReport(context.Background(), summaryTestExecutor{}, req)
	if err != nil {
		t.Fatalf("runInMemoryReport returned error: %v", err)
	}

	if got := sink.Rows[0]; got[0] != "Online" || got[1] != "Customer Name" {
		t.Fatalf("projected header = %#v", got)
	}
	if got := sink.Rows[1]; got[0] != 50.0 || got[1] != "Asha" {
		t.Fatalf("projected row = %#v", got)
	}
}

func TestRunRawInMemoryReportValidatesSelectionButKeepsRawRowsForSummary(t *testing.T) {
	req := reports.Request{SelectedColumns: []string{"online"}}

	sink, err := runRawInMemoryReport(context.Background(), summaryTestExecutor{}, req)
	if err != nil {
		t.Fatalf("runRawInMemoryReport returned error: %v", err)
	}

	if got := len(sink.Rows[0]); got != 4 {
		t.Fatalf("raw summary rows should keep all columns, got %d", got)
	}

	summary := summarizeRows(summaryTestExecutor{}, req.SelectedColumns, sink.Rows)
	if summary.RowCount != 2 {
		t.Fatalf("RowCount = %d, want 2", summary.RowCount)
	}
	if summary.Totals["online"] != 125 {
		t.Fatalf("online total = %v, want 125", summary.Totals["online"])
	}
	if _, ok := summary.Totals["cash"]; ok {
		t.Fatal("cash should not be summarized when it is not selected")
	}
}

func TestRunInMemoryReportRejectsInvalidAndSensitiveColumns(t *testing.T) {
	invalid := reports.Request{SelectedColumns: []string{"missing"}}
	if _, err := runInMemoryReport(context.Background(), summaryTestExecutor{}, invalid); err == nil {
		t.Fatal("expected invalid selected column to be rejected")
	}

	sensitive := reports.Request{SelectedColumns: []string{"pan"}}
	if _, err := runInMemoryReport(context.Background(), summaryTestExecutor{}, sensitive); err == nil {
		t.Fatal("expected sensitive selected column to be rejected without permission")
	}
}

func TestExportReportUsesSameColumnProjectionAsPreview(t *testing.T) {
	body, contentType, extension, err := exportReport(
		context.Background(),
		summaryTestExecutor{},
		reports.Request{
			Format:          "CSV",
			SelectedColumns: []string{"online", "customer_name"},
		},
		"daily_sales",
	)
	if err != nil {
		t.Fatalf("exportReport returned error: %v", err)
	}
	if contentType != "text/csv" || extension != "csv" {
		t.Fatalf("contentType/extension = %s/%s, want text/csv/csv", contentType, extension)
	}

	records, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("failed to read exported CSV: %v", err)
	}
	if len(records) != 4 {
		t.Fatalf("exported records = %d, want 4", len(records))
	}
	if records[0][0] != "Online" || records[0][1] != "Customer Name" {
		t.Fatalf("exported header = %#v", records[0])
	}
	if records[1][0] != "50" || records[1][1] != "Asha" {
		t.Fatalf("exported row = %#v", records[1])
	}
}
