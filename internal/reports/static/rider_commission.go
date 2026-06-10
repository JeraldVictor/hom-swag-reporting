package static

import (
	"context"
	"github.com/jeraldvictor/hom-swag/reporting/internal/reports"
)

type RiderCommissionExecutor struct{}

func (e *RiderCommissionExecutor) Key() string {
	return "rider_commission"
}

func (e *RiderCommissionExecutor) Version() int {
	return 1
}

func (e *RiderCommissionExecutor) Validate(ctx context.Context, req reports.Request) error {
	return nil
}

func (e *RiderCommissionExecutor) Run(ctx context.Context, req reports.Request, sink reports.RowSink) error {
	// Write header
	sink.WriteRow([]interface{}{"Rider ID", "Rider Name", "Commission Amount", "Date"})

	// Placeholder data
	sink.WriteRow([]interface{}{"R1", "John Doe", 50.0, "2026-06-01"})
	sink.WriteRow([]interface{}{"R2", "Jane Smith", 75.5, "2026-06-02"})

	return nil
}
