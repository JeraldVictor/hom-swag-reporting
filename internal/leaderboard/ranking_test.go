package leaderboard

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func oid(hex string) primitive.ObjectID {
	id, err := primitive.ObjectIDFromHex(hex)
	if err != nil {
		panic(err)
	}
	return id
}

func TestRankBeauticiansUsesRevenueOrdersAndStableID(t *testing.T) {
	a := oid("000000000000000000000001")
	b := oid("000000000000000000000002")
	c := oid("000000000000000000000003")
	d := oid("000000000000000000000004")
	got := RankBeauticians([]BeauticianScore{
		{WorkerID: d, Revenue: 90, OrderCount: 100},
		{WorkerID: c, Revenue: 100, OrderCount: 1},
		{WorkerID: b, Revenue: 100, OrderCount: 2},
		{WorkerID: a, Revenue: 100, OrderCount: 2},
	}, []float64{300, 200})
	want := []Award{{a, 1, 300}, {b, 2, 200}, {c, 3, 0}, {d, 4, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("awards = %#v, want %#v", got, want)
	}
}

func TestRankRidersUsesTripsDistanceAndDoesNotMutateInput(t *testing.T) {
	a := oid("000000000000000000000001")
	b := oid("000000000000000000000002")
	c := oid("000000000000000000000003")
	d := oid("000000000000000000000004")
	scores := []RiderScore{{WorkerID: d, TripCount: 2, TotalDistanceKM: 99}, {WorkerID: c, TripCount: 3, TotalDistanceKM: 9}, {WorkerID: b, TripCount: 3, TotalDistanceKM: 10}, {WorkerID: a, TripCount: 3, TotalDistanceKM: 10}}
	original := append([]RiderScore(nil), scores...)
	got := RankRiders(scores, []float64{50})
	want := []Award{{a, 1, 50}, {b, 2, 0}, {c, 3, 0}, {d, 4, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("awards = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(scores, original) {
		t.Fatal("ranker mutated source scores")
	}
}

func TestRankersAcceptEmptyInput(t *testing.T) {
	if got := RankBeauticians(nil, []float64{1}); len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
	if got := RankRiders(nil, []float64{1}); len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}
