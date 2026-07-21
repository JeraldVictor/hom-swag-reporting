package earnings

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestReportDateBoundsAndFallbackReason(t *testing.T) {
	start, end, err := reportDateBounds("2026-07-01", "2026-07-31")
	if err != nil || start.Format("2006-01-02") != "2026-07-01" || end.Format("2006-01-02") != "2026-07-31" || end.Hour() != 23 {
		t.Fatalf("start=%v end=%v err=%v", start, end, err)
	}
	if _, _, err := reportDateBounds("bad", "2026-07-31"); err == nil {
		t.Fatal("expected invalid start error")
	}
	if _, _, err := reportDateBounds("2026-07-01", "bad"); err == nil {
		t.Fatal("expected invalid end error")
	}
	if got := firstNonEmpty(" ", "petrol_adjustment"); got != "petrol_adjustment" {
		t.Fatalf("reason=%q", got)
	}
	if got := firstNonEmpty("", " "); got != "Adjustment" {
		t.Fatalf("fallback=%q", got)
	}
}

func TestRepositoryReportRows(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID, workerID := primitive.NewObjectID(), primitive.NewObjectID()
	now := time.Now().UTC()

	mt.Run("trips success and decode error", func(mt *mtest.T) {
		ns := mt.DB.Name() + ".trips"
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "trip_number", Value: "T-1"}}),
			mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "_id", Value: "invalid"}}),
		)
		rows, err := NewRepository(mt.DB).loadReportTrips(context.Background(), officeID, workerID, "2026-07-01", "2026-07-31")
		if err != nil || len(rows) != 1 || rows[0].TripNumber != "T-1" {
			mt.Fatalf("rows=%v err=%v", rows, err)
		}
		if _, err := NewRepository(mt.DB).loadReportTrips(context.Background(), officeID, workerID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected decode error")
		}
	})
	mt.Run("trips aggregate error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, err := NewRepository(mt.DB).loadReportTrips(context.Background(), officeID, workerID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected aggregate error")
		}
	})
	mt.Run("orders success and decode error", func(mt *mtest.T) {
		ns := mt.DB.Name() + ".orders"
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "order_number", Value: "O-1"}}),
			mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "_id", Value: "invalid"}}),
		)
		rows, err := NewRepository(mt.DB).loadReportOrders(context.Background(), officeID, workerID, "2026-07-01", "2026-07-31")
		if err != nil || len(rows) != 1 || rows[0].OrderNumber != "O-1" {
			mt.Fatalf("rows=%v err=%v", rows, err)
		}
		if _, err := NewRepository(mt.DB).loadReportOrders(context.Background(), officeID, workerID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected decode error")
		}
	})
	mt.Run("orders aggregate error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, err := NewRepository(mt.DB).loadReportOrders(context.Background(), officeID, workerID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected aggregate error")
		}
	})
	mt.Run("payouts merge current and legacy", func(mt *mtest.T) {
		settlementNS := mt.DB.Name() + "." + settlementCollection
		legacyNS := mt.DB.Name() + ".payouts"
		mt.AddMockResponses(
			countResponse(settlementNS, 1),
			mtest.CreateCursorResponse(0, settlementNS, mtest.FirstBatch, bson.D{
				{Key: "_id", Value: primitive.NewObjectID()}, {Key: "bucket", Value: BucketCommission}, {Key: "amount_paise", Value: int64(2500)},
				{Key: "start_date", Value: "2026-07-01"}, {Key: "end_date", Value: "2026-07-31"}, {Key: "created_at", Value: now.Add(-time.Hour)},
			}),
			mtest.CreateCursorResponse(0, legacyNS, mtest.FirstBatch, bson.D{
				{Key: "_id", Value: primitive.NewObjectID()}, {Key: "payout_type", Value: BucketPetrol}, {Key: "amount", Value: 10.5},
				{Key: "period_start", Value: now.AddDate(0, 0, -2)}, {Key: "period_end", Value: now}, {Key: "payout_date", Value: now},
			}),
		)
		rows, err := NewRepository(mt.DB).loadReportPayouts(context.Background(), officeID, workerID, "2026-07-01", "2026-07-31")
		if err != nil || len(rows) != 2 || rows[0].PayoutType != BucketPetrol || rows[1].Amount != 25 {
			mt.Fatalf("rows=%+v err=%v", rows, err)
		}
	})
	mt.Run("payout errors", func(mt *mtest.T) {
		repo := NewRepository(mt.DB)
		mt.AddMockResponses(commandError())
		if _, err := repo.loadReportPayouts(context.Background(), officeID, workerID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected settlement error")
		}
		settlementNS := mt.DB.Name() + "." + settlementCollection
		mt.AddMockResponses(countResponse(settlementNS, 0), mtest.CreateCursorResponse(0, settlementNS, mtest.FirstBatch))
		if _, err := repo.loadReportPayouts(context.Background(), officeID, workerID, "bad", "2026-07-31"); err == nil {
			mt.Fatal("expected date error")
		}
		mt.AddMockResponses(countResponse(settlementNS, 0), mtest.CreateCursorResponse(0, settlementNS, mtest.FirstBatch), commandError())
		if _, err := repo.loadReportPayouts(context.Background(), officeID, workerID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected legacy find error")
		}
		legacyNS := mt.DB.Name() + ".payouts"
		mt.AddMockResponses(countResponse(settlementNS, 0), mtest.CreateCursorResponse(0, settlementNS, mtest.FirstBatch), mtest.CreateCursorResponse(0, legacyNS, mtest.FirstBatch, bson.D{{Key: "_id", Value: "invalid"}}))
		if _, err := repo.loadReportPayouts(context.Background(), officeID, workerID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected legacy decode error")
		}
	})
	mt.Run("adjustments merge ledger and legacy", func(mt *mtest.T) {
		ledgerNS := mt.DB.Name() + "." + ledgerCollection
		legacyNS := mt.DB.Name() + ".commissionadjustments"
		mt.AddMockResponses(
			countResponse(ledgerNS, 2),
			mtest.CreateCursorResponse(0, ledgerNS, mtest.FirstBatch,
				bson.D{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "component", Value: ComponentTripCommission}},
				bson.D{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "component", Value: ComponentPetrolAdjustment}, {Key: "settlement_bucket", Value: BucketPetrol}, {Key: "amount_paise", Value: int64(500)}, {Key: "service_date_key", Value: "2026-07-20"}},
			),
			mtest.CreateCursorResponse(0, legacyNS, mtest.FirstBatch, bson.D{{Key: "_id", Value: primitive.NewObjectID()}, {Key: "payout_type", Value: BucketCommission}, {Key: "date", Value: now}, {Key: "amount", Value: -2.0}}),
		)
		rows, err := NewRepository(mt.DB).loadReportAdjustments(context.Background(), officeID, workerID, "2026-07-01", "2026-07-31")
		if err != nil || len(rows) != 2 || rows[0].PayoutType != BucketCommission || rows[1].PayoutType != BucketPetrol {
			mt.Fatalf("rows=%+v err=%v", rows, err)
		}
	})
	mt.Run("adjustment errors", func(mt *mtest.T) {
		repo := NewRepository(mt.DB)
		mt.AddMockResponses(commandError())
		if _, err := repo.loadReportAdjustments(context.Background(), officeID, workerID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected ledger error")
		}
		ledgerNS := mt.DB.Name() + "." + ledgerCollection
		mt.AddMockResponses(countResponse(ledgerNS, 0), mtest.CreateCursorResponse(0, ledgerNS, mtest.FirstBatch))
		if _, err := repo.loadReportAdjustments(context.Background(), officeID, workerID, "bad", "2026-07-31"); err == nil {
			mt.Fatal("expected date error")
		}
		mt.AddMockResponses(countResponse(ledgerNS, 0), mtest.CreateCursorResponse(0, ledgerNS, mtest.FirstBatch), commandError())
		if _, err := repo.loadReportAdjustments(context.Background(), officeID, workerID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected legacy find error")
		}
		legacyNS := mt.DB.Name() + ".commissionadjustments"
		mt.AddMockResponses(countResponse(ledgerNS, 0), mtest.CreateCursorResponse(0, ledgerNS, mtest.FirstBatch), mtest.CreateCursorResponse(0, legacyNS, mtest.FirstBatch, bson.D{{Key: "_id", Value: "invalid"}}))
		if _, err := repo.loadReportAdjustments(context.Background(), officeID, workerID, "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected legacy decode error")
		}
	})
}

func TestRepositoryLoadReportDetail(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID, workerID := primitive.NewObjectID(), primitive.NewObjectID()
	empty := func(mt *mtest.T, collection string) bson.D {
		return mtest.CreateCursorResponse(0, mt.DB.Name()+"."+collection, mtest.FirstBatch)
	}
	mt.Run("rider success", func(mt *mtest.T) {
		mt.AddMockResponses(empty(mt, "trips"), countResponse(mt.DB.Name()+"."+settlementCollection, 0), empty(mt, settlementCollection), empty(mt, "payouts"), countResponse(mt.DB.Name()+"."+ledgerCollection, 0), empty(mt, ledgerCollection), empty(mt, "commissionadjustments"))
		detail, err := NewRepository(mt.DB).LoadReportDetail(context.Background(), officeID, workerID, "rider", "2026-07-01", "2026-07-31")
		if err != nil || detail.Trips == nil || detail.Orders == nil || detail.Payouts == nil || detail.Adjustments == nil {
			mt.Fatalf("detail=%+v err=%v", detail, err)
		}
	})
	mt.Run("beautician source error", func(mt *mtest.T) {
		mt.AddMockResponses(commandError())
		if _, err := NewRepository(mt.DB).LoadReportDetail(context.Background(), officeID, workerID, "beautician", "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected source error")
		}
	})
	mt.Run("payout error", func(mt *mtest.T) {
		mt.AddMockResponses(empty(mt, "orders"), commandError())
		if _, err := NewRepository(mt.DB).LoadReportDetail(context.Background(), officeID, workerID, "beautician", "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected payout error")
		}
	})
	mt.Run("adjustment error", func(mt *mtest.T) {
		mt.AddMockResponses(empty(mt, "orders"), countResponse(mt.DB.Name()+"."+settlementCollection, 0), empty(mt, settlementCollection), empty(mt, "payouts"), commandError())
		if _, err := NewRepository(mt.DB).LoadReportDetail(context.Background(), officeID, workerID, "beautician", "2026-07-01", "2026-07-31"); err == nil {
			mt.Fatal("expected adjustment error")
		}
	})
}
