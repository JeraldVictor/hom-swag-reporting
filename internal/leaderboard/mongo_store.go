package leaderboard

import (
	"context"
	"errors"

	"github.com/JeraldVictor/hom-swag-reporting/internal/payables"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoStore struct{ db *mongo.Database }

func NewMongoStore(db *mongo.Database) *MongoStore { return &MongoStore{db: db} }

func (s *MongoStore) BeauticianScores(ctx context.Context, officeID primitive.ObjectID, startDate, endDate string) ([]SourceScore, error) {
	cursor, err := s.db.Collection("orders").Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"office_id": officeID, "status": "completed", "is_deleted": bson.M{"$ne": true},
			"beautician_id": bson.M{"$ne": nil}, "booking_info.date": bson.M{"$gte": startDate, "$lte": endDate},
		}}},
		{{Key: "$project", Value: bson.M{
			"beautician_id": 1,
			"order_revenue": bson.M{"$max": bson.A{0, bson.M{"$ifNull": bson.A{
				"$order_cost", bson.M{"$ifNull": bson.A{"$revenue", bson.M{"$subtract": bson.A{
					bson.M{"$ifNull": bson.A{"$subtotal", 0}}, bson.M{"$ifNull": bson.A{"$discount_total", 0}},
				}}}},
			}}}},
		}}},
		{{Key: "$group", Value: bson.M{
			"_id": "$beautician_id", "gross_revenue": bson.M{"$sum": "$order_revenue"},
			"order_count": bson.M{"$sum": 1}, "order_ids": bson.M{"$push": "$_id"},
		}}},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	type orderScore struct {
		WorkerID     primitive.ObjectID   `bson:"_id"`
		GrossRevenue float64              `bson:"gross_revenue"`
		OrderCount   int                  `bson:"order_count"`
		OrderIDs     []primitive.ObjectID `bson:"order_ids"`
	}
	var rows []orderScore
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, err
	}
	orderIDs := make([]primitive.ObjectID, 0)
	for _, row := range rows {
		orderIDs = append(orderIDs, row.OrderIDs...)
	}
	deductions := map[primitive.ObjectID]float64{}
	if len(orderIDs) > 0 {
		complaintCursor, err := s.db.Collection("complaints").Aggregate(ctx, mongo.Pipeline{
			{{Key: "$match", Value: bson.M{
				"office_id": officeID, "order_id": bson.M{"$in": orderIDs}, "status": "closed", "is_deleted": bson.M{"$ne": true},
			}}},
			{{Key: "$unwind", Value: "$activity_log"}},
			{{Key: "$match", Value: bson.M{"activity_log.resolution_type": "beautician_deduction"}}},
			{{Key: "$group", Value: bson.M{"_id": "$target_id", "deduction": bson.M{"$sum": bson.M{"$ifNull": bson.A{"$activity_log.amount", 0}}}}}},
		})
		if err != nil {
			return nil, err
		}
		defer complaintCursor.Close(ctx)
		var deductionRows []struct {
			WorkerID primitive.ObjectID `bson:"_id"`
			Amount   float64            `bson:"deduction"`
		}
		if err := complaintCursor.All(ctx, &deductionRows); err != nil {
			return nil, err
		}
		for _, row := range deductionRows {
			deductions[row.WorkerID] = max(0, row.Amount)
		}
	}
	scores := make([]SourceScore, 0, len(rows))
	for _, row := range rows {
		scores = append(scores, SourceScore{
			WorkerID: row.WorkerID, Count: row.OrderCount,
			Amount: max(0, row.GrossRevenue-deductions[row.WorkerID]),
		})
	}
	return scores, nil
}

func (s *MongoStore) RiderScores(ctx context.Context, officeID primitive.ObjectID, startDate, endDate string) ([]SourceScore, error) {
	match := payables.TripBaseMatch(startDate, endDate)
	match["office_id"], match["rider_id"] = officeID, bson.M{"$ne": nil}
	cursor, err := s.db.Collection("trips").Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$addFields", Value: bson.M{"payable_distance_km": payables.SnapshotOrLegacyExpr("payable_distance_km", payables.PayableDistanceExpr())}}},
		{{Key: "$group", Value: bson.M{
			"_id": "$rider_id", "trip_count": bson.M{"$sum": 1},
			"total_distance_km": bson.M{"$sum": "$payable_distance_km"},
		}}},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var rows []struct {
		WorkerID      primitive.ObjectID `bson:"_id"`
		TripCount     int                `bson:"trip_count"`
		TotalDistance float64            `bson:"total_distance_km"`
	}
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, err
	}
	scores := make([]SourceScore, 0, len(rows))
	for _, row := range rows {
		scores = append(scores, SourceScore{WorkerID: row.WorkerID, Count: row.TripCount, Amount: row.TotalDistance})
	}
	return scores, nil
}

func (s *MongoStore) Profiles(ctx context.Context, role string, workerIDs []primitive.ObjectID, gender string) ([]Profile, error) {
	if len(workerIDs) == 0 {
		return []Profile{}, nil
	}
	collection := "riders"
	if role == "beautician" {
		collection = "beauticians"
	}
	filter := bson.M{"_id": bson.M{"$in": workerIDs}, "is_deleted": bson.M{"$ne": true}}
	if gender != "" && gender != "all" {
		filter["gender"] = gender
	}
	cursor, err := s.db.Collection(collection).Find(ctx, filter, options.Find().SetProjection(bson.M{
		"name": 1, "photo": 1, "gender": 1, "can_view_leaderboard": 1,
	}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var profiles []Profile
	return profiles, cursor.All(ctx, &profiles)
}

func (s *MongoStore) Prizes(ctx context.Context, officeID primitive.ObjectID) (PrizeSchedule, error) {
	var office struct {
		Prizes PrizeSchedule `bson:"leaderboard_prizes"`
	}
	err := s.db.Collection("offices").FindOne(ctx, bson.M{"_id": officeID}, options.FindOne().SetProjection(bson.M{"leaderboard_prizes": 1})).Decode(&office)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return PrizeSchedule{}, nil
	}
	return office.Prizes, err
}
