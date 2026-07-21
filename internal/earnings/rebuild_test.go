package earnings

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/JeraldVictor/hom-swag-reporting/internal/leaderboard"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// rebuildBackend is deliberately small: this test exercises processor input
// validation without requiring Mongo or a running reporting service.
type rebuildBackend struct {
	job                                                                                                      RebuildJob
	orders                                                                                                   []OrderSource
	finished                                                                                                 bool
	finishErr                                                                                                error
	entries                                                                                                  []LedgerEntry
	beauticians                                                                                              []BeauticianLeaderboardSource
	riders                                                                                                   []RiderLeaderboardSource
	prizes                                                                                                   LeaderboardPrizes
	finishStatus                                                                                             string
	claimErr, ordersErr, tripsErr, targetsErr, target2BonusErr, beauticiansErr, ridersErr, prizesErr, putErr error
	trips                                                                                                    []TripSource
	targets                                                                                                  []WorkerTarget
	target2Bonus                                                                                             float64
	stored                                                                                                   LedgerEntry
	created                                                                                                  bool
	stats                                                                                                    RebuildStats
	message                                                                                                  string
}

func (b *rebuildBackend) ClaimNextRebuild(context.Context) (RebuildJob, error) {
	return b.job, b.claimErr
}
func (b *rebuildBackend) LoadOrderSources(context.Context, primitive.ObjectID, string, string) ([]OrderSource, error) {
	return b.orders, b.ordersErr
}
func (b *rebuildBackend) LoadTripSources(context.Context, primitive.ObjectID, string, string) ([]TripSource, error) {
	return b.trips, b.tripsErr
}
func (b *rebuildBackend) LoadWorkerTargets(context.Context, primitive.ObjectID) ([]WorkerTarget, error) {
	return b.targets, b.targetsErr
}
func (b *rebuildBackend) LoadTarget2Bonus(context.Context, primitive.ObjectID) (float64, error) {
	return b.target2Bonus, b.target2BonusErr
}
func (b *rebuildBackend) LoadBeauticianLeaderboardSources(context.Context, primitive.ObjectID, string, string) ([]BeauticianLeaderboardSource, error) {
	return b.beauticians, b.beauticiansErr
}
func (b *rebuildBackend) LoadRiderLeaderboardSources(context.Context, primitive.ObjectID, string, string) ([]RiderLeaderboardSource, error) {
	return b.riders, b.ridersErr
}
func (b *rebuildBackend) LoadLeaderboardPrizes(context.Context, primitive.ObjectID) (LeaderboardPrizes, error) {
	return b.prizes, b.prizesErr
}
func (b *rebuildBackend) PutSourceEntry(_ context.Context, entry LedgerEntry) (LedgerEntry, bool, error) {
	b.entries = append(b.entries, entry)
	return b.stored, b.created || b.stored.ID.IsZero(), b.putErr
}

func TestProcessorMaterializesDeterministicLeaderboardAwards(t *testing.T) {
	beautician := primitive.NewObjectID()
	rider := primitive.NewObjectID()
	office := primitive.NewObjectID()
	b := &rebuildBackend{
		job:         RebuildJob{ID: primitive.NewObjectID(), OfficeID: office, RequestedBy: primitive.NewObjectID(), Scope: "leaderboards", StartDate: "2026-07-01", EndDate: "2026-07-31"},
		beauticians: []BeauticianLeaderboardSource{{WorkerID: beautician, Revenue: 500, OrderCount: 2}},
		riders:      []RiderLeaderboardSource{{WorkerID: rider, WorkerType: "rider", TripCount: 3, TotalDistanceKM: 12}},
		prizes:      LeaderboardPrizes{Beautician: []float64{35}, Rider: []float64{25}},
	}
	processed, err := NewProcessor(b).ProcessNext(context.Background())
	if err != nil || !processed || len(b.entries) != 2 {
		t.Fatalf("processed=%v err=%v entries=%d", processed, err, len(b.entries))
	}
	for _, entry := range b.entries {
		if entry.Component != ComponentLeaderboardBonus || entry.SourceID != nil || entry.ServiceDateKey != "2026-07-31" || entry.ConfigurationSnapshot["rank"] != 1 {
			t.Fatalf("unexpected award entry: %#v", entry)
		}
		if entry.IdempotencyKey == "" || entry.CalculationVersion != leaderboardCalculationVersion {
			t.Fatal("missing stable identity/calculation version")
		}
		if entry.ConfigurationSnapshot["configuration_version"] == "" || entry.ConfigurationSnapshot["ranking_contract"] == "" {
			t.Fatalf("missing immutable configuration snapshot: %#v", entry.ConfigurationSnapshot)
		}
		prizes, ok := entry.ConfigurationSnapshot["prize_schedule_paise"].([]int64)
		if !ok || len(prizes) != 1 || prizes[0] != entry.AmountPaise {
			t.Fatalf("effective prize schedule was not captured: %#v", entry.ConfigurationSnapshot)
		}
	}
}

func TestLeaderboardConfigurationVersionIsContentAddressed(t *testing.T) {
	first := snapshotLeaderboardConfiguration(LeaderboardPrizes{Beautician: []float64{35}, Rider: []float64{25}})
	replay := snapshotLeaderboardConfiguration(LeaderboardPrizes{Beautician: []float64{35.004}, Rider: []float64{25}})
	changed := snapshotLeaderboardConfiguration(LeaderboardPrizes{Beautician: []float64{36}, Rider: []float64{25}})
	if first.Version == "" || first.Version != replay.Version {
		t.Fatalf("equivalent paise configuration should have a stable version: first=%q replay=%q", first.Version, replay.Version)
	}
	if first.Version == changed.Version {
		t.Fatal("an effective prize change must produce a new configuration version")
	}
}

func TestLeaderboardIdentitySeparatesBoardFromWorkerType(t *testing.T) {
	worker := primitive.NewObjectID()
	b := &rebuildBackend{
		job:         RebuildJob{ID: primitive.NewObjectID(), OfficeID: primitive.NewObjectID(), RequestedBy: primitive.NewObjectID(), Scope: "leaderboards", StartDate: "2026-07-01", EndDate: "2026-07-31"},
		beauticians: []BeauticianLeaderboardSource{{WorkerID: worker, Revenue: 500, OrderCount: 2}},
		riders:      []RiderLeaderboardSource{{WorkerID: worker, WorkerType: "beautician", TripCount: 3, TotalDistanceKM: 12}},
		prizes:      LeaderboardPrizes{Beautician: []float64{35}, Rider: []float64{25}},
	}
	if _, err := NewProcessor(b).ProcessNext(context.Background()); err != nil || len(b.entries) != 2 {
		t.Fatalf("err=%v entries=%d", err, len(b.entries))
	}
	if b.entries[0].IdempotencyKey == b.entries[1].IdempotencyKey {
		t.Fatalf("distinct board awards collided: %q", b.entries[0].IdempotencyKey)
	}
	if b.entries[0].WorkerType != "beautician" || b.entries[1].WorkerType != "beautician" {
		t.Fatalf("ledger worker type must remain the actual payable worker type: %#v", b.entries)
	}
}

func TestProcessorRejectsInvalidLeaderboardPrize(t *testing.T) {
	b := &rebuildBackend{job: RebuildJob{ID: primitive.NewObjectID(), OfficeID: primitive.NewObjectID(), Scope: "leaderboards", StartDate: "2026-07-01", EndDate: "2026-07-31"}, prizes: LeaderboardPrizes{Rider: []float64{-1}}}
	processed, err := NewProcessor(b).ProcessNext(context.Background())
	if err == nil || !processed || len(b.entries) != 0 || b.finishStatus != "failed" {
		t.Fatalf("processed=%v err=%v entries=%d status=%s", processed, err, len(b.entries), b.finishStatus)
	}
}
func (b *rebuildBackend) FinishRebuild(_ context.Context, _ primitive.ObjectID, status string, stats RebuildStats, message string) error {
	b.finished = true
	b.finishStatus = status
	b.stats = stats
	b.message = message
	return b.finishErr
}

func TestProcessorMalformedSourceDateDoesNotPanic(t *testing.T) {
	worker := primitive.NewObjectID()
	b := &rebuildBackend{
		job: RebuildJob{ID: primitive.NewObjectID(), OfficeID: primitive.NewObjectID(), Scope: "commissions", StartDate: "2026-07-01", EndDate: "2026-07-31"},
		orders: []OrderSource{{
			ID: primitive.NewObjectID(), BeauticianID: worker, Status: "completed",
			BookingInfo: struct {
				Date string `bson:"date"`
			}{Date: "bad"},
		}},
	}
	processed, err := NewProcessor(b).ProcessNext(context.Background())
	if err != nil || !processed {
		t.Fatalf("processed=%v err=%v", processed, err)
	}
	if !b.finished {
		t.Fatal("rebuild should be finalized even when source rows are malformed")
	}
}

func TestCoveringMonthsRejectsMalformedDates(t *testing.T) {
	if _, _, err := coveringMonths("not-a-date", "2026-07-31"); err == nil {
		t.Fatal("expected malformed start date to be rejected")
	}
	if _, _, err := coveringMonths("2026-07-01", "2026-02-30"); err == nil {
		t.Fatal("expected impossible end date to be rejected")
	}
}

func TestSameSourceEntryNormalizesBSONConfigurationNumbers(t *testing.T) {
	left := LedgerEntry{ConfigurationSnapshot: map[string]interface{}{"rank": int32(1), "prize": 25.0, "schedule": []int64{2500, 1000}}}
	right := LedgerEntry{ConfigurationSnapshot: map[string]interface{}{"rank": 1, "prize": int64(25), "schedule": []interface{}{int32(2500), float64(1000)}}}
	if !sameSourceEntry(left, right) {
		t.Fatal("BSON numeric widths and array representations should not create false rebuild conflicts")
	}
	right.ConfigurationSnapshot["rank"] = 2
	if sameSourceEntry(left, right) {
		t.Fatal("a changed rank must be detected as a conflict")
	}
}

func float(value float64) *float64 { return &value }

func TestProcessorClaimAndFinishFailures(t *testing.T) {
	boom := errors.New("boom")
	for _, test := range []struct {
		name          string
		b             *rebuildBackend
		wantProcessed bool
	}{
		{"empty queue", &rebuildBackend{claimErr: ErrNoQueuedRebuild}, false},
		{"claim error", &rebuildBackend{claimErr: boom}, false},
		{"finish error", &rebuildBackend{job: RebuildJob{Scope: "petrol"}, finishErr: boom}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			processed, err := NewProcessor(test.b).ProcessNext(context.Background())
			if processed != test.wantProcessed || (test.name == "empty queue") != (err == nil) {
				t.Fatalf("processed=%v err=%v", processed, err)
			}
		})
	}
}

func TestProcessorMaterializesOrdersAndTrips(t *testing.T) {
	office, worker, rider := primitive.NewObjectID(), primitive.NewObjectID(), primitive.NewObjectID()
	orderID, tripID := primitive.NewObjectID(), primitive.NewObjectID()
	b := &rebuildBackend{
		job:     RebuildJob{ID: primitive.NewObjectID(), OfficeID: office, RequestedBy: primitive.NewObjectID(), Scope: "all", StartDate: "2026-07-01", EndDate: "2026-07-31"},
		targets: []WorkerTarget{{WorkerID: worker, Target1: 100}},
		orders: []OrderSource{
			{ID: orderID, OfficeID: office, BeauticianID: worker, Status: "completed", BookingInfo: OrderBookingInfo{Date: "2026-07-15"}, Snapshot: &CommissionSnapshot{OrderCost: float(150), SpecialCommission: float(10), GeneralCommission: float(20), UpgradeAddonCommission: float(5), IsPaid: true}},
			{ID: primitive.NewObjectID(), BeauticianID: worker, Status: "completed", BookingInfo: OrderBookingInfo{Date: "2026-06-30"}, Snapshot: &CommissionSnapshot{OrderCost: float(10)}},
		},
		trips:  []TripSource{{ID: tripID, OfficeID: office, RiderID: &rider, Date: "2026-07-16", Snapshot: &PayableSnapshot{CommissionPayable: float(7), PetrolPayable: float(8)}}},
		prizes: LeaderboardPrizes{},
	}
	processed, err := NewProcessor(b).ProcessNext(context.Background())
	if err != nil || !processed || b.finishStatus != "completed" || len(b.entries) != 5 || b.stats.Inserted != 5 {
		t.Fatalf("processed=%v err=%v status=%s entries=%d stats=%+v", processed, err, b.finishStatus, len(b.entries), b.stats)
	}
	if b.entries[0].Status != StatusSettled || b.entries[0].SettledAmountPaise != b.entries[0].AmountPaise || b.entries[0].SourceType != "orders" {
		t.Fatalf("paid source not preserved: %+v", b.entries[0])
	}
}

func TestProcessorMaterializesTarget2BonusWithImmutableInputs(t *testing.T) {
	office, worker := primitive.NewObjectID(), primitive.NewObjectID()
	b := &rebuildBackend{
		job:          RebuildJob{ID: primitive.NewObjectID(), OfficeID: office, RequestedBy: primitive.NewObjectID(), Scope: "commissions", StartDate: "2026-07-10", EndDate: "2026-07-20"},
		targets:      []WorkerTarget{{WorkerID: worker, Target1: 100, Target2: 200}},
		target2Bonus: 50,
		orders: []OrderSource{
			{ID: primitive.NewObjectID(), BeauticianID: worker, Status: "completed", BookingInfo: OrderBookingInfo{Date: "2026-07-02"}, Snapshot: &CommissionSnapshot{OrderCost: float(125)}},
			{ID: primitive.NewObjectID(), BeauticianID: worker, Status: "completed", BookingInfo: OrderBookingInfo{Date: "2026-07-25"}, Snapshot: &CommissionSnapshot{OrderCost: float(75)}},
		},
	}
	if _, err := NewProcessor(b).ProcessNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b.entries) != 1 {
		t.Fatalf("entries=%d, want one target bonus", len(b.entries))
	}
	entry := b.entries[0]
	if entry.Component != ComponentTargetBonus || entry.AmountPaise != 5000 || entry.ServiceDateKey != "2026-07-31" || entry.SourceType != "targets" {
		t.Fatalf("unexpected target bonus: %+v", entry)
	}
	if got := entry.ConfigurationSnapshot; got["target_month"] != "2026-07" || got["monthly_target2"] != float64(200) || got["monthly_revenue"] != float64(200) || got["monthly_target2_bonus"] != float64(50) {
		t.Fatalf("configuration snapshot=%#v", got)
	}
	wantKey := "target_bonus:" + office.Hex() + ":" + worker.Hex() + ":2026-07:v1"
	if entry.IdempotencyKey != wantKey {
		t.Fatalf("idempotency key=%q want %q", entry.IdempotencyKey, wantKey)
	}
}

func TestProcessorDoesNotMaterializeUnearnedTarget2Bonus(t *testing.T) {
	worker := primitive.NewObjectID()
	validOrder := OrderSource{ID: primitive.NewObjectID(), BeauticianID: worker, Status: "completed", BookingInfo: OrderBookingInfo{Date: "2026-07-15"}, Snapshot: &CommissionSnapshot{OrderCost: float(199)}}
	tests := []struct {
		name   string
		target float64
		bonus  float64
		orders []OrderSource
	}{
		{name: "target not reached", target: 200, bonus: 50, orders: []OrderSource{validOrder}},
		{name: "target not configured", target: 0, bonus: 50, orders: []OrderSource{{ID: primitive.NewObjectID(), BeauticianID: worker, Status: "completed", BookingInfo: OrderBookingInfo{Date: "2026-07-15"}, Snapshot: &CommissionSnapshot{OrderCost: float(500)}}}},
		{name: "office bonus zero", target: 100, bonus: 0, orders: []OrderSource{{ID: primitive.NewObjectID(), BeauticianID: worker, Status: "completed", BookingInfo: OrderBookingInfo{Date: "2026-07-15"}, Snapshot: &CommissionSnapshot{OrderCost: float(500)}}}},
		{name: "incomplete monthly revenue", target: 100, bonus: 50, orders: []OrderSource{{ID: primitive.NewObjectID(), BeauticianID: worker, Status: "completed", BookingInfo: OrderBookingInfo{Date: "2026-07-15"}, Snapshot: nil}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			b := &rebuildBackend{
				job:     RebuildJob{ID: primitive.NewObjectID(), OfficeID: primitive.NewObjectID(), Scope: "commissions", StartDate: "2026-07-01", EndDate: "2026-07-31"},
				targets: []WorkerTarget{{WorkerID: worker, Target1: 50, Target2: test.target}}, target2Bonus: test.bonus, orders: test.orders,
			}
			if _, err := NewProcessor(b).ProcessNext(context.Background()); err != nil {
				t.Fatal(err)
			}
			for _, entry := range b.entries {
				if entry.Component == ComponentTargetBonus {
					t.Fatalf("unexpected target bonus: %+v", entry)
				}
			}
		})
	}
}

func TestProcessorRejectsInvalidTarget2Configuration(t *testing.T) {
	base := RebuildJob{ID: primitive.NewObjectID(), OfficeID: primitive.NewObjectID(), Scope: "commissions", StartDate: "2026-07-01", EndDate: "2026-07-31"}
	for _, test := range []struct {
		name string
		b    *rebuildBackend
	}{
		{name: "bonus load error", b: &rebuildBackend{job: base, target2BonusErr: errors.New("office")}},
		{name: "negative bonus", b: &rebuildBackend{job: base, target2Bonus: -1}},
		{name: "negative worker target", b: &rebuildBackend{job: base, targets: []WorkerTarget{{WorkerID: primitive.NewObjectID(), Target2: -1}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewProcessor(test.b).ProcessNext(context.Background()); err == nil || test.b.finishStatus != "failed" || len(test.b.entries) != 0 {
				t.Fatalf("err=%v status=%s entries=%d", err, test.b.finishStatus, len(test.b.entries))
			}
		})
	}
}

func TestProcessorReturnsTarget2BonusWriteFailure(t *testing.T) {
	worker := primitive.NewObjectID()
	b := &rebuildBackend{
		job:          RebuildJob{ID: primitive.NewObjectID(), OfficeID: primitive.NewObjectID(), Scope: "commissions", StartDate: "2026-07-01", EndDate: "2026-07-31"},
		targets:      []WorkerTarget{{WorkerID: worker, Target2: 100}},
		target2Bonus: 25,
		putErr:       errors.New("write failed"),
		orders: []OrderSource{{
			ID: primitive.NewObjectID(), BeauticianID: worker, Status: "completed", BookingInfo: OrderBookingInfo{Date: "2026-07-10"},
			Snapshot: &CommissionSnapshot{OrderCost: float(100), SpecialCommission: float(0), GeneralCommission: float(0), UpgradeAddonCommission: float(0)},
		}},
	}
	if _, err := NewProcessor(b).ProcessNext(context.Background()); err == nil || b.finishStatus != "failed" {
		t.Fatalf("err=%v status=%s", err, b.finishStatus)
	}
}

func TestProcessorNeverMaterializesNonCompletedOrders(t *testing.T) {
	worker := primitive.NewObjectID()
	orders := make([]OrderSource, 0, 5)
	for _, status := range []string{"cancelled", "cancelled_and_refunded", "refunded", "ongoing", ""} {
		orders = append(orders, OrderSource{
			ID: primitive.NewObjectID(), BeauticianID: worker, Status: status, BookingInfo: OrderBookingInfo{Date: "2026-07-15"},
			Snapshot: &CommissionSnapshot{OrderCost: float(500), SpecialCommission: float(50), GeneralCommission: float(50), UpgradeAddonCommission: float(50)},
		})
	}
	b := &rebuildBackend{
		job:    RebuildJob{ID: primitive.NewObjectID(), OfficeID: primitive.NewObjectID(), Scope: "commissions", StartDate: "2026-07-01", EndDate: "2026-07-31"},
		orders: orders, targets: []WorkerTarget{{WorkerID: worker, Target1: 1}},
	}
	processed, err := NewProcessor(b).ProcessNext(context.Background())
	if err != nil || !processed || b.finishStatus != "completed" {
		t.Fatalf("processed=%v err=%v status=%s", processed, err, b.finishStatus)
	}
	if len(b.entries) != 0 || b.stats.Scanned != 0 || b.stats.Inserted != 0 {
		t.Fatalf("non-completed orders reached materialization: entries=%d stats=%+v", len(b.entries), b.stats)
	}
}

func TestOrderDateUsesBookingDateOnly(t *testing.T) {
	order := OrderSource{BookingInfo: OrderBookingInfo{Date: "2026-07-10"}}
	if got := orderDate(order); got != "2026-07-10" {
		t.Fatalf("orderDate()=%q, want booking date", got)
	}
	order.BookingInfo.Date = ""
	if got := orderDate(order); got != "" {
		t.Fatalf("orderDate()=%q, want no legacy fallback", got)
	}
}

func TestProcessorAssignsOrderToBookingDateWhenDatesDiffer(t *testing.T) {
	worker := primitive.NewObjectID()
	order := OrderSource{
		ID: primitive.NewObjectID(), BeauticianID: worker, Status: "completed",
		BookingInfo: OrderBookingInfo{Date: "2026-07-31"},
		Snapshot:    &CommissionSnapshot{OrderCost: float(10), SpecialCommission: float(1), GeneralCommission: float(0), UpgradeAddonCommission: float(0)},
	}
	b := &rebuildBackend{
		job:    RebuildJob{ID: primitive.NewObjectID(), OfficeID: primitive.NewObjectID(), Scope: "commissions", StartDate: "2026-07-01", EndDate: "2026-07-31"},
		orders: []OrderSource{order},
	}
	if _, err := NewProcessor(b).ProcessNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b.entries) != 1 || b.entries[0].ServiceDateKey != "2026-07-31" {
		t.Fatalf("order assigned using wrong period date: entries=%+v", b.entries)
	}
}

func TestProcessorOrderValidationAndBackendErrors(t *testing.T) {
	boom := errors.New("boom")
	base := RebuildJob{ID: primitive.NewObjectID(), OfficeID: primitive.NewObjectID(), Scope: "commissions", StartDate: "2026-07-01", EndDate: "2026-07-31"}
	for _, test := range []struct {
		name   string
		mutate func(*rebuildBackend)
	}{
		{"bad range", func(b *rebuildBackend) { b.job.StartDate = "bad" }},
		{"orders error", func(b *rebuildBackend) { b.ordersErr = boom }},
		{"targets error", func(b *rebuildBackend) { b.targetsErr = boom }},
		{"put error", func(b *rebuildBackend) {
			b.putErr = boom
			b.orders = []OrderSource{{ID: primitive.NewObjectID(), BeauticianID: primitive.NewObjectID(), Status: "completed", BookingInfo: OrderBookingInfo{Date: "2026-07-01"}, Snapshot: &CommissionSnapshot{OrderCost: float(1), SpecialCommission: float(1)}}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := &rebuildBackend{job: base}
			test.mutate(b)
			processed, err := NewProcessor(b).ProcessNext(context.Background())
			if !processed || err == nil || b.finishStatus != "failed" {
				t.Fatalf("processed=%v err=%v status=%s", processed, err, b.finishStatus)
			}
		})
	}

	worker := primitive.NewObjectID()
	b := &rebuildBackend{job: base, orders: []OrderSource{
		{ID: primitive.NewObjectID(), BeauticianID: worker, Status: "completed", BookingInfo: OrderBookingInfo{Date: "bad"}, Snapshot: &CommissionSnapshot{}},
		{ID: primitive.NewObjectID(), BeauticianID: worker, Status: "completed", BookingInfo: OrderBookingInfo{Date: "2026-07-02"}},
		{ID: primitive.NewObjectID(), BeauticianID: worker, Status: "completed", BookingInfo: OrderBookingInfo{Date: "2026-07-03"}, Snapshot: &CommissionSnapshot{OrderCost: float(math.NaN()), SpecialCommission: float(math.Inf(1)), GeneralCommission: float(2), UpgradeAddonCommission: float(0)}},
	}}
	if _, err := NewProcessor(b).ProcessNext(context.Background()); err != nil || b.finishStatus != "completed_with_issues" || len(b.entries) != 0 || b.stats.MissingSnapshots < 3 {
		t.Fatalf("err=%v status=%s entries=%d stats=%+v", err, b.finishStatus, len(b.entries), b.stats)
	}
}

func TestProcessorTripVariantsAndValidation(t *testing.T) {
	beautician, rider := primitive.NewObjectID(), primitive.NewObjectID()
	base := RebuildJob{ID: primitive.NewObjectID(), OfficeID: primitive.NewObjectID(), Scope: "petrol", StartDate: "2026-07-01", EndDate: "2026-07-31"}
	b := &rebuildBackend{job: base, trips: []TripSource{
		{ID: primitive.NewObjectID(), DriverBeauticianID: &beautician, RiderID: &rider, Date: "2026-07-01", Snapshot: &PayableSnapshot{CommissionPayable: float(3), PetrolPayable: float(4)}},
		{ID: primitive.NewObjectID(), IsSelfDrive: true, BeauticianID: &beautician, Date: "2026-07-02", Snapshot: &PayableSnapshot{PetrolPayable: float(5)}},
		{ID: primitive.NewObjectID(), RiderID: &rider, Date: "2026-07-03", Snapshot: &PayableSnapshot{PetrolPayable: float(0)}},
		{ID: primitive.NewObjectID(), Date: "bad"},
		{ID: primitive.NewObjectID(), Date: "2026-07-04"},
		{ID: primitive.NewObjectID(), RiderID: &rider, Date: "2026-07-05", Snapshot: &PayableSnapshot{PetrolPayable: float(math.NaN())}},
	}}
	if _, err := NewProcessor(b).ProcessNext(context.Background()); err != nil || len(b.entries) != 2 || b.finishStatus != "completed_with_issues" {
		t.Fatalf("err=%v entries=%d status=%s", err, len(b.entries), b.finishStatus)
	}
	if b.entries[0].WorkerType != "beautician" || b.entries[0].Component != ComponentPetrol {
		t.Fatalf("entry=%+v", b.entries[0])
	}

	b = &rebuildBackend{job: base, tripsErr: errors.New("trips")}
	if _, err := NewProcessor(b).ProcessNext(context.Background()); err == nil {
		t.Fatal("expected trip load failure")
	}
	b = &rebuildBackend{job: base, trips: []TripSource{{ID: primitive.NewObjectID(), RiderID: &rider, Date: "2026-07-01", Snapshot: &PayableSnapshot{PetrolPayable: float(1)}}}, putErr: errors.New("put")}
	if _, err := NewProcessor(b).ProcessNext(context.Background()); err == nil {
		t.Fatal("expected put failure")
	}
}

func TestEffectiveTripPayablesRepairsUnpaidRoundTripSnapshot(t *testing.T) {
	trip := TripSource{
		IsTwoWay: true, IsCommissionable: true, CommissionAmount: 27.11, AutoDistanceKM: 13.556,
		FareCalculation: TripFareCalculation{TripDistanceKM: 13.556, CalculatedFare: 42.97},
		Snapshot: &PayableSnapshot{
			PayableDistanceKM: float(13.56), CommissionPayable: float(13.56), PetrolPayable: float(42.97),
			PetrolCostPerLiter: float(95.08), StandardMileagePerLiter: float(30), CommissionRatePerKM: float(1),
		},
	}
	commission, petrol := effectiveTripPayables(trip)
	if commission == nil || *commission != 27.11 || petrol == nil || *petrol != 85.93 {
		t.Fatalf("commission=%v petrol=%v", commission, petrol)
	}
}

func TestEffectiveTripPayablesPreservesExplicitCommission(t *testing.T) {
	trip := TripSource{
		IsCommissionable: true, CommissionAmount: 19.75, AutoDistanceKM: 12,
		Snapshot: &PayableSnapshot{CommissionPayable: float(12), PetrolPayable: float(8)},
	}
	commission, _ := effectiveTripPayables(trip)
	if commission == nil || *commission != 19.75 {
		t.Fatalf("explicit commission was lost: %v", commission)
	}
}

func TestEffectiveTripPayablesProtectsPaidAndManualValues(t *testing.T) {
	paid := TripSource{
		IsTwoWay: true, IsCommissionable: true, AutoDistanceKM: 10,
		Snapshot: &PayableSnapshot{CommissionPayable: float(9), PetrolPayable: float(8), IsPaid: true},
	}
	commission, petrol := effectiveTripPayables(paid)
	if *commission != 9 || *petrol != 8 {
		t.Fatalf("paid snapshot changed: commission=%v petrol=%v", *commission, *petrol)
	}

	manual := TripSource{
		IsTwoWay: true, IsCommissionable: true, IsManualDistance: true, AutoDistanceKM: 10, ExtraKM: 5,
		FareCalculation: TripFareCalculation{TripDistanceKM: 12, PetrolCostPerLiter: 100, StandardMileagePerLiter: 25},
		Snapshot:        &PayableSnapshot{CommissionRatePerKM: float(1)},
	}
	commission, petrol = effectiveTripPayables(manual)
	if *commission != 12 || *petrol != 48 {
		t.Fatalf("manual distance was doubled: commission=%v petrol=%v", *commission, *petrol)
	}
}

func TestEffectiveTripPayablesReconstructsIncompletePaidSnapshot(t *testing.T) {
	trip := TripSource{
		IsCommissionable: true, CommissionAmount: 16, AutoDistanceKM: 15.98,
		FareCalculation: TripFareCalculation{PetrolCostPerLiter: 100, StandardMileagePerLiter: 40},
		Snapshot:        &PayableSnapshot{IsPaid: true},
	}
	commission, petrol := effectiveTripPayables(trip)
	if commission == nil || *commission != 16 || petrol == nil || *petrol != 39.95 {
		t.Fatalf("incomplete paid snapshot was not reconstructed: commission=%v petrol=%v", commission, petrol)
	}
}

func TestEffectiveTripPayablesUsesOfficeFallbackForImportedPaidSnapshot(t *testing.T) {
	trip := TripSource{
		IsTwoWay: true, IsCommissionable: true, CommissionAmount: 16, AutoDistanceKM: 7.99,
		OfficePetrolCostPerLiter: 110.94, OfficeStandardMileagePerLiter: 35,
		Snapshot: &PayableSnapshot{IsPaid: true},
	}
	commission, petrol := effectiveTripPayables(trip)
	if commission == nil || *commission != 16 || petrol == nil || *petrol != 50.65 {
		t.Fatalf("office fallback did not match static report: commission=%v petrol=%v", commission, petrol)
	}

	trip.Snapshot.PetrolPayable = float(9)
	_, petrol = effectiveTripPayables(trip)
	if petrol == nil || *petrol != 9 {
		t.Fatalf("paid snapshot was overwritten by office fallback: petrol=%v", petrol)
	}
}

func TestProcessorDoesNotSettleCommissionFromLegacyTripPaidFlag(t *testing.T) {
	rider := primitive.NewObjectID()
	job := RebuildJob{ID: primitive.NewObjectID(), OfficeID: primitive.NewObjectID(), Scope: "all", StartDate: "2026-07-01", EndDate: "2026-07-31"}
	b := &rebuildBackend{job: job, trips: []TripSource{{
		ID: primitive.NewObjectID(), RiderID: &rider, Date: "2026-07-01",
		Snapshot: &PayableSnapshot{CommissionPayable: float(10), PetrolPayable: float(20), IsPaid: true},
	}}}
	if _, err := NewProcessor(b).ProcessNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b.entries) != 2 {
		t.Fatalf("entries=%#v", b.entries)
	}
	for _, entry := range b.entries {
		if entry.Component == ComponentTripCommission && (entry.Status != StatusOpen || entry.SettledAmountPaise != 0) {
			t.Fatalf("legacy petrol flag settled commission: %#v", entry)
		}
		if entry.Component == ComponentPetrol && (entry.Status != StatusSettled || entry.SettledAmountPaise != 2000) {
			t.Fatalf("legacy petrol settlement was lost: %#v", entry)
		}
	}
}

func TestProcessorConflictsAndHelpers(t *testing.T) {
	job := RebuildJob{OfficeID: primitive.NewObjectID(), RequestedBy: primitive.NewObjectID()}
	entry := sourceEntry(job, primitive.NewObjectID(), primitive.NewObjectID(), "rider", "2026-07-01", ComponentPetrol, BucketPetrol, 123, false)
	entry.ID = primitive.NewObjectID()
	b := &rebuildBackend{stored: entry, created: false}
	stats := RebuildStats{}
	if err := NewProcessor(b).put(context.Background(), entry, &stats); err != nil || stats.Unchanged != 1 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	b.stored.AmountPaise++
	if err := NewProcessor(b).put(context.Background(), entry, &stats); err != nil || stats.Conflicts != 1 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	if sourceCollection(ComponentLeaderboardBonus) != "leaderboards" || sourceCollection(ComponentTripCommission) != "trips" || sourceCollection(ComponentGeneralCommission) != "orders" {
		t.Fatal("wrong source collection")
	}
	if workerMonthKey(entry.WorkerID, "x") == "" {
		t.Fatal("worker month key empty")
	}
	order := OrderSource{}
	order.BookingInfo.Date = "2026-07-01"
	if orderDate(order) != "2026-07-01" {
		t.Fatal("booking date fallback failed")
	}
	if _, _, ok := tripWorker(TripSource{}); ok {
		t.Fatal("unexpected worker")
	}
	if sameConfigurationSnapshot(map[string]interface{}{"x": 1}, nil) || sameConfigurationSnapshot(map[string]interface{}{"x": 1}, map[string]interface{}{"y": 1}) {
		t.Fatal("different maps matched")
	}
	if sameConfigurationValue(1, "1") || !sameConfigurationValue("x", "x") {
		t.Fatal("configuration comparison wrong")
	}
	if sameConfigurationValue([]int{1}, "not-a-slice") ||
		sameConfigurationValue([]int{1}, []int{1, 2}) ||
		sameConfigurationValue([]int{1}, []int{2}) {
		t.Fatal("different configuration slices matched")
	}
	if items, ok := configurationSlice(nil); ok || items != nil {
		t.Fatal("nil must not be a configuration slice")
	}
	for _, value := range []interface{}{int(1), int32(1), int64(1), float32(1), float64(1)} {
		if number, ok := numericConfigurationValue(value); !ok || number != 1 {
			t.Fatalf("value=%T", value)
		}
	}
	if _, ok := numericConfigurationValue("1"); ok {
		t.Fatal("string is numeric")
	}
}

func TestProcessorLeaderboardValidationAndErrors(t *testing.T) {
	boom := errors.New("boom")
	job := RebuildJob{ID: primitive.NewObjectID(), OfficeID: primitive.NewObjectID(), Scope: "leaderboards", StartDate: "2026-07-01", EndDate: "2026-07-31"}
	validB := BeauticianLeaderboardSource{WorkerID: primitive.NewObjectID(), Revenue: 1, OrderCount: 1}
	validR := RiderLeaderboardSource{WorkerID: primitive.NewObjectID(), WorkerType: "rider", TripCount: 1, TotalDistanceKM: 1}
	tests := []struct {
		name string
		b    *rebuildBackend
	}{
		{"beautician load", &rebuildBackend{job: job, beauticiansErr: boom}},
		{"rider load", &rebuildBackend{job: job, ridersErr: boom}},
		{"prizes load", &rebuildBackend{job: job, prizesErr: boom}},
		{"invalid beautician", &rebuildBackend{job: job, beauticians: []BeauticianLeaderboardSource{{Revenue: -1}}}},
		{"duplicate beautician", &rebuildBackend{job: job, beauticians: []BeauticianLeaderboardSource{validB, validB}}},
		{"invalid rider", &rebuildBackend{job: job, riders: []RiderLeaderboardSource{{WorkerID: primitive.NewObjectID(), WorkerType: "other"}}}},
		{"duplicate rider", &rebuildBackend{job: job, riders: []RiderLeaderboardSource{validR, validR}}},
		{"award put", &rebuildBackend{job: job, beauticians: []BeauticianLeaderboardSource{validB}, prizes: LeaderboardPrizes{Beautician: []float64{1}}, putErr: boom}},
		{"rider award put", &rebuildBackend{job: job, riders: []RiderLeaderboardSource{validR}, prizes: LeaderboardPrizes{Rider: []float64{1}}, putErr: boom}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewProcessor(test.b).ProcessNext(context.Background()); err == nil {
				t.Fatal("expected failure")
			}
		})
	}
	stats := RebuildStats{}
	if err := NewProcessor(&rebuildBackend{}).putLeaderboardAward(context.Background(), job, leaderboardAwardZero(), "rider", "rider", leaderboardConfiguration{}, &stats); err != nil {
		t.Fatal(err)
	}
}

func leaderboardAwardZero() leaderboard.Award { return leaderboard.Award{} }
