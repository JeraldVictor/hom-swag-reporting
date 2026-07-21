package earnings

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type reconciliationBackendStub struct {
	fail        string
	orders      []OrderSource
	trips       []TripSource
	targets     []WorkerTarget
	bonus       float64
	beauticians []BeauticianLeaderboardSource
	riders      []RiderLeaderboardSource
	prizes      LeaderboardPrizes
	ledger      []LedgerEntry
}

func (s *reconciliationBackendStub) err(name string) error {
	if s.fail == name {
		return errors.New(name)
	}
	return nil
}
func (s *reconciliationBackendStub) LoadOrderSources(context.Context, primitive.ObjectID, string, string) ([]OrderSource, error) {
	return s.orders, s.err("orders")
}
func (s *reconciliationBackendStub) LoadTripSources(context.Context, primitive.ObjectID, string, string) ([]TripSource, error) {
	return s.trips, s.err("trips")
}
func (s *reconciliationBackendStub) LoadWorkerTargets(context.Context, primitive.ObjectID) ([]WorkerTarget, error) {
	return s.targets, s.err("targets")
}
func (s *reconciliationBackendStub) LoadTarget2Bonus(context.Context, primitive.ObjectID) (float64, error) {
	return s.bonus, s.err("bonus")
}
func (s *reconciliationBackendStub) LoadBeauticianLeaderboardSources(context.Context, primitive.ObjectID, string, string) ([]BeauticianLeaderboardSource, error) {
	return s.beauticians, s.err("beauticians")
}
func (s *reconciliationBackendStub) LoadRiderLeaderboardSources(context.Context, primitive.ObjectID, string, string) ([]RiderLeaderboardSource, error) {
	return s.riders, s.err("riders")
}
func (s *reconciliationBackendStub) LoadLeaderboardPrizes(context.Context, primitive.ObjectID) (LeaderboardPrizes, error) {
	return s.prizes, s.err("prizes")
}
func (s *reconciliationBackendStub) LoadReconciliationLedger(context.Context, primitive.ObjectID, string, string) ([]LedgerEntry, error) {
	return s.ledger, s.err("ledger")
}

func reconciliationFixture() (*reconciliationBackendStub, primitive.ObjectID, primitive.ObjectID, primitive.ObjectID) {
	office, beautician, rider := primitive.NewObjectID(), primitive.NewObjectID(), primitive.NewObjectID()
	orderID, tripID := primitive.NewObjectID(), primitive.NewObjectID()
	cost, special, general, upgrade := 100.0, 10.0, 20.0, 5.0
	commission, petrol := 30.0, 40.0
	order := OrderSource{ID: orderID, OfficeID: office, BeauticianID: beautician, Status: "completed", BookingInfo: OrderBookingInfo{Date: "2026-07-10"}, Snapshot: &CommissionSnapshot{OrderCost: &cost, SpecialCommission: &special, GeneralCommission: &general, UpgradeAddonCommission: &upgrade}}
	trip := TripSource{ID: tripID, OfficeID: office, RiderID: &rider, Date: "2026-07-11", Status: "completed", Snapshot: &PayableSnapshot{CommissionPayable: &commission, PetrolPayable: &petrol}}
	source := func(id primitive.ObjectID) *primitive.ObjectID { return &id }
	entries := []LedgerEntry{
		{WorkerID: beautician, WorkerType: "beautician", Component: ComponentSpecialCommission, SettlementBucket: BucketCommission, AmountPaise: 1000, SourceType: "orders", SourceID: source(orderID), ServiceDateKey: "2026-07-10"},
		{WorkerID: beautician, WorkerType: "beautician", Component: ComponentGeneralCommission, SettlementBucket: BucketCommission, AmountPaise: 2000, SourceType: "orders", SourceID: source(orderID), ServiceDateKey: "2026-07-10"},
		{WorkerID: beautician, WorkerType: "beautician", Component: ComponentUpgradeCommission, SettlementBucket: BucketCommission, AmountPaise: 500, SourceType: "orders", SourceID: source(orderID), ServiceDateKey: "2026-07-10"},
		{WorkerID: beautician, WorkerType: "beautician", Component: ComponentTargetBonus, SettlementBucket: BucketCommission, AmountPaise: 400, SourceType: "targets", ServiceDateKey: "2026-07-31", ConfigurationSnapshot: map[string]interface{}{"target_month": "2026-07"}},
		{WorkerID: rider, WorkerType: "rider", Component: ComponentTripCommission, SettlementBucket: BucketCommission, AmountPaise: 3000, SourceType: "trips", SourceID: source(tripID), ServiceDateKey: "2026-07-11"},
		{WorkerID: rider, WorkerType: "rider", Component: ComponentPetrol, SettlementBucket: BucketPetrol, AmountPaise: 4100, SourceType: "trips", SourceID: source(tripID), ServiceDateKey: "2026-07-11"},
		{WorkerID: beautician, WorkerType: "beautician", Component: ComponentLeaderboardBonus, SettlementBucket: BucketCommission, AmountPaise: 500, SourceType: "leaderboards", ServiceDateKey: "2026-07-31", ConfigurationSnapshot: map[string]interface{}{"period_start": "2026-07-01", "period_end": "2026-07-31", "leaderboard_type": "beautician"}},
		{WorkerID: rider, WorkerType: "rider", Component: ComponentLeaderboardBonus, SettlementBucket: BucketCommission, AmountPaise: 600, SourceType: "leaderboards", ServiceDateKey: "2026-07-31", ConfigurationSnapshot: map[string]interface{}{"period_start": "2026-07-01", "period_end": "2026-07-31", "leaderboard_type": "rider"}},
	}
	return &reconciliationBackendStub{
		orders: orderSlice(order), trips: []TripSource{trip}, targets: []WorkerTarget{{WorkerID: beautician, Target1: 50, Target2: 80}}, bonus: 4,
		beauticians: []BeauticianLeaderboardSource{{WorkerID: beautician, Revenue: 100, OrderCount: 1}},
		riders:      []RiderLeaderboardSource{{WorkerID: rider, WorkerType: "rider", TripCount: 1, TotalDistanceKM: 10}},
		prizes:      LeaderboardPrizes{Beautician: []float64{5}, Rider: []float64{6}}, ledger: entries,
	}, office, beautician, rider
}

func orderSlice(order OrderSource) []OrderSource { return []OrderSource{order} }

func TestReconcilerReportsComponentDiscrepancies(t *testing.T) {
	backend, office, _, _ := reconciliationFixture()
	result, err := NewReconciler(backend).Run(context.Background(), office, "2026-07-01", "2026-07-31")
	if err != nil {
		t.Fatal(err)
	}
	if result.Ready || result.ExpectedPaise != 12000 || result.LedgerPaise != 12100 || result.DifferencePaise != 100 || result.AbsoluteDifferencePaise != 100 || result.Matched != 7 || result.Mismatched != 1 || len(result.Rows) != 8 {
		t.Fatalf("result=%+v", result)
	}
	if result.Rows[0].Component != ComponentPetrol || result.Rows[0].DifferencePaise != 100 || result.Rows[0].Status != "mismatched" {
		t.Fatalf("first row=%+v", result.Rows[0])
	}
}

func TestReconcilerReadyWhenCanonicalTotalsMatch(t *testing.T) {
	backend, office, _, _ := reconciliationFixture()
	backend.ledger[5].AmountPaise = 4000
	result, err := NewReconciler(backend).Run(context.Background(), office, "2026-07-01", "2026-07-31")
	if err != nil || !result.Ready || result.Mismatched != 0 || result.DifferencePaise != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestReconcilerUsesSameRoundTripRepairAsRebuild(t *testing.T) {
	backend, office, _, _ := reconciliationFixture()
	backend.ledger[5].AmountPaise = 4000
	trip := &backend.trips[0]
	trip.IsTwoWay = true
	trip.IsCommissionable = true
	trip.CommissionAmount = 27.11
	trip.AutoDistanceKM = 13.556
	trip.FareCalculation = TripFareCalculation{PetrolCostPerLiter: 95.08, StandardMileagePerLiter: 30}
	trip.Snapshot.CommissionPayable = float(13.56)
	trip.Snapshot.PetrolPayable = float(42.97)
	trip.Snapshot.PetrolCostPerLiter = float(95.08)
	trip.Snapshot.StandardMileagePerLiter = float(30)
	backend.ledger[4].AmountPaise = 2711
	backend.ledger[5].AmountPaise = 8593

	result, err := NewReconciler(backend).Run(context.Background(), office, "2026-07-01", "2026-07-31")
	if err != nil || !result.Ready || result.Mismatched != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestReconcilerKeepsBeauticianAndRiderBoardsIndependent(t *testing.T) {
	backend, office, beautician, _ := reconciliationFixture()
	backend.ledger[5].AmountPaise = 4000
	backend.riders[0].WorkerID = beautician
	backend.riders[0].WorkerType = "beautician"
	backend.ledger[7].WorkerID = beautician
	backend.ledger[7].WorkerType = "beautician"
	result, err := NewReconciler(backend).Run(context.Background(), office, "2026-07-01", "2026-07-31")
	if err != nil || !result.Ready {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	boards := map[string]bool{}
	for _, row := range result.Rows {
		if row.WorkerID == beautician && row.Component == ComponentLeaderboardBonus {
			boards[row.Scope] = true
		}
	}
	if !boards["beautician_leaderboard"] || !boards["rider_leaderboard"] {
		t.Fatalf("leaderboard scopes=%v", boards)
	}
}

func TestReconcilerPropagatesSourceFailuresAndConfigurationErrors(t *testing.T) {
	for _, stage := range []string{"orders", "trips", "targets", "bonus", "beauticians", "riders", "prizes", "ledger"} {
		backend, office, _, _ := reconciliationFixture()
		backend.fail = stage
		if _, err := NewReconciler(backend).Run(context.Background(), office, "2026-07-01", "2026-07-31"); err == nil {
			t.Fatalf("%s error not propagated", stage)
		}
	}
	backend, office, _, _ := reconciliationFixture()
	if _, err := NewReconciler(backend).Run(context.Background(), office, "bad", "2026-07-31"); err == nil {
		t.Fatal("invalid range")
	}
	backend, office, _, _ = reconciliationFixture()
	backend.bonus = -1
	if _, err := NewReconciler(backend).Run(context.Background(), office, "2026-07-01", "2026-07-31"); err == nil {
		t.Fatal("invalid office bonus")
	}
	backend, office, _, _ = reconciliationFixture()
	backend.targets = []WorkerTarget{{}}
	if _, err := NewReconciler(backend).Run(context.Background(), office, "2026-07-01", "2026-07-31"); err == nil {
		t.Fatal("invalid target")
	}
	backend, office, _, _ = reconciliationFixture()
	backend.beauticians[0].Revenue = -1
	if _, err := NewReconciler(backend).Run(context.Background(), office, "2026-07-01", "2026-07-31"); err == nil {
		t.Fatal("invalid beautician score")
	}
	backend, office, _, _ = reconciliationFixture()
	backend.riders[0].WorkerType = "other"
	if _, err := NewReconciler(backend).Run(context.Background(), office, "2026-07-01", "2026-07-31"); err == nil {
		t.Fatal("invalid rider score")
	}
	backend, office, _, _ = reconciliationFixture()
	backend.beauticians = append(backend.beauticians, backend.beauticians[0])
	if _, err := NewReconciler(backend).Run(context.Background(), office, "2026-07-01", "2026-07-31"); err == nil {
		t.Fatal("duplicate beautician score")
	}
	backend, office, _, _ = reconciliationFixture()
	backend.riders = append(backend.riders, backend.riders[0])
	if _, err := NewReconciler(backend).Run(context.Background(), office, "2026-07-01", "2026-07-31"); err == nil {
		t.Fatal("duplicate rider score")
	}
}

func TestReconcilerCountsMissingSnapshotsAndIgnoresOutOfScopeData(t *testing.T) {
	backend, office, beautician, rider := reconciliationFixture()
	backend.orders = append(backend.orders,
		OrderSource{Status: "cancelled"},
		OrderSource{Status: "completed", IsDeleted: true},
		OrderSource{Status: "completed", BeauticianID: beautician, BookingInfo: OrderBookingInfo{Date: "2026-07-12"}},
	)
	backend.trips = append(backend.trips,
		TripSource{Status: "cancelled"},
		TripSource{Status: "completed", Date: "2026-07-12", RiderID: &rider},
	)
	backend.orders[0].Snapshot.SpecialCommission = nil
	backend.trips[0].Snapshot.PetrolPayable = nil
	backend.ledger = append(backend.ledger,
		LedgerEntry{Status: StatusVoid},
		LedgerEntry{WorkerID: beautician, WorkerType: "beautician", Component: ComponentCommissionAdjustment},
		LedgerEntry{WorkerID: beautician, WorkerType: "beautician", Component: ComponentSpecialCommission, SourceType: "orders", ServiceDateKey: "2026-06-30"},
	)
	result, err := NewReconciler(backend).Run(context.Background(), office, "2026-07-01", "2026-07-31")
	if err != nil || result.Ready || result.MissingSnapshots != 4 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestReconcilerDoesNotTreatUnassignedTransportAsPayable(t *testing.T) {
	backend, office, _, _ := reconciliationFixture()
	backend.ledger[5].AmountPaise = 4000
	zero := 0.0
	backend.trips = append(backend.trips, TripSource{
		ID: primitive.NewObjectID(), OfficeID: office, Date: "2026-07-12", Status: "completed",
		Snapshot: &PayableSnapshot{CommissionPayable: &zero, PetrolPayable: &zero},
	})
	result, err := NewReconciler(backend).Run(context.Background(), office, "2026-07-01", "2026-07-31")
	if err != nil || !result.Ready || result.MissingSnapshots != 0 {
		t.Fatalf("unassigned transport blocked reconciliation: result=%+v err=%v", result, err)
	}
}

func TestReconciliationHelpers(t *testing.T) {
	values := map[reconciliationKey]int64{}
	addReconciliationAmount(values, primitive.NewObjectID(), "rider", ComponentPetrol, BucketPetrol, 0)
	if len(values) != 0 || absPaise(-5) != 5 || absPaise(5) != 5 {
		t.Fatal("helper contract")
	}
	entry := LedgerEntry{WorkerID: primitive.NewObjectID(), WorkerType: "rider", Component: ComponentPetrol, SourceType: "trips", SourceID: func() *primitive.ObjectID { id := primitive.NewObjectID(); return &id }(), ServiceDateKey: "2026-07-01"}
	if _, included := reconciliationEntryScope(entry, "2026-07-01", "2026-07-31", "2026-07", "2026-07-31"); !included {
		t.Fatal("trip entry should be in scope")
	}
	if reconciliationDefaultScope(ComponentCommissionAdjustment) != "" {
		t.Fatal("adjustment must not have a parity scope")
	}
}
