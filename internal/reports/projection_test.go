package reports

import (
	"context"
	"testing"
)

type testExecutor struct{}

func (testExecutor) Key() string                                 { return "test_report" }
func (testExecutor) Version() int                                { return 1 }
func (testExecutor) Validate(context.Context, Request) error     { return nil }
func (testExecutor) Run(context.Context, Request, RowSink) error { return nil }
func (testExecutor) Columns() []Column {
	return []Column{
		{Key: "customer_name", Label: "Customer Name"},
		{Key: "cash", Label: "Cash"},
		{Key: "online", Label: "Online"},
		{Key: "pan", Label: "PAN", Sensitive: true},
	}
}

type memorySink struct {
	rows [][]interface{}
}

func (s *memorySink) WriteRow(row []interface{}) error {
	s.rows = append(s.rows, row)
	return nil
}

func TestProjectionSinkSelectsRequestedColumnsInOrder(t *testing.T) {
	inner := &memorySink{}
	sink, err := NewProjectionSink(testExecutor{}, []string{"online", "customer_name"}, inner)
	if err != nil {
		t.Fatalf("NewProjectionSink returned error: %v", err)
	}

	if err := sink.WriteRow([]interface{}{"Customer Name", "Cash", "Online"}); err != nil {
		t.Fatalf("WriteRow header returned error: %v", err)
	}
	if err := sink.WriteRow([]interface{}{"Asha", 120, 80}); err != nil {
		t.Fatalf("WriteRow data returned error: %v", err)
	}

	if got := inner.rows[0]; got[0] != "Online" || got[1] != "Customer Name" {
		t.Fatalf("projected header = %#v", got)
	}
	if got := inner.rows[1]; got[0] != 80 || got[1] != "Asha" {
		t.Fatalf("projected row = %#v", got)
	}
}

func TestProjectionSinkRejectsInvalidColumns(t *testing.T) {
	_, err := NewProjectionSink(testExecutor{}, []string{"missing_column"}, &memorySink{})
	if err == nil {
		t.Fatal("expected invalid column error")
	}
}

func TestProjectionSinkRejectsDuplicateColumns(t *testing.T) {
	_, err := NewProjectionSink(testExecutor{}, []string{"cash", "cash"}, &memorySink{})
	if err == nil {
		t.Fatal("expected duplicate column error")
	}
}

func TestProjectionSinkRejectsSensitiveColumnsWithoutPermission(t *testing.T) {
	_, err := NewProjectionSinkWithOptions(
		testExecutor{},
		[]string{"pan"},
		&memorySink{},
		ProjectionOptions{AllowSensitive: false},
	)
	if err == nil {
		t.Fatal("expected sensitive column permission error")
	}
}

func TestProjectionSinkOmitsSensitiveColumnsByDefaultWithoutPermission(t *testing.T) {
	inner := &memorySink{}
	sink, err := NewProjectionSinkWithOptions(
		testExecutor{},
		nil,
		inner,
		ProjectionOptions{AllowSensitive: false},
	)
	if err != nil {
		t.Fatalf("NewProjectionSinkWithOptions returned error: %v", err)
	}

	if err := sink.WriteRow([]interface{}{"Customer Name", "Cash", "Online", "PAN"}); err != nil {
		t.Fatalf("WriteRow header returned error: %v", err)
	}

	if got := inner.rows[0]; len(got) != 3 {
		t.Fatalf("projected row should omit sensitive column: %#v", got)
	}
}
