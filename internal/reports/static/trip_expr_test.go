package static

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func TestPayableTripBaseMatchIncludesLegacyAndNewCompletedStates(t *testing.T) {
	match := payableTripBaseMatch("2026-07-01", "2026-07-31")

	if deleted, ok := match["is_deleted"].(bson.M); !ok || deleted["$ne"] != true {
		t.Fatalf("is_deleted match = %#v, want only true records excluded", match["is_deleted"])
	}

	clauses, ok := match["$and"].(bson.A)
	if !ok || len(clauses) != 1 {
		t.Fatalf("$and = %#v, want payable status clause", match["$and"])
	}

	statusClause, ok := clauses[0].(bson.M)
	if !ok {
		t.Fatalf("status clause = %#v, want bson.M", clauses[0])
	}
	options, ok := statusClause["$or"].(bson.A)
	if !ok || len(options) != 2 {
		t.Fatalf("status options = %#v, want kanban or legacy status", statusClause["$or"])
	}
}

func TestTripPayableDistanceMatchesLegacyNodeFallback(t *testing.T) {
	fixtures := []struct {
		name     string
		document map[string]any
		expected float64
	}{
		{
			name: "uses fare calculation distance when available",
			document: map[string]any{
				"fare_calculation": map[string]any{"trip_distance_km": 12.5},
				"auto_distance_km": 9,
				"extra_km":         3,
				"is_two_way":       true,
			},
			expected: 12.5,
		},
		{
			name: "falls back to one way auto distance plus extra km",
			document: map[string]any{
				"auto_distance_km": 8,
				"extra_km":         1.5,
				"is_two_way":       false,
			},
			expected: 9.5,
		},
		{
			name: "falls back to two way auto distance plus extra km",
			document: map[string]any{
				"fare_calculation": map[string]any{"trip_distance_km": 0},
				"auto_distance_km": 8,
				"extra_km":         2,
				"is_two_way":       true,
			},
			expected: 18,
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			assertEvaluatedMoney(t, "payable_distance_km", tripPayableDistanceExpr(), fixture.document, fixture.expected)
		})
	}
}

func TestTripAllowanceWorkerAttributionPriority(t *testing.T) {
	expression := tripAllowanceWorkerIDExpr()
	fixtures := []struct {
		name     string
		document map[string]any
		expected string
	}{
		{
			name: "explicit driver beautician wins",
			document: map[string]any{
				"driver_beautician_id": "driver-beautician",
				"beautician_id":        "legacy-beautician",
				"rider_id":             "rider",
				"is_self_drive":        true,
			},
			expected: "driver-beautician",
		},
		{
			name: "legacy self drive beautician is retained",
			document: map[string]any{
				"beautician_id": "legacy-beautician",
				"rider_id":      "rider",
				"is_self_drive": true,
			},
			expected: "legacy-beautician",
		},
		{
			name: "ordinary trip belongs to rider",
			document: map[string]any{
				"beautician_id": "passenger-beautician",
				"rider_id":      "rider",
				"is_self_drive": false,
			},
			expected: "rider",
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			got := evalTestExpr(t, expression, fixture.document, nil)
			if got != fixture.expected {
				t.Fatalf("worker = %#v, want %q", got, fixture.expected)
			}
		})
	}
}

func TestTripSnapshotZeroIsAuthoritative(t *testing.T) {
	document := map[string]any{
		"payable_snapshot": map[string]any{
			"payable_distance_km": 0,
			"petrol_payable":      0,
			"commission_payable":  0,
		},
		"auto_distance_km":         10,
		"commission_amount":        25,
		"is_commission_applicable": true,
	}

	assertEvaluatedMoney(t, "zero snapshot distance", tripSnapshotOrLegacyExpr("payable_distance_km", tripPayableDistanceExpr()), document, 0)
	assertEvaluatedMoney(t, "zero snapshot petrol", tripSnapshotOrLegacyExpr("petrol_payable", 50), document, 0)
	assertEvaluatedMoney(t, "zero snapshot commission", tripSnapshotOrLegacyExpr("commission_payable", 25), document, 0)
}

func TestTripSnapshotFallsBackOnlyWhenMissingOrNull(t *testing.T) {
	assertEvaluatedMoney(t, "missing snapshot", tripSnapshotOrLegacyExpr("petrol_payable", 50), map[string]any{}, 50)
	assertEvaluatedMoney(t, "null snapshot", tripSnapshotOrLegacyExpr("petrol_payable", 50), map[string]any{
		"payable_snapshot": map[string]any{"petrol_payable": nil},
	}, 50)
}

func TestTripPetrolPayableFallsBackToOfficeFareFormula(t *testing.T) {
	distanceExpr := tripPayableDistanceExpr()

	t.Run("uses fare calculation amount when available", func(t *testing.T) {
		document := map[string]any{
			"fare_calculation": map[string]any{
				"trip_distance_km": 12,
				"calculated_fare":  77,
			},
			"office": map[string]any{
				"petrol_cost_per_liter":      110,
				"standard_mileage_per_liter": 55,
			},
		}

		assertEvaluatedMoney(t, "petrol_payable", tripPetrolPayableExpr(distanceExpr), document, 77)
	})

	t.Run("calculates from payable distance and office petrol settings", func(t *testing.T) {
		document := map[string]any{
			"auto_distance_km": 10,
			"extra_km":         2,
			"is_two_way":       true,
			"office": map[string]any{
				"petrol_cost_per_liter":      105,
				"standard_mileage_per_liter": 42,
			},
		}

		assertEvaluatedMoney(t, "petrol_payable", tripPetrolPayableExpr(distanceExpr), document, 55)
	})

	t.Run("returns zero when office settings are missing", func(t *testing.T) {
		document := map[string]any{
			"auto_distance_km": 10,
			"is_two_way":       false,
			"office":           map[string]any{},
		}

		assertEvaluatedMoney(t, "petrol_payable", tripPetrolPayableExpr(distanceExpr), document, 0)
	})
}
