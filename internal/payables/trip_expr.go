package payables

import "go.mongodb.org/mongo-driver/bson"

func TripStatusMatch() bson.M {
	return bson.M{"$or": bson.A{
		bson.M{"kanban_state": bson.M{"$in": bson.A{"trip_completed", "fare_calculation_pending", "completed"}}},
		bson.M{"status": "completed"},
	}}
}

func TripBaseMatch(startDateKey, endDateKey string) bson.M {
	return bson.M{"date": bson.M{"$gte": startDateKey, "$lte": endDateKey}, "is_deleted": bson.M{"$ne": true}, "$and": bson.A{TripStatusMatch()}}
}

func AllowanceWorkerIDExpr() bson.M {
	return bson.M{"$ifNull": bson.A{"$driver_beautician_id", bson.M{"$cond": bson.A{
		bson.M{"$and": bson.A{
			bson.M{"$eq": bson.A{bson.M{"$ifNull": bson.A{"$is_self_drive", false}}, true}},
			bson.M{"$ne": bson.A{bson.M{"$ifNull": bson.A{"$beautician_id", nil}}, nil}},
		}}, "$beautician_id", "$rider_id",
	}}}}
}

// AllowanceWorkerTypeExpr must use the same null-safe precedence as
// AllowanceWorkerIDExpr. MongoDB's $ne comparison against a missing field does
// not behave like a comparison against an explicit null, which previously
// caused legacy rider trips to be labelled as beautician earnings.
func AllowanceWorkerTypeExpr() bson.M {
	driver := bson.M{"$ifNull": bson.A{"$driver_beautician_id", nil}}
	beautician := bson.M{"$ifNull": bson.A{"$beautician_id", nil}}
	selfDrive := bson.M{"$ifNull": bson.A{"$is_self_drive", false}}
	return bson.M{"$cond": bson.A{
		bson.M{"$or": bson.A{
			bson.M{"$ne": bson.A{driver, nil}},
			bson.M{"$and": bson.A{
				bson.M{"$eq": bson.A{selfDrive, true}},
				bson.M{"$ne": bson.A{beautician, nil}},
			}},
		}},
		"beautician", "rider",
	}}
}

func SnapshotOrLegacyExpr(field string, legacy any) bson.M {
	return bson.M{"$ifNull": bson.A{"$payable_snapshot." + field, legacy}}
}

// PaidSnapshotOrCanonicalExpr freezes settled source values, while allowing an
// earnings rebuild to repair stale snapshots on trips that are still unpaid.
func PaidSnapshotOrCanonicalExpr(field string, canonical any) bson.M {
	snapshotField := "$payable_snapshot." + field
	return bson.M{"$cond": bson.A{
		bson.M{"$and": bson.A{
			bson.M{"$eq": bson.A{"$payable_snapshot.is_paid", true}},
			bson.M{"$ne": bson.A{bson.M{"$ifNull": bson.A{snapshotField, nil}}, nil}},
		}},
		snapshotField,
		canonical,
	}}
}

func PayableDistanceExpr() bson.M {
	base := bson.M{"$ifNull": bson.A{"$auto_distance_km", 0}}
	trip := bson.M{"$ifNull": bson.A{"$fare_calculation.trip_distance_km", 0}}
	extra := bson.M{"$ifNull": bson.A{"$extra_km", 0}}
	automatic := bson.M{"$add": bson.A{
		bson.M{"$cond": bson.A{"$is_two_way", bson.M{"$multiply": bson.A{base, 2}}, base}}, extra,
	}}
	return bson.M{"$cond": bson.A{
		bson.M{"$and": bson.A{bson.M{"$eq": bson.A{"$is_distance_manually_overridden", true}}, bson.M{"$gt": bson.A{trip, 0}}}},
		trip,
		bson.M{"$cond": bson.A{bson.M{"$gt": bson.A{base, 0}}, automatic, trip}},
	}}
}
