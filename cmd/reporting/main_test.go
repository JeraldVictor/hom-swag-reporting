package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"testing"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
)

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
