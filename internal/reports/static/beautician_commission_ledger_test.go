package static

import (
	"context"
	"testing"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestBeauticianCommissionAuthoritativeUsesLedgerPayables(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID, workerID := primitive.NewObjectID(), primitive.NewObjectID()

	mt.Run("ledger components replace legacy payable calculations", func(mt *mtest.T) {
		ledgerNamespace := mt.DB.Name() + ".earnings_ledger"
		ordersNamespace := mt.DB.Name() + ".orders"
		officesNamespace := mt.DB.Name() + ".offices"
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, ledgerNamespace, mtest.FirstBatch, bson.D{
				{Key: "_id", Value: workerID}, {Key: "name", Value: "Beauty One"}, {Key: "emp_code", Value: "B-001"},
				{Key: "monthly_target1", Value: 100.0}, {Key: "monthly_target2", Value: 200.0},
				{Key: "special_commission_paise", Value: int64(1111)},
				{Key: "general_commission_paise", Value: int64(2222)},
				{Key: "upgrade_commission_paise", Value: int64(3333)},
				{Key: "target_bonus_paise", Value: int64(444)},
				{Key: "leaderboard_bonus_paise", Value: int64(555)},
				{Key: "leaderboard_rank", Value: 2},
			}),
			mtest.CreateCursorResponse(0, ordersNamespace, mtest.FirstBatch, bson.D{
				{Key: "_id", Value: workerID}, {Key: "name", Value: "Beauty One"}, {Key: "emp_code", Value: "B-001"},
				{Key: "monthly_target1", Value: 100.0}, {Key: "monthly_target2", Value: 200.0},
				{Key: "total_special_commission", Value: 90.0}, {Key: "total_general_commission", Value: 99.0},
				{Key: "total_upgrade_addon_commission", Value: 80.0}, {Key: "total_revenue", Value: 250.0},
				{Key: "total_refund", Value: 10.0}, {Key: "order_count", Value: 3},
			}),
			mtest.CreateCursorResponse(0, ordersNamespace, mtest.FirstBatch, bson.D{
				{Key: "_id", Value: workerID}, {Key: "total_revenue", Value: 250.0},
			}),
			mtest.CreateCursorResponse(0, officesNamespace, mtest.FirstBatch, bson.D{
				{Key: "_id", Value: officeID}, {Key: "monthly_target2_bonus", Value: 9.99},
			}),
		)

		sink := &integrationSink{}
		err := NewBeauticianCommissionExecutorWithMode(mt.DB, "authoritative").Run(context.Background(), reports.Request{
			Parameters: map[string]interface{}{
				"start_date": "2026-07-01", "end_date": "2026-07-31", "office_id": officeID.Hex(),
			},
		}, sink)
		if err != nil {
			mt.Fatalf("run: %v", err)
		}
		if got, want := len(sink.rows), 2; got != want {
			mt.Fatalf("row count = %d, want %d", got, want)
		}
		assertLedgerReportCell(t, sink.rows, "Special Commission", "11.11")
		assertLedgerReportCell(t, sink.rows, "Payable General Commission", "22.22")
		assertLedgerReportCell(t, sink.rows, "Potential General Commission", "99.00")
		assertLedgerReportCell(t, sink.rows, "Upgrade/Add-on Commission", "33.33")
		assertLedgerReportCell(t, sink.rows, "Target 2 Bonus", "4.44")
		assertLedgerReportCell(t, sink.rows, "Leaderboard Rank", "#2")
		assertLedgerReportCell(t, sink.rows, "Leaderboard Bonus", "5.55")
		assertLedgerReportCell(t, sink.rows, "Total Commission", "77.00")

		firstEvent := mt.GetAllStartedEvents()[0]
		if collection, ok := firstEvent.Command.Lookup("aggregate").StringValueOK(); !ok || collection != "earnings_ledger" {
			mt.Fatalf("first aggregate collection = %q, want earnings_ledger", collection)
		}
	})
}

func TestBeauticianCommissionConstructorsFailClosedToLegacy(t *testing.T) {
	if executor := NewBeauticianCommissionExecutor(nil); executor.mode != "shadow" {
		t.Fatalf("default mode = %q, want shadow", executor.mode)
	}
	if executor := NewBeauticianCommissionExecutorWithMode(nil, "unexpected"); executor.mode == "authoritative" {
		t.Fatal("unexpected mode must not select authoritative behavior")
	}
}

func TestBeauticianCommissionAuthoritativeLimitsAfterLedgerMerge(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID := primitive.NewObjectID()
	firstID := primitive.NewObjectIDFromTimestamp(primitive.DateTime(1).Time())
	secondID := primitive.NewObjectIDFromTimestamp(primitive.DateTime(2).Time())

	mt.Run("ledger-only workers obey the final report limit", func(mt *mtest.T) {
		ledgerNamespace := mt.DB.Name() + ".earnings_ledger"
		ordersNamespace := mt.DB.Name() + ".orders"
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, ledgerNamespace, mtest.FirstBatch,
				bson.D{{Key: "_id", Value: secondID}, {Key: "name", Value: "Second"}, {Key: "special_commission_paise", Value: int64(200)}},
				bson.D{{Key: "_id", Value: firstID}, {Key: "name", Value: "First"}, {Key: "special_commission_paise", Value: int64(100)}},
			),
			mtest.CreateCursorResponse(0, ordersNamespace, mtest.FirstBatch),
			mtest.CreateCursorResponse(0, ordersNamespace, mtest.FirstBatch),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".offices", mtest.FirstBatch),
		)
		sink := &integrationSink{}
		err := NewBeauticianCommissionExecutorWithMode(mt.DB, "authoritative").Run(context.Background(), reports.Request{
			Parameters: map[string]interface{}{"start_date": "2026-07-01", "end_date": "2026-07-31", "office_id": officeID.Hex()},
			Limit:      1,
		}, sink)
		if err != nil {
			mt.Fatal(err)
		}
		if len(sink.rows) != 2 || sink.rows[1][0] != firstID.Hex() {
			mt.Fatalf("limited rows = %#v", sink.rows)
		}
	})
}

func assertLedgerReportCell(t *testing.T, rows [][]interface{}, column string, want interface{}) {
	t.Helper()
	index := integrationColumnIndex(t, rows[0], column)
	if got := rows[1][index]; got != want {
		t.Fatalf("%s = %#v, want %#v", column, got, want)
	}
}
