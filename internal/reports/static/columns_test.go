package static

import (
	"testing"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
)

func TestDailySalesColumnsExposeCustomerFriendlyCalculationDescriptions(t *testing.T) {
	columns := withColumnDescriptions(dailySalesColumns)
	descriptionsByKey := make(map[string]string, len(columns))
	for _, column := range columns {
		descriptionsByKey[column.Key] = column.Description
	}

	requiredDescriptions := []string{
		"total_value",
		"net_receivable_including_gst",
		"total_received",
		"cash",
		"online",
		"bank_transfer",
		"total_discount_provided",
	}

	for _, key := range requiredDescriptions {
		if descriptionsByKey[key] == "" {
			t.Fatalf("expected customer-friendly description for %s", key)
		}
	}
}

func TestReportColumnDefinitionsHaveStableKeysAndDescriptions(t *testing.T) {
	reportColumns := map[string][]string{
		"daily_sales":           columnKeys(dailySalesColumns),
		"rider_commission":      columnKeys(riderCommissionColumns),
		"petrol_weekly":         columnKeys(petrolWeeklyColumns),
		"beautician_commission": columnKeys(beauticianCommissionColumns),
		"staff_summary":         columnKeys(staffSummaryColumns),
		"cod_pending":           columnKeys(codPendingColumns),
	}

	for reportKey, keys := range reportColumns {
		t.Run(reportKey, func(t *testing.T) {
			seen := map[string]struct{}{}
			for _, key := range keys {
				if key == "" {
					t.Fatal("column key should not be empty")
				}
				if _, exists := seen[key]; exists {
					t.Fatalf("duplicate column key %s", key)
				}
				seen[key] = struct{}{}
			}
		})
	}
}

func TestFinancialReportColumnsExposeFormulaDescriptions(t *testing.T) {
	allColumns := []reportColumnSet{
		{name: "daily_sales", columns: dailySalesColumns},
		{name: "rider_commission", columns: riderCommissionColumns},
		{name: "petrol_weekly", columns: petrolWeeklyColumns},
		{name: "beautician_commission", columns: beauticianCommissionColumns},
		{name: "staff_summary", columns: staffSummaryColumns},
		{name: "cod_pending", columns: codPendingColumns},
	}

	for _, set := range allColumns {
		t.Run(set.name, func(t *testing.T) {
			for _, column := range withColumnDescriptions(set.columns) {
				if column.ContributesToTotal && column.FormulaID == "" {
					t.Fatalf("%s contributes to totals but has no formula id", column.Key)
				}
				if column.FormulaID != "" && column.Description == "" {
					t.Fatalf("%s has formula id %s but no customer description", column.Key, column.FormulaID)
				}
			}
		})
	}
}

type reportColumnSet struct {
	name    string
	columns []reports.Column
}

func columnKeys(columns []reports.Column) []string {
	keys := make([]string, len(columns))
	for index, column := range columns {
		keys[index] = column.Key
	}
	return keys
}
