package main

import (
	"context"
	"testing"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
)

type summaryTestExecutor struct{}

func (summaryTestExecutor) Key() string                                     { return "daily_sales" }
func (summaryTestExecutor) Version() int                                    { return 2 }
func (summaryTestExecutor) Validate(context.Context, reports.Request) error { return nil }
func (summaryTestExecutor) Run(context.Context, reports.Request, reports.RowSink) error {
	return nil
}
func (summaryTestExecutor) Columns() []reports.Column {
	return []reports.Column{
		{Key: "customer_name", Label: "Customer Name"},
		{Key: "cash", Label: "Cash", ContributesToTotal: true},
		{Key: "online", Label: "Online", ContributesToTotal: true},
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
