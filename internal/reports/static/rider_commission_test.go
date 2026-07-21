package static

import (
	"context"
	"testing"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func riderCommissionRequest() reports.Request {
	return reports.Request{Parameters: map[string]interface{}{
		"start_date": "2026-07-01",
		"end_date":   "2026-07-31",
	}}
}

func TestRiderCommissionModeSelectsDataSource(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, fixture := range []struct {
		name       string
		mode       string
		collection string
	}{
		{name: "default constructor remains shadow compatible", collection: "trips"},
		{name: "shadow reads trips", mode: "shadow", collection: "trips"},
		{name: "unknown mode fails closed to shadow", mode: "other", collection: "trips"},
		{name: "authoritative reads ledger first", mode: "authoritative", collection: "earnings_ledger"},
	} {
		mt.Run(fixture.name, func(mt *mtest.T) {
			mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+fixture.collection, mtest.FirstBatch))
			if fixture.mode == "authoritative" {
				mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".trips", mtest.FirstBatch))
			}
			var executor *RiderCommissionExecutor
			if fixture.mode == "" {
				executor = NewRiderCommissionExecutor(mt.DB)
			} else {
				executor = NewRiderCommissionExecutorWithMode(mt.DB, fixture.mode)
			}
			if err := executor.Run(context.Background(), riderCommissionRequest(), &integrationSink{}); err != nil {
				mt.Fatalf("run: %v", err)
			}
			event := mt.GetStartedEvent()
			collection, ok := event.Command.Lookup("aggregate").StringValueOK()
			if !ok || collection != fixture.collection {
				mt.Fatalf("first aggregate collection = %q, want %q", collection, fixture.collection)
			}
		})
	}
}

func TestRiderCommissionAuthoritativeUsesOnlyLedgerForMoney(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	workerID := primitive.NewObjectID()
	officeID := primitive.NewObjectID()

	mt.Run("merges ledger money with descriptive trip statistics", func(mt *mtest.T) {
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".earnings_ledger", mtest.FirstBatch, bson.D{
				{Key: "_id", Value: workerID}, {Key: "name", Value: "Ledger Rider"}, {Key: "emp_code", Value: "R-7"},
				{Key: "petrol_payable_paise", Value: int64(12345)},
				{Key: "commission_payable_paise", Value: int64(6789)},
				{Key: "leaderboard_bonus_paise", Value: int64(501)},
				{Key: "leaderboard_rank", Value: 2},
			}),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".trips", mtest.FirstBatch, bson.D{
				{Key: "_id", Value: workerID}, {Key: "name", Value: "Trip Rider"}, {Key: "emp_code", Value: "R-7"},
				{Key: "trip_count", Value: 4}, {Key: "total_distance_km", Value: 18.25},
			}),
		)
		req := riderCommissionRequest()
		req.Parameters["office_id"] = officeID.Hex()
		req.Parameters["staff_id"] = workerID.Hex()
		sink := &integrationSink{}
		if err := NewRiderCommissionExecutorWithMode(mt.DB, "authoritative").Run(context.Background(), req, sink); err != nil {
			mt.Fatalf("run: %v", err)
		}
		if got, want := len(sink.rows), 2; got != want {
			mt.Fatalf("rows = %d, want %d", got, want)
		}
		want := []interface{}{workerID.Hex(), "R-7", "Trip Rider", 4, "18.25", "123.45", "67.89", "#2", "5.01", "72.90"}
		for index, value := range want {
			if sink.rows[1][index] != value {
				mt.Fatalf("row[%d] = %#v, want %#v", index, sink.rows[1][index], value)
			}
		}

		events := mt.GetAllStartedEvents()
		if len(events) != 2 {
			mt.Fatalf("aggregate command count = %d, want 2", len(events))
		}
		pipeline, ok := events[0].Command.Lookup("pipeline").ArrayOK()
		if !ok {
			mt.Fatal("ledger aggregate pipeline missing")
		}
		stages, err := pipeline.Values()
		if err != nil || len(stages) == 0 {
			mt.Fatalf("pipeline stages: %v", err)
		}
		var firstStage bson.M
		if err := bson.Unmarshal(stages[0].Document(), &firstStage); err != nil {
			mt.Fatalf("decode ledger match: %v", err)
		}
		match := firstStage["$match"].(bson.M)
		if match["office_id"] != officeID || match["worker_id"] != workerID || match["status"] == nil {
			mt.Fatalf("ledger match = %#v", match)
		}
	})
}

func TestRiderCommissionAuthoritativeKeepsZeroMoneyTrips(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	workerID := primitive.NewObjectID()
	mt.Run("trip without ledger entry", func(mt *mtest.T) {
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".earnings_ledger", mtest.FirstBatch),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".trips", mtest.FirstBatch, bson.D{
				{Key: "_id", Value: workerID}, {Key: "name", Value: "Zero Rider"}, {Key: "emp_code", Value: "R-0"},
				{Key: "trip_count", Value: 1}, {Key: "total_distance_km", Value: 3.0},
			}),
		)
		sink := &integrationSink{}
		if err := NewRiderCommissionExecutorWithMode(mt.DB, "authoritative").Run(context.Background(), riderCommissionRequest(), sink); err != nil {
			mt.Fatalf("run: %v", err)
		}
		if len(sink.rows) != 2 || sink.rows[1][3] != 1 || sink.rows[1][5] != "0.00" || sink.rows[1][9] != "0.00" {
			mt.Fatalf("unexpected zero-money row: %#v", sink.rows)
		}
	})
}

func TestRiderCommissionStaffFilterRanksAgainstWholeOffice(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	firstID := primitive.NewObjectID()
	selectedID := primitive.NewObjectID()
	officeID := primitive.NewObjectID()

	mt.Run("selected rider retains office rank", func(mt *mtest.T) {
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".trips", mtest.FirstBatch,
				bson.D{{Key: "_id", Value: firstID}, {Key: "name", Value: "First"}, {Key: "trip_count", Value: 10}, {Key: "total_distance_km", Value: 90.0}},
				bson.D{{Key: "_id", Value: selectedID}, {Key: "name", Value: "Selected"}, {Key: "trip_count", Value: 5}, {Key: "total_distance_km", Value: 50.0}},
			),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".offices", mtest.FirstBatch,
				bson.D{{Key: "_id", Value: officeID}, {Key: "leaderboard_prizes", Value: bson.M{"rider": bson.A{100.0, 50.0}}}},
			),
		)
		req := riderCommissionRequest()
		req.Parameters["office_id"] = officeID.Hex()
		req.Parameters["staff_id"] = selectedID.Hex()
		req.Limit = 1
		sink := &integrationSink{}
		if err := NewRiderCommissionExecutorWithMode(mt.DB, "shadow").Run(context.Background(), req, sink); err != nil {
			mt.Fatalf("run: %v", err)
		}
		if len(sink.rows) != 2 {
			mt.Fatalf("rows = %#v", sink.rows)
		}
		if sink.rows[1][0] != selectedID.Hex() || sink.rows[1][7] != "#2" || sink.rows[1][8] != "50.00" {
			mt.Fatalf("selected row = %#v", sink.rows[1])
		}
	})
}
