package static

import "testing"

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
