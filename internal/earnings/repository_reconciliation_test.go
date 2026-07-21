package earnings

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestRepositoryLoadReconciliationLedger(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("loads entries", func(mt *mtest.T) {
		entryID := primitive.NewObjectID()
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+ledgerCollection, mtest.FirstBatch, bson.D{{Key: "_id", Value: entryID}, {Key: "amount_paise", Value: int64(100)}}))
		entries, err := NewRepository(mt.DB).LoadReconciliationLedger(context.Background(), primitive.NewObjectID(), "2026-07-01", "2026-07-15")
		if err != nil || len(entries) != 1 || entries[0].ID != entryID {
			mt.Fatalf("entries=%+v err=%v", entries, err)
		}
	})
	mt.Run("returns find error", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 8, Message: "find"}))
		if _, err := NewRepository(mt.DB).LoadReconciliationLedger(context.Background(), primitive.NewObjectID(), "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected error")
		}
	})
	mt.Run("reconcile delegates to source loaders", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 8, Message: "orders"}))
		if _, err := NewRepository(mt.DB).Reconcile(context.Background(), primitive.NewObjectID(), "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected source error")
		}
	})
}
