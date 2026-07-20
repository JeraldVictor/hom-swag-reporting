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
		bson.M{"$and": bson.A{"$is_self_drive", bson.M{"$ne": bson.A{"$beautician_id", nil}}}}, "$beautician_id", "$rider_id",
	}}}}
}

func SnapshotOrLegacyExpr(field string, legacy any) bson.M {
	return bson.M{"$ifNull": bson.A{"$payable_snapshot." + field, legacy}}
}

func PayableDistanceExpr() bson.M {
	base := bson.M{"$ifNull": bson.A{"$auto_distance_km", 0}}
	trip := bson.M{"$ifNull": bson.A{"$fare_calculation.trip_distance_km", 0}}
	extra := bson.M{"$ifNull": bson.A{"$extra_km", 0}}
	return bson.M{"$cond": bson.A{bson.M{"$gt": bson.A{trip, 0}}, trip, bson.M{"$add": bson.A{
		bson.M{"$cond": bson.A{"$is_two_way", bson.M{"$multiply": bson.A{base, 2}}, base}}, extra,
	}}}}
}
