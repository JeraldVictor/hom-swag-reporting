package payables

import (
	"reflect"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func TestTripBaseMatchIncludesDateDeletionAndPayableStatuses(t *testing.T) {
	got := TripBaseMatch("2026-07-01", "2026-07-31")
	if !reflect.DeepEqual(got["date"], bson.M{"$gte": "2026-07-01", "$lte": "2026-07-31"}) {
		t.Fatalf("date match = %#v", got["date"])
	}
	if _, ok := got["is_deleted"]; !ok {
		t.Fatal("missing deletion filter")
	}
	if _, ok := got["$and"]; !ok {
		t.Fatal("missing status filter")
	}
	if len(TripStatusMatch()["$or"].(bson.A)) != 2 {
		t.Fatal("unexpected status alternatives")
	}
}

func TestTripExpressionsRetainSnapshotAndWorkerPrecedence(t *testing.T) {
	if got := SnapshotOrLegacyExpr("payable_distance_km", 7); !reflect.DeepEqual(got, bson.M{"$ifNull": bson.A{"$payable_snapshot.payable_distance_km", 7}}) {
		t.Fatalf("snapshot expression = %#v", got)
	}
	if _, ok := AllowanceWorkerIDExpr()["$ifNull"]; !ok {
		t.Fatal("missing worker precedence")
	}
	workerType := AllowanceWorkerTypeExpr()
	if _, ok := workerType["$cond"]; !ok {
		t.Fatal("missing worker type precedence")
	}
	encoded, err := bson.MarshalExtJSON(workerType, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"$ifNull", "driver_beautician_id", "is_self_drive", "beautician_id"} {
		if !strings.Contains(string(encoded), required) {
			t.Fatalf("worker type expression does not contain %q: %s", required, encoded)
		}
	}
	if _, ok := PayableDistanceExpr()["$cond"]; !ok {
		t.Fatal("missing distance fallback")
	}
}
