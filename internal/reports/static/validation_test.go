package static

import (
	"context"
	"testing"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
)

func TestValidateReportDateRangeRejectsInvalidParameters(t *testing.T) {
	fixtures := []struct {
		name       string
		parameters map[string]interface{}
	}{
		{
			name:       "missing start date",
			parameters: map[string]interface{}{"end_date": "2026-07-31"},
		},
		{
			name:       "missing end date",
			parameters: map[string]interface{}{"start_date": "2026-07-01"},
		},
		{
			name: "non string start date",
			parameters: map[string]interface{}{
				"start_date": 123,
				"end_date":   "2026-07-31",
			},
		},
		{
			name: "invalid date text",
			parameters: map[string]interface{}{
				"start_date": "not-a-date",
				"end_date":   "2026-07-31",
			},
		},
		{
			name: "end date before start date",
			parameters: map[string]interface{}{
				"start_date": "2026-07-31",
				"end_date":   "2026-07-01",
			},
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			err := validateReportDateRange(fixture.parameters, parseReportDate)
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestReportExecutorsValidateDateRangesBeforeRunning(t *testing.T) {
	executors := []reports.Executor{
		NewDailySalesExecutor(nil),
		NewCODPendingExecutor(nil),
		NewBeauticianCommissionExecutor(nil),
		NewRiderCommissionExecutor(nil),
		NewPetrolWeeklyExecutor(nil),
		NewStaffSummaryExecutor(nil),
	}

	req := reports.Request{
		Parameters: map[string]interface{}{
			"start_date": "2026-07-31",
			"end_date":   "2026-07-01",
		},
	}

	for _, executor := range executors {
		t.Run(executor.Key(), func(t *testing.T) {
			if err := executor.Validate(context.Background(), req); err == nil {
				t.Fatal("expected reversed date range to be rejected")
			}
		})
	}
}

func TestValidateReportDateRangeAcceptsDateOnlyAndRFC3339(t *testing.T) {
	dateOnly := map[string]interface{}{
		"start_date": "2026-07-01",
		"end_date":   "2026-07-31",
	}
	if err := validateReportDateRange(dateOnly, parseReportDate); err != nil {
		t.Fatalf("date-only range should be valid: %v", err)
	}

	rfc3339 := map[string]interface{}{
		"start_date": "2026-07-01T00:00:00Z",
		"end_date":   "2026-07-31T23:59:59Z",
	}
	if err := validateReportDateRange(rfc3339, parseReportDate); err != nil {
		t.Fatalf("RFC3339 range should be valid: %v", err)
	}
}
