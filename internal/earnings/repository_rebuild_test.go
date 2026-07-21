package earnings

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func findAndModifyResponse(value interface{}) bson.D {
	return bson.D{{Key: "ok", Value: 1}, {Key: "value", Value: value}}
}

func TestRepositoryLoadTripSourcesUsesPayableEligibilityFilter(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	office := primitive.NewObjectID()
	mt.Run("completed trips only", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".trips", mtest.FirstBatch))
		if _, err := NewRepository(mt.DB).LoadTripSources(context.Background(), office, "2026-07-01", "2026-07-31"); err != nil {
			t.Fatal(err)
		}
		event := mt.GetStartedEvent()
		if event == nil {
			t.Fatal("expected Mongo find command")
		}
		command := event.Command.String()
		for _, required := range []string{"kanban_state", "trip_completed", "fare_calculation_pending", "status", "completed", "is_deleted"} {
			if !strings.Contains(command, required) {
				t.Fatalf("find filter does not contain %q: %s", required, command)
			}
		}
	})
}

func TestRepositoryClaimNextRebuild(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	id, office := primitive.NewObjectID(), primitive.NewObjectID()
	mt.Run("success", func(mt *mtest.T) {
		mt.AddMockResponses(findAndModifyResponse(bson.D{{Key: "_id", Value: id}, {Key: "office_id", Value: office}, {Key: "status", Value: "running"}}))
		job, err := NewRepository(mt.DB).ClaimNextRebuild(context.Background())
		if err != nil || job.ID != id || job.Status != "running" {
			t.Fatalf("job=%+v err=%v", job, err)
		}
	})
	mt.Run("empty", func(mt *mtest.T) {
		mt.AddMockResponses(findAndModifyResponse(nil))
		_, err := NewRepository(mt.DB).ClaimNextRebuild(context.Background())
		if !errors.Is(err, ErrNoQueuedRebuild) {
			t.Fatalf("err=%v", err)
		}
	})
	mt.Run("error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, err := NewRepository(mt.DB).ClaimNextRebuild(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRepositoryLoadRawSources(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	office, worker := primitive.NewObjectID(), primitive.NewObjectID()
	tests := []struct {
		name, collection string
		doc              bson.D
		call             func(*Repository) (int, error)
	}{
		{"orders", "orders", bson.D{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "office_id", Value: office}, {Key: "beautician_id", Value: worker}}, func(r *Repository) (int, error) {
			rows, err := r.LoadOrderSources(context.Background(), office, "2026-07-01", "2026-07-31")
			return len(rows), err
		}},
		{"trips", "trips", bson.D{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "office_id", Value: office}, {Key: "rider_id", Value: worker}}, func(r *Repository) (int, error) {
			rows, err := r.LoadTripSources(context.Background(), office, "2026-07-01", "2026-07-31")
			return len(rows), err
		}},
		{"targets", "beauticians", bson.D{{Key: "_id", Value: worker}, {Key: "monthly_target1", Value: 100.0}, {Key: "monthly_target2", Value: 200.0}}, func(r *Repository) (int, error) {
			rows, err := r.LoadWorkerTargets(context.Background(), office)
			return len(rows), err
		}},
	}
	for _, test := range tests {
		test := test
		mt.Run(test.name+" success", func(mt *mtest.T) {
			ns := mt.DB.Name() + "." + test.collection
			mt.AddMockResponses(mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, test.doc))
			count, err := test.call(NewRepository(mt.DB))
			if err != nil || count != 1 {
				t.Fatalf("count=%d err=%v", count, err)
			}
		})
		mt.Run(test.name+" find error", func(mt *mtest.T) {
			mt.AddMockResponses(commandError())
			if _, err := test.call(NewRepository(mt.DB)); err == nil {
				t.Fatal("expected error")
			}
		})
		mt.Run(test.name+" decode error", func(mt *mtest.T) {
			ns := mt.DB.Name() + "." + test.collection
			mt.AddMockResponses(mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "_id", Value: "bad"}}))
			if _, err := test.call(NewRepository(mt.DB)); err == nil {
				t.Fatal("expected decode error")
			}
		})
	}
}

func TestRepositoryLoadsExactSourceByID(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	id, office := primitive.NewObjectID(), primitive.NewObjectID()
	captured := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	mt.Run("order", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".orders", mtest.FirstBatch, bson.D{
			{Key: "_id", Value: id}, {Key: "office_id", Value: office}, {Key: "commission_snapshot", Value: bson.D{{Key: "captured_at", Value: captured}}},
		}))
		row, err := NewRepository(mt.DB).LoadOrderSource(context.Background(), id)
		if err != nil || row.ID != id || row.Snapshot == nil || !row.Snapshot.CapturedAt.Equal(captured) {
			t.Fatalf("row=%+v err=%v", row, err)
		}
		if event := mt.GetStartedEvent(); event == nil || !strings.Contains(event.Command.String(), id.Hex()) {
			t.Fatalf("exact id missing from query: %+v", event)
		}
	})
	mt.Run("trip", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".trips", mtest.FirstBatch, bson.D{
			{Key: "_id", Value: id}, {Key: "office_id", Value: office}, {Key: "payable_snapshot", Value: bson.D{{Key: "captured_at", Value: captured}}},
		}))
		row, err := NewRepository(mt.DB).LoadTripSource(context.Background(), id)
		if err != nil || row.ID != id || row.Snapshot == nil || !row.Snapshot.CapturedAt.Equal(captured) {
			t.Fatalf("row=%+v err=%v", row, err)
		}
	})
	for _, call := range []func(*Repository) error{
		func(r *Repository) error { _, err := r.LoadOrderSource(context.Background(), id); return err },
		func(r *Repository) error { _, err := r.LoadTripSource(context.Background(), id); return err },
	} {
		mt.Run("error", func(mt *mtest.T) {
			mt.AddMockResponses(commandError())
			if err := call(NewRepository(mt.DB)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestRepositoryLoadOrderSourcesRequiresCompletedStatus(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	office := primitive.NewObjectID()
	mt.Run("completed-only filter", func(mt *mtest.T) {
		ns := mt.DB.Name() + ".orders"
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns, mtest.FirstBatch))
		if _, err := NewRepository(mt.DB).LoadOrderSources(context.Background(), office, "2026-07-01", "2026-07-31"); err != nil {
			t.Fatal(err)
		}
		started := mt.GetStartedEvent()
		if started == nil {
			t.Fatal("missing find command")
		}
		filter, ok := started.Command.Lookup("filter").DocumentOK()
		if !ok {
			t.Fatalf("find command has no filter: %v", started.Command)
		}
		status, ok := filter.Lookup("status").StringValueOK()
		if !ok || status != "completed" {
			t.Fatalf("order source filter can admit non-completed statuses: %v", filter)
		}
	})
}

func TestRepositoryLoadOrderSourcesIgnoresBSONServiceDate(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	office, worker := primitive.NewObjectID(), primitive.NewObjectID()
	mt.Run("booking date is the sole period date", func(mt *mtest.T) {
		ns := mt.DB.Name() + ".orders"
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{
			{Key: "_id", Value: primitive.NewObjectID()},
			{Key: "office_id", Value: office},
			{Key: "beautician_id", Value: worker},
			{Key: "status", Value: "completed"},
			{Key: "booking_info", Value: bson.D{{Key: "date", Value: "2026-07-15"}}},
			// This is the production shape that previously failed decoding into a string.
			{Key: "service_date", Value: time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)},
		}))

		rows, err := NewRepository(mt.DB).LoadOrderSources(context.Background(), office, "2026-07-01", "2026-07-31")
		if err != nil || len(rows) != 1 || rows[0].BookingInfo.Date != "2026-07-15" {
			t.Fatalf("rows=%+v err=%v", rows, err)
		}

		started := mt.GetStartedEvent()
		if started == nil {
			t.Fatal("missing find command")
		}
		filter, ok := started.Command.Lookup("filter").DocumentOK()
		if !ok {
			t.Fatalf("find command has no filter: %v", started.Command)
		}
		if filter.Lookup("service_date").Type != 0 {
			t.Fatalf("order source filter must not use service_date: %v", filter)
		}
		if filter.Lookup("booking_info.date").Type == 0 {
			t.Fatalf("order source filter must use booking_info.date: %v", filter)
		}
		projection, ok := started.Command.Lookup("projection").DocumentOK()
		if !ok {
			t.Fatalf("find command has no projection: %v", started.Command)
		}
		if projection.Lookup("service_date").Type != 0 {
			t.Fatalf("order source projection must not decode service_date: %v", projection)
		}
		if projection.Lookup("booking_info.date").Type == 0 {
			t.Fatalf("order source projection must include booking_info.date: %v", projection)
		}
	})
}

func TestRepositoryLoadLeaderboardSources(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	office, worker := primitive.NewObjectID(), primitive.NewObjectID()
	mt.Run("beautician success", func(mt *mtest.T) {
		ns := mt.DB.Name() + ".leaderboards"
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "_id", Value: worker}, {Key: "revenue", Value: 10.0}, {Key: "order_count", Value: 1}}))
		rows, err := NewRepository(mt.DB).LoadBeauticianLeaderboardSources(context.Background(), office, "2026-07-01", "2026-07-31")
		if err != nil || len(rows) != 1 {
			t.Fatalf("rows=%v err=%v", rows, err)
		}
	})
	mt.Run("beautician invalid dates", func(mt *mtest.T) {
		r := NewRepository(mt.DB)
		if _, err := r.LoadBeauticianLeaderboardSources(context.Background(), office, "bad", "2026-07-31"); err == nil {
			t.Fatal("expected start error")
		}
		if _, err := r.LoadBeauticianLeaderboardSources(context.Background(), office, "2026-07-01", "bad"); err == nil {
			t.Fatal("expected end error")
		}
	})
	mt.Run("beautician aggregate error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, err := NewRepository(mt.DB).LoadBeauticianLeaderboardSources(context.Background(), office, "2026-07-01", "2026-07-31"); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("beautician decode error", func(mt *mtest.T) {
		ns := mt.DB.Name() + ".leaderboards"
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "_id", Value: "bad"}}))
		if _, err := NewRepository(mt.DB).LoadBeauticianLeaderboardSources(context.Background(), office, "2026-07-01", "2026-07-31"); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("rider success", func(mt *mtest.T) {
		ns := mt.DB.Name() + ".trips"
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "_id", Value: worker}, {Key: "worker_type", Value: "rider"}, {Key: "trip_count", Value: 1}, {Key: "total_distance_km", Value: 2.0}}))
		rows, err := NewRepository(mt.DB).LoadRiderLeaderboardSources(context.Background(), office, "2026-07-01", "2026-07-31")
		if err != nil || len(rows) != 1 {
			t.Fatalf("rows=%v err=%v", rows, err)
		}
	})
	mt.Run("rider aggregate error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, err := NewRepository(mt.DB).LoadRiderLeaderboardSources(context.Background(), office, "2026-07-01", "2026-07-31"); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("rider decode error", func(mt *mtest.T) {
		ns := mt.DB.Name() + ".trips"
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "_id", Value: "bad"}}))
		if _, err := NewRepository(mt.DB).LoadRiderLeaderboardSources(context.Background(), office, "2026-07-01", "2026-07-31"); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRepositoryLeaderboardPrizesAndSourceWrites(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	office, entryID := primitive.NewObjectID(), primitive.NewObjectID()
	mt.Run("prizes success", func(mt *mtest.T) {
		doc := bson.D{{Key: "_id", Value: office}, {Key: "leaderboard_prizes", Value: bson.D{{Key: "beutician", Value: bson.A{10.0}}, {Key: "rider", Value: bson.A{5.0}}}}}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".offices", mtest.FirstBatch, doc))
		prizes, err := NewRepository(mt.DB).LoadLeaderboardPrizes(context.Background(), office)
		if err != nil || len(prizes.Beautician) != 1 || len(prizes.Rider) != 1 {
			t.Fatalf("prizes=%v err=%v", prizes, err)
		}
	})
	mt.Run("prizes missing", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".offices", mtest.FirstBatch))
		prizes, err := NewRepository(mt.DB).LoadLeaderboardPrizes(context.Background(), office)
		if err != nil || len(prizes.Rider) != 0 {
			t.Fatalf("prizes=%v err=%v", prizes, err)
		}
	})
	mt.Run("prizes error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, err := NewRepository(mt.DB).LoadLeaderboardPrizes(context.Background(), office); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("source existing", func(mt *mtest.T) {
		doc := bson.D{{Key: "_id", Value: entryID}, {Key: "office_id", Value: office}, {Key: "idempotency_key", Value: "source"}}
		mt.AddMockResponses(findAndModifyResponse(doc))
		stored, created, err := NewRepository(mt.DB).PutSourceEntry(context.Background(), LedgerEntry{OfficeID: office, IdempotencyKey: "source"})
		if err != nil || created || stored.ID != entryID {
			t.Fatalf("stored=%v created=%v err=%v", stored, created, err)
		}
	})
	mt.Run("repairs an open unsettled source", func(mt *mtest.T) {
		worker := primitive.NewObjectID()
		doc := bson.D{
			{Key: "_id", Value: entryID}, {Key: "office_id", Value: office}, {Key: "worker_id", Value: worker},
			{Key: "worker_type", Value: "rider"}, {Key: "service_date_key", Value: "2026-07-21"},
			{Key: "component", Value: ComponentTripCommission}, {Key: "settlement_bucket", Value: BucketCommission},
			{Key: "amount_paise", Value: int64(1356)}, {Key: "settled_amount_paise", Value: int64(0)},
			{Key: "status", Value: StatusOpen}, {Key: "source_type", Value: "trips"},
			{Key: "calculation_version", Value: 1}, {Key: "idempotency_key", Value: "source"},
		}
		mt.AddMockResponses(
			findAndModifyResponse(doc),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}, bson.E{Key: "nModified", Value: 1}),
		)
		stored, created, err := NewRepository(mt.DB).PutSourceEntry(context.Background(), LedgerEntry{
			OfficeID: office, WorkerID: worker, WorkerType: "rider", ServiceDateKey: "2026-07-21",
			Component: ComponentTripCommission, SettlementBucket: BucketCommission, AmountPaise: 2711,
			Status: StatusOpen, SourceType: "trips", CalculationVersion: 1, IdempotencyKey: "source",
		})
		if err != nil || created || stored.AmountPaise != 2711 || stored.ID != entryID {
			t.Fatalf("stored=%+v created=%v err=%v", stored, created, err)
		}
	})
	mt.Run("does not rewrite a settled source", func(mt *mtest.T) {
		doc := bson.D{
			{Key: "_id", Value: entryID}, {Key: "office_id", Value: office}, {Key: "idempotency_key", Value: "source"},
			{Key: "amount_paise", Value: int64(1356)}, {Key: "settled_amount_paise", Value: int64(1356)}, {Key: "status", Value: StatusSettled},
		}
		mt.AddMockResponses(findAndModifyResponse(doc))
		stored, created, err := NewRepository(mt.DB).PutSourceEntry(context.Background(), LedgerEntry{OfficeID: office, AmountPaise: 2711, IdempotencyKey: "source"})
		if err != nil || created || stored.AmountPaise != 1356 || stored.Status != StatusSettled {
			t.Fatalf("stored=%+v created=%v err=%v", stored, created, err)
		}
	})
	mt.Run("source error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, _, err := NewRepository(mt.DB).PutSourceEntry(context.Background(), LedgerEntry{OfficeID: office}); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("finish success", func(mt *mtest.T) {
		mt.AddMockResponses(commandOK())
		if err := NewRepository(mt.DB).FinishRebuild(context.Background(), primitive.NewObjectID(), "completed", RebuildStats{Scanned: 1}, ""); err != nil {
			t.Fatal(err)
		}
	})
	mt.Run("finish error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if err := NewRepository(mt.DB).FinishRebuild(context.Background(), primitive.NewObjectID(), "failed", RebuildStats{}, "x"); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRepositoryLoadTarget2Bonus(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	office := primitive.NewObjectID()
	mt.Run("success", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".offices", mtest.FirstBatch,
			bson.D{{Key: "_id", Value: office}, {Key: "monthly_target2_bonus", Value: 75.0}}))
		bonus, err := NewRepository(mt.DB).LoadTarget2Bonus(context.Background(), office)
		if err != nil || bonus != 75 {
			t.Fatalf("bonus=%v err=%v", bonus, err)
		}
	})
	mt.Run("missing office defaults to zero", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".offices", mtest.FirstBatch))
		bonus, err := NewRepository(mt.DB).LoadTarget2Bonus(context.Background(), office)
		if err != nil || bonus != 0 {
			t.Fatalf("bonus=%v err=%v", bonus, err)
		}
	})
	mt.Run("error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, err := NewRepository(mt.DB).LoadTarget2Bonus(context.Background(), office); err == nil {
			t.Fatal("expected error")
		}
	})
}
