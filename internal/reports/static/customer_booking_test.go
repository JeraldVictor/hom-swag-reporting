package static

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestCustomerBookingValidateRequiresOfficeAndValidFilters(t *testing.T) {
	executor := NewCustomerBookingExecutor(nil)
	valid := reports.Request{Parameters: map[string]interface{}{
		"start_date":      "2026-07-01",
		"end_date":        "2026-07-31",
		"office_id":       primitive.NewObjectID().Hex(),
		"order_status":    "completed",
		"customer_status": "active",
		"zone_ids":        []interface{}{primitive.NewObjectID().Hex()},
		"customer_search": "Asha",
	}}
	if err := executor.Validate(context.Background(), valid); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	fixtures := []struct {
		name   string
		mutate func(map[string]interface{})
	}{
		{name: "missing office", mutate: func(parameters map[string]interface{}) { delete(parameters, "office_id") }},
		{name: "invalid office", mutate: func(parameters map[string]interface{}) { parameters["office_id"] = "bad" }},
		{name: "invalid zone", mutate: func(parameters map[string]interface{}) { parameters["zone_ids"] = []interface{}{"bad"} }},
		{name: "invalid order status", mutate: func(parameters map[string]interface{}) { parameters["order_status"] = "unknown" }},
		{name: "invalid customer status", mutate: func(parameters map[string]interface{}) { parameters["customer_status"] = "unknown" }},
		{name: "non string search", mutate: func(parameters map[string]interface{}) { parameters["customer_search"] = 42 }},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			parameters := make(map[string]interface{}, len(valid.Parameters))
			for key, value := range valid.Parameters {
				parameters[key] = value
			}
			fixture.mutate(parameters)
			if err := executor.Validate(context.Background(), reports.Request{Parameters: parameters}); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCustomerBookingZoneIDsSupportsMultipleAndLegacyValues(t *testing.T) {
	zoneA := primitive.NewObjectID()
	zoneB := primitive.NewObjectID()
	zoneIDs, err := customerBookingZoneIDs(map[string]interface{}{
		"zone_ids": []interface{}{zoneA.Hex(), zoneB.Hex(), zoneA.Hex()},
	})
	if err != nil {
		t.Fatalf("parse multiple zones: %v", err)
	}
	if got, want := zoneIDs, []primitive.ObjectID{zoneA, zoneB}; !reflect.DeepEqual(got, want) {
		t.Fatalf("zone IDs = %#v, want %#v", got, want)
	}

	legacy, err := customerBookingZoneIDs(map[string]interface{}{"zone_id": zoneA.Hex()})
	if err != nil || !reflect.DeepEqual(legacy, []primitive.ObjectID{zoneA}) {
		t.Fatalf("legacy zone = %#v, %v", legacy, err)
	}
}

func TestCustomerBookingDefaultsToCompletedOrdersAndAllCustomers(t *testing.T) {
	filters, err := parseCustomerBookingFilters(map[string]interface{}{
		"start_date": "2026-07-01",
		"end_date":   "2026-07-31",
		"office_id":  primitive.NewObjectID().Hex(),
	})
	if err != nil {
		t.Fatalf("parse filters: %v", err)
	}
	if filters.OrderStatus != "completed" {
		t.Fatalf("order status = %q, want completed", filters.OrderStatus)
	}
	if filters.CustomerStatus != customerStatusAll {
		t.Fatalf("customer status = %q, want all", filters.CustomerStatus)
	}
}

func TestCustomerBookingRowFormatsZonesAndAddress(t *testing.T) {
	row := customerBookingRow{
		CustomerID:      primitive.NewObjectID(),
		CustomerName:    "Asha",
		IsActive:        true,
		RegisteredAt:    time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC),
		LastBookingDate: time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC),
		BookingCount:    3,
		ZoneNames:       []string{"Zone XYZ", "Zone ABC", "Zone XYZ", ""},
		AddressCount:    2,
		DefaultAddress: customerAddress{
			BuildingInfo: "Flat 2",
			Street:       "Main Road",
			City:         "Kolkata",
			State:        "West Bengal",
			Pincode:      "700001",
		},
	}

	values := row.values()
	if got, want := values[6], "active"; got != want {
		t.Fatalf("status = %v, want %v", got, want)
	}
	if got, want := values[8], "2026-07-20"; got != want {
		t.Fatalf("last booking = %v, want %v", got, want)
	}
	if got, want := values[10], "Zone ABC, Zone XYZ"; got != want {
		t.Fatalf("zones = %v, want %v", got, want)
	}
	if got, want := values[12], "Flat 2, Main Road, Kolkata, West Bengal, 700001"; got != want {
		t.Fatalf("address = %v, want %v", got, want)
	}
}

func TestUniqueSortedStrings(t *testing.T) {
	got := uniqueSortedStrings([]string{" B ", "A", "B", "", "A"})
	want := []string{"A", "B"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uniqueSortedStrings() = %#v, want %#v", got, want)
	}
}
