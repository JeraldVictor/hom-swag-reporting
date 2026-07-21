package leaderboard

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func leaderboardCommandError() bson.D {
	return mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 123, Message: "mock failure"})
}

func TestMongoStoreBeauticianScores(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID, workerID, orderID := primitive.NewObjectID(), primitive.NewObjectID(), primitive.NewObjectID()
	mt.Run("scores subtract non-negative complaint deductions", func(mt *mtest.T) {
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".orders", mtest.FirstBatch, bson.D{
				{Key: "_id", Value: workerID}, {Key: "gross_revenue", Value: 1000.0},
				{Key: "order_count", Value: 2}, {Key: "order_ids", Value: bson.A{orderID}},
			}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".complaints", mtest.FirstBatch, bson.D{
				{Key: "_id", Value: workerID}, {Key: "deduction", Value: 1200.0},
			}),
		)
		scores, err := NewMongoStore(mt.DB).BeauticianScores(context.Background(), officeID, "2026-07-01", "2026-07-31")
		if err != nil || len(scores) != 1 || scores[0].Amount != 0 || scores[0].Count != 2 {
			mt.Fatalf("scores=%#v err=%v", scores, err)
		}
	})
	mt.Run("empty orders skip complaints", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".orders", mtest.FirstBatch))
		scores, err := NewMongoStore(mt.DB).BeauticianScores(context.Background(), officeID, "2026-07-01", "2026-07-31")
		if err != nil || len(scores) != 0 {
			mt.Fatalf("scores=%#v err=%v", scores, err)
		}
	})
	mt.Run("order aggregate error", func(mt *mtest.T) {
		mt.AddMockResponses(leaderboardCommandError())
		if _, err := NewMongoStore(mt.DB).BeauticianScores(context.Background(), officeID, "a", "b"); err == nil {
			mt.Fatal("expected error")
		}
	})
	mt.Run("order decode error", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".orders", mtest.FirstBatch, bson.D{{Key: "_id", Value: "bad"}}))
		if _, err := NewMongoStore(mt.DB).BeauticianScores(context.Background(), officeID, "a", "b"); err == nil {
			mt.Fatal("expected error")
		}
	})
	mt.Run("complaint aggregate error", func(mt *mtest.T) {
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".orders", mtest.FirstBatch, bson.D{{Key: "_id", Value: workerID}, {Key: "order_ids", Value: bson.A{orderID}}}),
			leaderboardCommandError(),
		)
		if _, err := NewMongoStore(mt.DB).BeauticianScores(context.Background(), officeID, "a", "b"); err == nil {
			mt.Fatal("expected error")
		}
	})
	mt.Run("complaint decode error", func(mt *mtest.T) {
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".orders", mtest.FirstBatch, bson.D{{Key: "_id", Value: workerID}, {Key: "order_ids", Value: bson.A{orderID}}}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".complaints", mtest.FirstBatch, bson.D{{Key: "_id", Value: "bad"}}),
		)
		if _, err := NewMongoStore(mt.DB).BeauticianScores(context.Background(), officeID, "a", "b"); err == nil {
			mt.Fatal("expected error")
		}
	})
}

func TestMongoStoreRidersProfilesAndPrizes(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID, workerID := primitive.NewObjectID(), primitive.NewObjectID()
	mt.Run("rider scores", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".trips", mtest.FirstBatch, bson.D{
			{Key: "_id", Value: workerID}, {Key: "trip_count", Value: 3}, {Key: "total_distance_km", Value: 12.5},
		}))
		scores, err := NewMongoStore(mt.DB).RiderScores(context.Background(), officeID, "2026-07-01", "2026-07-31")
		if err != nil || len(scores) != 1 || scores[0].Amount != 12.5 {
			mt.Fatalf("scores=%#v err=%v", scores, err)
		}
	})
	mt.Run("rider aggregate error", func(mt *mtest.T) {
		mt.AddMockResponses(leaderboardCommandError())
		if _, err := NewMongoStore(mt.DB).RiderScores(context.Background(), officeID, "a", "b"); err == nil {
			mt.Fatal("expected error")
		}
	})
	mt.Run("rider decode error", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".trips", mtest.FirstBatch, bson.D{{Key: "_id", Value: "bad"}}))
		if _, err := NewMongoStore(mt.DB).RiderScores(context.Background(), officeID, "a", "b"); err == nil {
			mt.Fatal("expected error")
		}
	})
	mt.Run("empty profiles", func(mt *mtest.T) {
		profiles, err := NewMongoStore(mt.DB).Profiles(context.Background(), "rider", nil, "all")
		if err != nil || len(profiles) != 0 {
			mt.Fatalf("profiles=%#v err=%v", profiles, err)
		}
	})
	mt.Run("rider profiles", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".riders", mtest.FirstBatch, bson.D{{Key: "_id", Value: workerID}, {Key: "name", Value: "Rider"}}))
		profiles, err := NewMongoStore(mt.DB).Profiles(context.Background(), "rider", []primitive.ObjectID{workerID}, "female")
		if err != nil || len(profiles) != 1 || profiles[0].Name != "Rider" {
			mt.Fatalf("profiles=%#v err=%v", profiles, err)
		}
	})
	mt.Run("beautician profiles", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".beauticians", mtest.FirstBatch, bson.D{{Key: "_id", Value: workerID}, {Key: "name", Value: "Beautician"}}))
		profiles, err := NewMongoStore(mt.DB).Profiles(context.Background(), "beautician", []primitive.ObjectID{workerID}, "all")
		if err != nil || len(profiles) != 1 {
			mt.Fatalf("profiles=%#v err=%v", profiles, err)
		}
	})
	mt.Run("profiles find error", func(mt *mtest.T) {
		mt.AddMockResponses(leaderboardCommandError())
		if _, err := NewMongoStore(mt.DB).Profiles(context.Background(), "rider", []primitive.ObjectID{workerID}, "all"); err == nil {
			mt.Fatal("expected error")
		}
	})
	mt.Run("profiles decode error", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".riders", mtest.FirstBatch, bson.D{{Key: "_id", Value: "bad"}}))
		if _, err := NewMongoStore(mt.DB).Profiles(context.Background(), "rider", []primitive.ObjectID{workerID}, "all"); err == nil {
			mt.Fatal("expected error")
		}
	})
	mt.Run("prizes", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".offices", mtest.FirstBatch, bson.D{
			{Key: "leaderboard_prizes", Value: bson.D{{Key: "beutician", Value: bson.A{100.0}}, {Key: "rider", Value: bson.A{50.0}}}},
		}))
		prizes, err := NewMongoStore(mt.DB).Prizes(context.Background(), officeID)
		if err != nil || len(prizes.Beautician) != 1 || len(prizes.Rider) != 1 {
			mt.Fatalf("prizes=%#v err=%v", prizes, err)
		}
	})
	mt.Run("missing office", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".offices", mtest.FirstBatch))
		prizes, err := NewMongoStore(mt.DB).Prizes(context.Background(), officeID)
		if err != nil || prizes.Beautician != nil {
			mt.Fatalf("prizes=%#v err=%v", prizes, err)
		}
	})
	mt.Run("prizes error", func(mt *mtest.T) {
		mt.AddMockResponses(leaderboardCommandError())
		if _, err := NewMongoStore(mt.DB).Prizes(context.Background(), officeID); err == nil {
			mt.Fatal("expected error")
		}
	})
}
