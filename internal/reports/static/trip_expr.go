package static

import (
	"github.com/JeraldVictor/hom-swag-reporting/internal/payables"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func payableTripKanbanStates() bson.A {
	return bson.A{"trip_completed", "fare_calculation_pending", "completed"}
}

func payableTripStatusMatch() bson.M {
	return payables.TripStatusMatch()
}

func payableTripBaseMatch(startDateKey string, endDateKey string) bson.M {
	return payables.TripBaseMatch(startDateKey, endDateKey)
}

func tripAllowanceWorkerIDExpr() bson.M {
	return payables.AllowanceWorkerIDExpr()
}

func tripSnapshotOrLegacyExpr(field string, legacy any) bson.M {
	return payables.SnapshotOrLegacyExpr(field, legacy)
}

func tripPayableDistanceExpr() bson.M {
	return payables.PayableDistanceExpr()
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
