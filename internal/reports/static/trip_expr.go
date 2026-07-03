package static

import (
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func payableTripKanbanStates() bson.A {
	return bson.A{"trip_completed", "fare_calculation_pending", "completed"}
}

func payableTripStatusMatch() bson.M {
	return bson.M{"$or": bson.A{
		bson.M{"kanban_state": bson.M{"$in": payableTripKanbanStates()}},
		bson.M{"status": "completed"},
	}}
}

func payableTripBaseMatch(startDateKey string, endDateKey string) bson.M {
	return bson.M{
		"date": bson.M{
			"$gte": startDateKey,
			"$lte": endDateKey,
		},
		"is_deleted": bson.M{"$ne": true},
		"$and":       bson.A{payableTripStatusMatch()},
	}
}

func tripPayableDistanceExpr() bson.M {
	baseDistance := bson.M{"$ifNull": bson.A{"$auto_distance_km", 0}}
	tripDistance := bson.M{"$ifNull": bson.A{"$fare_calculation.trip_distance_km", 0}}
	extraDistance := bson.M{"$ifNull": bson.A{"$extra_km", 0}}

	return bson.M{"$cond": bson.A{
		bson.M{"$gt": bson.A{tripDistance, 0}},
		tripDistance,
		bson.M{"$add": bson.A{
			bson.M{"$cond": bson.A{
				"$is_two_way",
				bson.M{"$multiply": bson.A{baseDistance, 2}},
				baseDistance,
			}},
			extraDistance,
		}},
	}}
}

func tripPetrolPayableExpr(distanceExpr any) bson.M {
	petrolCost := bson.M{"$ifNull": bson.A{"$office.petrol_cost_per_liter", 0}}
	mileage := bson.M{"$ifNull": bson.A{"$office.standard_mileage_per_liter", 0}}

	return bson.M{"$ifNull": bson.A{
		"$fare_calculation.calculated_fare",
		bson.M{"$cond": bson.A{
			bson.M{"$and": bson.A{
				bson.M{"$gt": bson.A{petrolCost, 0}},
				bson.M{"$gt": bson.A{mileage, 0}},
			}},
			bson.M{"$round": bson.A{
				bson.M{"$multiply": bson.A{
					bson.M{"$divide": bson.A{distanceExpr, mileage}},
					petrolCost,
				}},
				2,
			}},
			0,
		}},
	}}
}

func tripOfficeLookupStages() mongo.Pipeline {
	return mongo.Pipeline{
		{{Key: "$lookup", Value: bson.M{
			"from":         "offices",
			"localField":   "office_id",
			"foreignField": "_id",
			"as":           "office",
		}}},
		{{Key: "$unwind", Value: bson.M{
			"path":                       "$office",
			"preserveNullAndEmptyArrays": true,
		}}},
	}
}
