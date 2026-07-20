package static

import (
	"context"
	"testing"

	"github.com/JeraldVictor/hom-swag-reporting/internal/reports"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func petrolWeeklyRequest(officeID primitive.ObjectID) reports.Request {
	return reports.Request{Parameters: map[string]interface{}{
		"start_date": "2026-07-01",
		"end_date":   "2026-07-07",
		"office_id":  officeID.Hex(),
	}}
}

func TestPetrolWeeklyModeSelectsDataSource(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID := primitive.NewObjectID()

	for _, fixture := range []struct {
		name       string
		mode       string
		collection string
	}{
		{name: "default constructor remains shadow compatible", mode: "", collection: "trips"},
		{name: "shadow reads trips", mode: "shadow", collection: "trips"},
		{name: "unknown mode fails closed to shadow", mode: "anything-else", collection: "trips"},
		{name: "authoritative reads ledger", mode: "authoritative", collection: "earnings_ledger"},
	} {
		mt.Run(fixture.name, func(mt *mtest.T) {
			namespace := mt.DB.Name() + "." + fixture.collection
			mt.AddMockResponses(mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch))
			var executor *PetrolWeeklyExecutor
			if fixture.mode == "" {
				executor = NewPetrolWeeklyExecutor(mt.DB)
			} else {
				executor = NewPetrolWeeklyExecutorWithMode(mt.DB, fixture.mode)
			}
			if err := executor.Run(context.Background(), petrolWeeklyRequest(officeID), &integrationSink{}); err != nil {
				mt.Fatalf("run: %v", err)
			}
			event := mt.GetStartedEvent()
			if event == nil {
				mt.Fatal("expected aggregate command")
			}
			collection, ok := event.Command.Lookup("aggregate").StringValueOK()
			if !ok || collection != fixture.collection {
				mt.Fatalf("aggregate collection = %q, want %q", collection, fixture.collection)
			}
		})
	}
}

func TestPetrolWeeklyAuthoritativeUsesLedgerPaiseAndCompatibleRows(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	officeID := primitive.NewObjectID()
	workerID := primitive.NewObjectID()

	mt.Run("ledger row", func(mt *mtest.T) {
		namespace := mt.DB.Name() + ".earnings_ledger"
		mt.AddMockResponses(mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, bson.D{
			{Key: "_id", Value: workerID},
			{Key: "rider_name", Value: "Rider One"},
			{Key: "emp_code", Value: "R-001"},
			{Key: "total_distance", Value: 12.5},
			{Key: "total_amount_paise", Value: int64(12345)},
		}))

		sink := &integrationSink{}
		err := NewPetrolWeeklyExecutorWithMode(mt.DB, "authoritative").Run(
			context.Background(), petrolWeeklyRequest(officeID), sink,
		)
		if err != nil {
			mt.Fatalf("run: %v", err)
		}
		if got, want := len(sink.rows), 2; got != want {
			mt.Fatalf("row count = %d, want %d", got, want)
		}
		want := []interface{}{workerID.Hex(), "R-001", "Rider One", "12.50", "123.45"}
		for index, value := range want {
			if sink.rows[1][index] != value {
				mt.Fatalf("row[%d] = %#v, want %#v", index, sink.rows[1][index], value)
			}
		}

		event := mt.GetStartedEvent()
		pipeline, ok := event.Command.Lookup("pipeline").ArrayOK()
		if !ok {
			mt.Fatal("aggregate pipeline missing")
		}
		stages, err := pipeline.Values()
		if err != nil || len(stages) == 0 {
			mt.Fatalf("decode pipeline values: %v", err)
		}
		var firstStage bson.M
		if err := bson.Unmarshal(stages[0].Document(), &firstStage); err != nil {
			mt.Fatalf("decode first stage: %v", err)
		}
		match, ok := firstStage["$match"].(bson.M)
		if !ok {
			mt.Fatalf("first stage is not $match: %#v", firstStage)
		}
		if match["office_id"] != officeID || match["component"] != "petrol" || match["source_type"] != "trips" {
			mt.Fatalf("ledger match = %#v", match)
		}
	})
}
