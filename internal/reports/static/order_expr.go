package static

import (
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func orderReportBaseMatch(startDate time.Time, endDate time.Time, matchStart string, matchEnd string) bson.M {
	return bson.M{
		"is_deleted": bson.M{"$ne": true},
		"$or":        orderReportDateClauses(startDate, endDate, matchStart, matchEnd),
	}
}

func orderReportDateClauses(startDate time.Time, endDate time.Time, matchStart string, matchEnd string) bson.A {
	return bson.A{
		bson.M{"service_date": bson.M{"$gte": startDate, "$lte": endDate}},
		bson.M{
			"service_date": bson.M{"$exists": false},
			"booking_info.date": bson.M{
				"$gte": matchStart,
				"$lte": matchEnd,
			},
		},
		bson.M{
			"service_date": nil,
			"booking_info.date": bson.M{
				"$gte": matchStart,
				"$lte": matchEnd,
			},
		},
		bson.M{"booking_info.date": bson.M{"$gte": matchStart, "$lte": matchEnd}},
	}
}
