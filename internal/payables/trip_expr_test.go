package payables

import (
	"reflect"
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
	if _, ok := PayableDistanceExpr()["$cond"]; !ok {
		t.Fatal("missing distance fallback")
	}
}
