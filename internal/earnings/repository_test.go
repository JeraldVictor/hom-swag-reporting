package earnings

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func commandOK() bson.D { return bson.D{{Key: "ok", Value: 1}} }
func commandError() bson.D {
	return mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 123, Message: "mock failure"})
}
func countResponse(namespace string, count int64) bson.D {
	return mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, bson.D{{Key: "n", Value: count}})
}

func TestRepositoryIndexes(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("success", func(mt *mtest.T) {
		mt.AddMockResponses(commandOK(), commandOK(), commandOK(), commandOK(), commandOK(), commandOK(), commandOK())
		if err := NewRepository(mt.DB).EnsureIndexes(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	mt.Run("ledger index error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if err := NewRepository(mt.DB).EnsureIndexes(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("period index error", func(mt *mtest.T) {
		mt.AddMockResponses(commandOK(), commandError())
		if err := NewRepository(mt.DB).EnsureIndexes(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("rebuild index error", func(mt *mtest.T) {
		mt.AddMockResponses(commandOK(), commandOK(), commandError())
		if err := NewRepository(mt.DB).EnsureIndexes(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("settlement index error", func(mt *mtest.T) {
		mt.AddMockResponses(commandOK(), commandOK(), commandOK(), commandError())
		if err := NewRepository(mt.DB).EnsureIndexes(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("mode index error", func(mt *mtest.T) {
		mt.AddMockResponses(commandOK(), commandOK(), commandOK(), commandOK(), commandError())
		if err := NewRepository(mt.DB).EnsureIndexes(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("order issue index error", func(mt *mtest.T) {
		mt.AddMockResponses(commandOK(), commandOK(), commandOK(), commandOK(), commandOK(), commandError())
		if err := NewRepository(mt.DB).EnsureIndexes(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("trip issue index error", func(mt *mtest.T) {
		mt.AddMockResponses(commandOK(), commandOK(), commandOK(), commandOK(), commandOK(), commandOK(), commandError())
		if err := NewRepository(mt.DB).EnsureIndexes(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRepositoryModeStateAndChanges(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID, actorID := primitive.NewObjectID(), primitive.NewObjectID()
	namespace := func(mt *mtest.T) string { return mt.DB.Name() + "." + modeCollection }

	mt.Run("missing state uses configured authoritative default", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, namespace(mt), mtest.FirstBatch))
		state, err := NewRepositoryWithMode(mt.DB, ModeAuthoritative).GetModeState(context.Background(), officeID)
		if err != nil || state.Mode != ModeAuthoritative || state.OfficeID != officeID || state.History == nil {
			mt.Fatalf("state=%#v err=%v", state, err)
		}
	})
	mt.Run("invalid default fails closed to shadow", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, namespace(mt), mtest.FirstBatch))
		mode, err := NewRepositoryWithMode(mt.DB, "invalid").Mode(context.Background(), officeID)
		if err != nil || mode != ModeShadow {
			mt.Fatalf("mode=%q err=%v", mode, err)
		}
	})
	mt.Run("stored unknown mode fails closed to shadow", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, namespace(mt), mtest.FirstBatch, bson.D{
			{Key: "office_id", Value: officeID}, {Key: "mode", Value: "unknown"},
		}))
		state, err := NewRepository(mt.DB).GetModeState(context.Background(), officeID)
		if err != nil || state.Mode != ModeShadow {
			mt.Fatalf("state=%#v err=%v", state, err)
		}
	})
	mt.Run("state lookup error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, err := NewRepository(mt.DB).GetModeState(context.Background(), officeID); err == nil {
			mt.Fatal("expected error")
		}
	})
	mt.Run("same mode is idempotent", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, namespace(mt), mtest.FirstBatch, bson.D{
			{Key: "office_id", Value: officeID}, {Key: "mode", Value: ModeShadow},
		}))
		state, changed, err := NewRepository(mt.DB).SetMode(context.Background(), officeID, ModeShadow, actorID, "same", "", "")
		if err != nil || changed || state.Mode != ModeShadow {
			mt.Fatalf("state=%#v changed=%t err=%v", state, changed, err)
		}
	})
	mt.Run("mode change is persisted with audit metadata", func(mt *mtest.T) {
		stored := bson.D{{Key: "office_id", Value: officeID}, {Key: "mode", Value: ModeAuthoritative}, {Key: "updated_by", Value: actorID}}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, namespace(mt), mtest.FirstBatch),
			mtest.CreateSuccessResponse(bson.E{Key: "value", Value: stored}),
		)
		state, changed, err := NewRepository(mt.DB).SetMode(context.Background(), officeID, ModeAuthoritative, actorID, "approved", "2026-07-01", "2026-07-31")
		if err != nil || !changed || state.Mode != ModeAuthoritative || state.UpdatedBy != actorID {
			mt.Fatalf("state=%#v changed=%t err=%v", state, changed, err)
		}
	})
	mt.Run("mode change lookup error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, _, err := NewRepository(mt.DB).SetMode(context.Background(), officeID, ModeAuthoritative, actorID, "x", "", ""); err == nil {
			mt.Fatal("expected error")
		}
	})
	mt.Run("mode update error", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, namespace(mt), mtest.FirstBatch), commandError())
		if _, _, err := NewRepository(mt.DB).SetMode(context.Background(), officeID, ModeAuthoritative, actorID, "x", "", ""); err == nil {
			mt.Fatal("expected error")
		}
	})
}

func TestRepositoryListEntries(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID := primitive.NewObjectID()
	workerID := primitive.NewObjectID()
	entryID := primitive.NewObjectID()
	filter := LedgerFilter{OfficeID: officeID.Hex(), WorkerID: workerID.Hex(), Component: string(ComponentPetrol), Bucket: string(BucketPetrol), Status: string(StatusOpen), StartDate: "2026-07-01", EndDate: "2026-07-31", Page: 1, Limit: 20}
	mt.Run("success", func(mt *mtest.T) {
		ns := mt.DB.Name() + "." + ledgerCollection
		doc := bson.D{{Key: "_id", Value: entryID}, {Key: "office_id", Value: officeID}, {Key: "worker_id", Value: workerID}, {Key: "component", Value: ComponentPetrol}, {Key: "settlement_bucket", Value: BucketPetrol}, {Key: "status", Value: StatusOpen}}
		mt.AddMockResponses(countResponse(ns, 1), mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, doc))
		entries, total, err := NewRepository(mt.DB).ListEntries(context.Background(), filter)
		if err != nil || total != 1 || len(entries) != 1 {
			t.Fatalf("entries=%v total=%d err=%v", entries, total, err)
		}
	})
	mt.Run("count error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, _, err := NewRepository(mt.DB).ListEntries(context.Background(), filter); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("find error", func(mt *mtest.T) {
		ns := mt.DB.Name() + "." + ledgerCollection
		mt.AddMockResponses(countResponse(ns, 1), commandError())
		if _, _, err := NewRepository(mt.DB).ListEntries(context.Background(), filter); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("decode error", func(mt *mtest.T) {
		ns := mt.DB.Name() + "." + ledgerCollection
		mt.AddMockResponses(countResponse(ns, 1), mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "office_id", Value: "invalid"}}))
		if _, _, err := NewRepository(mt.DB).ListEntries(context.Background(), filter); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRepositorySummary(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID := primitive.NewObjectID()
	mt.Run("success", func(mt *mtest.T) {
		ns := mt.DB.Name() + "." + ledgerCollection
		row := bson.D{{Key: "_id", Value: ComponentPetrol}, {Key: "amount_paise", Value: int64(100)}, {Key: "settled_amount_paise", Value: int64(20)}, {Key: "count", Value: int64(1)}}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, row))
		rows, err := NewRepository(mt.DB).Summary(context.Background(), officeID.Hex(), "2026-07-01", "2026-07-31")
		if err != nil || len(rows) != 1 || rows[0].AmountPaise != 100 {
			t.Fatalf("rows=%v err=%v", rows, err)
		}
	})
	mt.Run("aggregate error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, err := NewRepository(mt.DB).Summary(context.Background(), officeID.Hex(), "", ""); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("decode error", func(mt *mtest.T) {
		ns := mt.DB.Name() + "." + ledgerCollection
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "amount_paise", Value: "invalid"}}))
		if _, err := NewRepository(mt.DB).Summary(context.Background(), officeID.Hex(), "2026-07-01", ""); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRepositoryUpserts(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID, workerID := primitive.NewObjectID(), primitive.NewObjectID()
	mt.Run("adjustment success", func(mt *mtest.T) {
		entry := LedgerEntry{OfficeID: officeID, WorkerID: workerID, IdempotencyKey: "a"}
		storedID := primitive.NewObjectID()
		mt.AddMockResponses(bson.D{{Key: "ok", Value: 1}, {Key: "value", Value: bson.D{{Key: "_id", Value: storedID}, {Key: "office_id", Value: officeID}, {Key: "worker_id", Value: workerID}, {Key: "idempotency_key", Value: "a"}}}})
		stored, created, err := NewRepository(mt.DB).CreateAdjustment(context.Background(), entry)
		if err != nil || created || stored.ID != storedID {
			t.Fatalf("stored=%v created=%t err=%v", stored, created, err)
		}
	})
	mt.Run("adjustment error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, _, err := NewRepository(mt.DB).CreateAdjustment(context.Background(), LedgerEntry{OfficeID: officeID, IdempotencyKey: "a"}); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("period success and error", func(mt *mtest.T) {
		storedID := primitive.NewObjectID()
		doc := bson.D{{Key: "_id", Value: storedID}, {Key: "office_id", Value: officeID}, {Key: "kind", Value: "monthly"}}
		mt.AddMockResponses(bson.D{{Key: "ok", Value: 1}, {Key: "value", Value: doc}}, commandError())
		stored, created, err := NewRepository(mt.DB).ClosePeriod(context.Background(), Period{OfficeID: officeID, Kind: "monthly"})
		if err != nil || created || stored.ID != storedID {
			t.Fatalf("stored=%v created=%t err=%v", stored, created, err)
		}
		if _, _, err := NewRepository(mt.DB).ClosePeriod(context.Background(), Period{OfficeID: officeID}); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("rebuild success and error", func(mt *mtest.T) {
		storedID := primitive.NewObjectID()
		doc := bson.D{{Key: "_id", Value: storedID}, {Key: "office_id", Value: officeID}, {Key: "idempotency_key", Value: "r"}}
		mt.AddMockResponses(bson.D{{Key: "ok", Value: 1}, {Key: "value", Value: doc}}, commandError())
		stored, created, err := NewRepository(mt.DB).QueueRebuild(context.Background(), RebuildJob{OfficeID: officeID, IdempotencyKey: "r"})
		if err != nil || created || stored.ID != storedID {
			t.Fatalf("stored=%v created=%t err=%v", stored, created, err)
		}
		if _, _, err := NewRepository(mt.DB).QueueRebuild(context.Background(), RebuildJob{OfficeID: officeID}); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRepositoryExistenceAndOverlapQueries(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	id := primitive.NewObjectID()
	tests := []struct {
		name string
		call func(*Repository) (bool, error)
	}{
		{name: "office", call: func(r *Repository) (bool, error) { return r.OfficeExists(context.Background(), id) }},
		{name: "staff", call: func(r *Repository) (bool, error) { return r.ActiveStaffExists(context.Background(), id) }},
		{name: "beautician", call: func(r *Repository) (bool, error) {
			return r.WorkerBelongsToOffice(context.Background(), "beautician", id, id)
		}},
		{name: "rider", call: func(r *Repository) (bool, error) {
			return r.WorkerBelongsToOffice(context.Background(), "rider", id, id)
		}},
		{name: "date closed", call: func(r *Repository) (bool, error) { return r.IsDateClosed(context.Background(), id, "2026-07-01") }},
		{name: "closed overlap", call: func(r *Repository) (bool, error) {
			return r.HasClosedPeriodOverlap(context.Background(), id, "2026-07-01", "2026-07-31")
		}},
		{name: "active rebuild", call: func(r *Repository) (bool, error) {
			return r.HasActiveRebuildOverlap(context.Background(), id, "2026-07-01", "2026-07-31")
		}},
	}
	for _, test := range tests {
		mt.Run(test.name+" true", func(mt *mtest.T) {
			mt.AddMockResponses(countResponse(mt.DB.Name()+".x", 1))
			value, err := test.call(NewRepository(mt.DB))
			if err != nil || !value {
				t.Fatalf("value=%t err=%v", value, err)
			}
		})
		mt.Run(test.name+" error", func(mt *mtest.T) {
			mt.AddMockResponses(commandError())
			if _, err := test.call(NewRepository(mt.DB)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestBuildAllocations(t *testing.T) {
	negativeID, positiveID, secondID := primitive.NewObjectID(), primitive.NewObjectID(), primitive.NewObjectID()
	entries := []LedgerEntry{
		{ID: negativeID, AmountPaise: -100},
		{ID: positiveID, AmountPaise: 1000, SettledAmountPaise: 100},
		{ID: secondID, AmountPaise: 500},
	}
	allocations, err := buildAllocations(entries, 500)
	if err != nil || len(allocations) != 2 || allocations[0].AmountPaise != -100 || allocations[1].AmountPaise != 600 {
		t.Fatalf("allocations=%v err=%v", allocations, err)
	}
	if _, err := buildAllocations(nil, 1); !errors.Is(err, ErrNoPendingEarnings) {
		t.Fatalf("error=%v", err)
	}
	if _, err := buildAllocations(entries, 1401); !errors.Is(err, ErrSettlementExceedsPending) {
		t.Fatalf("error=%v", err)
	}
	allocations, err = buildAllocations([]LedgerEntry{{ID: positiveID, AmountPaise: 500}}, 500)
	if err != nil || len(allocations) != 1 || allocations[0].AmountPaise != 500 {
		t.Fatalf("exact allocations=%v err=%v", allocations, err)
	}
}

func TestRepositoryStatus(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID := primitive.NewObjectID()
	mt.Run("success", func(mt *mtest.T) {
		ns := mt.DB.Name() + "." + ledgerCollection
		mt.AddMockResponses(countResponse(ns, 4), countResponse(ns, 2), countResponse(mt.DB.Name()+"."+rebuildCollection, 1))
		status, err := NewRepository(mt.DB).Status(context.Background(), officeID.Hex())
		if err != nil || status["ledger_entries"] != int64(4) {
			t.Fatalf("status=%v err=%v", status, err)
		}
	})
	for index := 0; index < 3; index++ {
		mt.Run("error "+string(rune('1'+index)), func(mt *mtest.T) {
			responses := make([]bson.D, 0, index+1)
			for i := 0; i < index; i++ {
				responses = append(responses, countResponse(mt.DB.Name()+".x", 1))
			}
			responses = append(responses, commandError())
			mt.AddMockResponses(responses...)
			if _, err := NewRepository(mt.DB).Status(context.Background(), officeID.Hex()); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestRepositoryListRebuildsErrorsAndSuccess(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID := primitive.NewObjectID()
	filter := RebuildFilter{OfficeID: officeID.Hex(), Status: "failed", Page: 1, Limit: 10}
	mt.Run("success", func(mt *mtest.T) {
		ns := mt.DB.Name() + "." + rebuildCollection
		doc := bson.D{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "office_id", Value: officeID}, {Key: "status", Value: "failed"}}
		mt.AddMockResponses(countResponse(ns, 1), mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, doc))
		jobs, total, err := NewRepository(mt.DB).ListRebuilds(context.Background(), filter)
		if err != nil || total != 1 || len(jobs) != 1 {
			t.Fatalf("jobs=%v total=%d err=%v", jobs, total, err)
		}
	})
	mt.Run("count error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, _, err := NewRepository(mt.DB).ListRebuilds(context.Background(), filter); err == nil {
			t.Fatal("expected count error")
		}
	})
	mt.Run("find error", func(mt *mtest.T) {
		ns := mt.DB.Name() + "." + rebuildCollection
		mt.AddMockResponses(countResponse(ns, 1), commandError())
		if _, _, err := NewRepository(mt.DB).ListRebuilds(context.Background(), filter); err == nil {
			t.Fatal("expected find error")
		}
	})
	mt.Run("decode error", func(mt *mtest.T) {
		ns := mt.DB.Name() + "." + rebuildCollection
		mt.AddMockResponses(countResponse(ns, 1), mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "office_id", Value: "invalid"}}))
		if _, _, err := NewRepository(mt.DB).ListRebuilds(context.Background(), filter); err == nil {
			t.Fatal("expected decode error")
		}
	})
}

func TestRepositoryListSettlements(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID, workerID := primitive.NewObjectID(), primitive.NewObjectID()
	filter := SettlementFilter{OfficeID: officeID.Hex(), WorkerID: workerID.Hex(), Bucket: "petrol", StartDate: "2026-07-01", EndDate: "2026-07-31", Page: 1, Limit: 10}
	mt.Run("success", func(mt *mtest.T) {
		ns := mt.DB.Name() + "." + settlementCollection
		doc := bson.D{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "office_id", Value: officeID}, {Key: "worker_id", Value: workerID}, {Key: "amount_paise", Value: int64(100)}}
		mt.AddMockResponses(countResponse(ns, 1), mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, doc))
		rows, total, err := NewRepository(mt.DB).ListSettlements(context.Background(), filter)
		if err != nil || total != 1 || len(rows) != 1 {
			t.Fatalf("rows=%v total=%d err=%v", rows, total, err)
		}
	})
	mt.Run("count error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, _, err := NewRepository(mt.DB).ListSettlements(context.Background(), filter); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("find error", func(mt *mtest.T) {
		mt.AddMockResponses(countResponse(mt.DB.Name()+".x", 1), commandError())
		if _, _, err := NewRepository(mt.DB).ListSettlements(context.Background(), filter); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("decode error", func(mt *mtest.T) {
		ns := mt.DB.Name() + "." + settlementCollection
		mt.AddMockResponses(countResponse(ns, 1), mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "office_id", Value: "bad"}}))
		if _, _, err := NewRepository(mt.DB).ListSettlements(context.Background(), filter); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRepositoryAllocateSettlement(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID, workerID, entryID := primitive.NewObjectID(), primitive.NewObjectID(), primitive.NewObjectID()
	input := Settlement{OfficeID: officeID, WorkerID: workerID, WorkerType: "beautician", Bucket: BucketCommission, StartDate: "2026-07-01", EndDate: "2026-07-31", AmountPaise: 500, IdempotencyKey: "s1"}
	mt.Run("idempotent replay", func(mt *mtest.T) {
		ns := mt.DB.Name() + "." + settlementCollection
		doc := bson.D{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "office_id", Value: officeID}, {Key: "worker_id", Value: workerID}, {Key: "amount_paise", Value: int64(500)}, {Key: "idempotency_key", Value: "s1"}}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, doc), commandOK())
		stored, created, err := NewRepository(mt.DB).AllocateSettlement(context.Background(), input)
		if err != nil || created || stored.AmountPaise != 500 {
			t.Fatalf("stored=%v created=%t err=%v", stored, created, err)
		}
	})
	mt.Run("created", func(mt *mtest.T) {
		settlementNS := mt.DB.Name() + "." + settlementCollection
		ledgerNS := mt.DB.Name() + "." + ledgerCollection
		entry := bson.D{{Key: "_id", Value: entryID}, {Key: "office_id", Value: officeID}, {Key: "worker_id", Value: workerID}, {Key: "worker_type", Value: "beautician"}, {Key: "settlement_bucket", Value: BucketCommission}, {Key: "amount_paise", Value: int64(1000)}, {Key: "settled_amount_paise", Value: int64(0)}, {Key: "status", Value: StatusOpen}}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, settlementNS, mtest.FirstBatch),
			mtest.CreateCursorResponse(0, ledgerNS, mtest.FirstBatch, entry),
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}, {Key: "nModified", Value: 1}},
			commandOK(), commandOK(),
		)
		stored, created, err := NewRepository(mt.DB).AllocateSettlement(context.Background(), input)
		if err != nil || !created || len(stored.Allocations) != 1 || stored.Allocations[0].AmountPaise != 500 {
			t.Fatalf("stored=%v created=%t err=%v", stored, created, err)
		}
	})
	mt.Run("created on explicitly enabled standalone development database", func(mt *mtest.T) {
		settlementNS := mt.DB.Name() + "." + settlementCollection
		ledgerNS := mt.DB.Name() + "." + ledgerCollection
		entry := bson.D{{Key: "_id", Value: entryID}, {Key: "office_id", Value: officeID}, {Key: "worker_id", Value: workerID}, {Key: "worker_type", Value: "beautician"}, {Key: "settlement_bucket", Value: BucketCommission}, {Key: "amount_paise", Value: int64(1000)}, {Key: "settled_amount_paise", Value: int64(0)}, {Key: "status", Value: StatusOpen}}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, settlementNS, mtest.FirstBatch),
			mtest.CreateCursorResponse(0, ledgerNS, mtest.FirstBatch, entry),
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}, {Key: "nModified", Value: 1}},
			commandOK(),
		)
		repo := NewRepositoryWithOptions(mt.DB, RepositoryOptions{
			DefaultMode:                 ModeShadow,
			AllowNonTransactionalWrites: true,
		})
		stored, created, err := repo.AllocateSettlement(context.Background(), input)
		if err != nil || !created || len(stored.Allocations) != 1 || stored.Allocations[0].AmountPaise != 500 {
			mt.Fatalf("stored=%v created=%t err=%v", stored, created, err)
		}
	})
	mt.Run("selected entry unavailable", func(mt *mtest.T) {
		settlementNS := mt.DB.Name() + "." + settlementCollection
		ledgerNS := mt.DB.Name() + "." + ledgerCollection
		selected := input
		selected.RequestedEntryIDs = []primitive.ObjectID{entryID, primitive.NewObjectID()}
		entry := bson.D{{Key: "_id", Value: entryID}, {Key: "office_id", Value: officeID}, {Key: "worker_id", Value: workerID}, {Key: "worker_type", Value: "beautician"}, {Key: "settlement_bucket", Value: BucketCommission}, {Key: "amount_paise", Value: int64(1000)}, {Key: "settled_amount_paise", Value: int64(0)}, {Key: "status", Value: StatusOpen}}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, settlementNS, mtest.FirstBatch),
			mtest.CreateCursorResponse(0, ledgerNS, mtest.FirstBatch, entry),
			commandOK(),
		)
		if _, _, err := NewRepository(mt.DB).AllocateSettlement(context.Background(), selected); !errors.Is(err, ErrSettlementEntriesUnavailable) {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestRepositoryAllocateSettlementFailures(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID, workerID, entryID := primitive.NewObjectID(), primitive.NewObjectID(), primitive.NewObjectID()
	input := Settlement{OfficeID: officeID, WorkerID: workerID, WorkerType: "beautician", Bucket: BucketCommission, StartDate: "2026-07-01", EndDate: "2026-07-31", AmountPaise: 500, IdempotencyKey: "errors"}
	entry := bson.D{{Key: "_id", Value: entryID}, {Key: "office_id", Value: officeID}, {Key: "worker_id", Value: workerID}, {Key: "worker_type", Value: "beautician"}, {Key: "settlement_bucket", Value: BucketCommission}, {Key: "amount_paise", Value: int64(500)}, {Key: "settled_amount_paise", Value: int64(0)}, {Key: "status", Value: StatusOpen}}

	mt.Run("session error", func(mt *mtest.T) {
		r := NewRepository(mt.DB)
		r.startSession = func(...*options.SessionOptions) (mongo.Session, error) { return nil, errors.New("session unavailable") }
		if _, _, err := r.AllocateSettlement(context.Background(), input); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("idempotency lookup error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError(), commandOK())
		if _, _, err := NewRepository(mt.DB).AllocateSettlement(context.Background(), input); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("ledger find error", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+settlementCollection, mtest.FirstBatch), commandError(), commandOK())
		if _, _, err := NewRepository(mt.DB).AllocateSettlement(context.Background(), input); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("ledger decode error", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+settlementCollection, mtest.FirstBatch), mtest.CreateCursorResponse(0, mt.DB.Name()+"."+ledgerCollection, mtest.FirstBatch, bson.D{{Key: "_id", Value: "bad"}}), commandOK())
		if _, _, err := NewRepository(mt.DB).AllocateSettlement(context.Background(), input); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("allocation error", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+settlementCollection, mtest.FirstBatch), mtest.CreateCursorResponse(0, mt.DB.Name()+"."+ledgerCollection, mtest.FirstBatch), commandOK())
		if _, _, err := NewRepository(mt.DB).AllocateSettlement(context.Background(), input); !errors.Is(err, ErrNoPendingEarnings) {
			t.Fatalf("err=%v", err)
		}
	})
	mt.Run("ledger update error", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+settlementCollection, mtest.FirstBatch), mtest.CreateCursorResponse(0, mt.DB.Name()+"."+ledgerCollection, mtest.FirstBatch, entry), commandError(), commandOK())
		if _, _, err := NewRepository(mt.DB).AllocateSettlement(context.Background(), input); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("ledger concurrently changed", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+settlementCollection, mtest.FirstBatch), mtest.CreateCursorResponse(0, mt.DB.Name()+"."+ledgerCollection, mtest.FirstBatch, entry), bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 0}, {Key: "nModified", Value: 0}}, commandOK())
		if _, _, err := NewRepository(mt.DB).AllocateSettlement(context.Background(), input); err == nil {
			t.Fatal("expected error")
		}
	})
	mt.Run("insert error after fully settled", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+settlementCollection, mtest.FirstBatch), mtest.CreateCursorResponse(0, mt.DB.Name()+"."+ledgerCollection, mtest.FirstBatch, entry), bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}, {Key: "nModified", Value: 1}}, commandError(), commandOK())
		if _, _, err := NewRepository(mt.DB).AllocateSettlement(context.Background(), input); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRepositoryFindSettlement(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID := primitive.NewObjectID()
	mt.Run("found", func(mt *mtest.T) {
		ns := mt.DB.Name() + "." + settlementCollection
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "office_id", Value: officeID}}))
		_, found, err := NewRepository(mt.DB).FindSettlement(context.Background(), officeID, "s1")
		if err != nil || !found {
			t.Fatalf("found=%t err=%v", found, err)
		}
	})
	mt.Run("missing", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+settlementCollection, mtest.FirstBatch))
		_, found, err := NewRepository(mt.DB).FindSettlement(context.Background(), officeID, "s1")
		if err != nil || found {
			t.Fatalf("found=%t err=%v", found, err)
		}
	})
	mt.Run("error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, _, err := NewRepository(mt.DB).FindSettlement(context.Background(), officeID, "s1"); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRepositoryUpdateSettlementMetadata(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID, settlementID, actorID := primitive.NewObjectID(), primitive.NewObjectID(), primitive.NewObjectID()
	mt.Run("updates metadata and retains a revision", func(mt *mtest.T) {
		ns := mt.DB.Name() + "." + settlementCollection
		doc := bson.D{
			{Key: "_id", Value: settlementID}, {Key: "office_id", Value: officeID},
			{Key: "amount_paise", Value: int64(500)}, {Key: "payment_method", Value: "cash"},
			{Key: "reference", Value: "old"}, {Key: "remarks", Value: "before"},
			{Key: "allocations", Value: bson.A{}},
		}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, doc),
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}, {Key: "nModified", Value: 1}},
			commandOK(),
		)
		updated, err := NewRepository(mt.DB).UpdateSettlement(context.Background(), officeID, settlementID, SettlementUpdate{
			AmountPaise: 500, PaymentMethod: "upi", Reference: "new", Remarks: "after", UpdatedBy: actorID,
		})
		if err != nil || updated.PaymentMethod != "upi" || updated.Reference != "new" || len(updated.RevisionHistory) != 1 {
			mt.Fatalf("updated=%#v err=%v", updated, err)
		}
		if updated.RevisionHistory[0].PaymentMethod != "cash" || updated.RevisionHistory[0].EditedBy != actorID {
			mt.Fatalf("revision=%#v", updated.RevisionHistory[0])
		}
	})
	mt.Run("reallocates a corrected amount", func(mt *mtest.T) {
		ns, ledgerNS := mt.DB.Name()+"."+settlementCollection, mt.DB.Name()+"."+ledgerCollection
		entryID := primitive.NewObjectID()
		doc := bson.D{
			{Key: "_id", Value: settlementID}, {Key: "office_id", Value: officeID},
			{Key: "worker_id", Value: primitive.NewObjectID()}, {Key: "worker_type", Value: "beautician"},
			{Key: "bucket", Value: BucketCommission}, {Key: "start_date", Value: "2026-07-01"},
			{Key: "end_date", Value: "2026-07-31"}, {Key: "amount_paise", Value: int64(500)},
			{Key: "allocations", Value: bson.A{bson.D{{Key: "entry_id", Value: entryID}, {Key: "amount_paise", Value: int64(500)}}}},
		}
		settledEntry := bson.D{
			{Key: "_id", Value: entryID}, {Key: "office_id", Value: officeID},
			{Key: "amount_paise", Value: int64(500)}, {Key: "settled_amount_paise", Value: int64(500)},
			{Key: "status", Value: StatusSettled},
		}
		openEntry := bson.D{
			{Key: "_id", Value: entryID}, {Key: "office_id", Value: officeID},
			{Key: "amount_paise", Value: int64(500)}, {Key: "settled_amount_paise", Value: int64(0)},
			{Key: "status", Value: StatusOpen},
		}
		updatedOK := bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}, {Key: "nModified", Value: 1}}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, doc),
			mtest.CreateCursorResponse(0, ledgerNS, mtest.FirstBatch, settledEntry),
			updatedOK,
			mtest.CreateCursorResponse(0, ledgerNS, mtest.FirstBatch, openEntry),
			updatedOK, updatedOK, commandOK(),
		)
		updated, err := NewRepository(mt.DB).UpdateSettlement(context.Background(), officeID, settlementID, SettlementUpdate{
			AmountPaise: 300, PaymentMethod: "cash", UpdatedBy: actorID,
		})
		if err != nil || updated.AmountPaise != 300 || len(updated.Allocations) != 1 || updated.Allocations[0].AmountPaise != 300 {
			mt.Fatalf("updated=%#v err=%v", updated, err)
		}
	})
}
