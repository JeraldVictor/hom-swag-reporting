package earnings

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

var sourceTestOffice = primitive.NewObjectID()
var sourceTestWorker = primitive.NewObjectID()
var sourceTestID = primitive.NewObjectID()
var sourceTestCaptured = time.Date(2026, 7, 21, 12, 29, 59, 0, time.UTC)

type sourceEventBackendStub struct {
	closed                                                       bool
	closedErr, loadErr, monthlyErr, targetsErr, bonusErr, putErr error
	bonus                                                        float64
	invalidTarget                                                bool
	putFailAt, putCalls                                          int
	order                                                        OrderSource
	trip                                                         TripSource
	orders                                                       []OrderSource
	entries                                                      map[string]LedgerEntry
}

func defaultSourceBackend() *sourceEventBackendStub {
	cost, special, general, upgrade := 100.0, 10.0, 20.0, 5.0
	order := OrderSource{ID: sourceTestID, OfficeID: sourceTestOffice, BeauticianID: sourceTestWorker, Status: "completed", BookingInfo: OrderBookingInfo{Date: "2026-07-21"}, Snapshot: &CommissionSnapshot{OrderCost: &cost, SpecialCommission: &special, GeneralCommission: &general, UpgradeAddonCommission: &upgrade, CapturedAt: sourceTestCaptured}}
	return &sourceEventBackendStub{order: order, orders: []OrderSource{order}}
}
func (b *sourceEventBackendStub) HasClosedPeriodOverlap(context.Context, primitive.ObjectID, string, string) (bool, error) {
	return b.closed, b.closedErr
}
func (b *sourceEventBackendStub) LoadOrderSource(context.Context, primitive.ObjectID) (OrderSource, error) {
	if b.loadErr != nil {
		return OrderSource{}, b.loadErr
	}
	return b.order, nil
}
func (b *sourceEventBackendStub) LoadTripSource(context.Context, primitive.ObjectID) (TripSource, error) {
	if b.loadErr != nil {
		return TripSource{}, b.loadErr
	}
	return b.trip, nil
}
func (b *sourceEventBackendStub) LoadOrderSources(context.Context, primitive.ObjectID, string, string) ([]OrderSource, error) {
	if b.monthlyErr != nil {
		return nil, b.monthlyErr
	}
	return b.orders, nil
}
func (b *sourceEventBackendStub) LoadWorkerTargets(context.Context, primitive.ObjectID) ([]WorkerTarget, error) {
	if b.invalidTarget {
		return []WorkerTarget{{}}, nil
	}
	return []WorkerTarget{{WorkerID: sourceTestWorker, Target1: 50, Target2: 200}}, b.targetsErr
}
func (b *sourceEventBackendStub) LoadTarget2Bonus(context.Context, primitive.ObjectID) (float64, error) {
	return b.bonus, b.bonusErr
}
func (b *sourceEventBackendStub) PutSourceEntry(_ context.Context, e LedgerEntry) (LedgerEntry, bool, error) {
	b.putCalls++
	if b.putFailAt > 0 && b.putCalls == b.putFailAt {
		return LedgerEntry{}, false, errors.New("mongo")
	}
	if b.putErr != nil {
		return LedgerEntry{}, false, b.putErr
	}
	if b.entries == nil {
		b.entries = map[string]LedgerEntry{}
	}
	old, ok := b.entries[e.IdempotencyKey]
	if ok {
		return old, false, nil
	}
	b.entries[e.IdempotencyKey] = e
	return e, true, nil
}

func validSourceEvent() SourceEvent {
	return SourceEvent{EventID: "018f-event", EventType: SourceEventType, SchemaVersion: SourceEventSchemaVersion, SourceType: "order", SourceID: sourceTestID.Hex(), OfficeID: sourceTestOffice.Hex(), SourceVersion: sourceTestCaptured.Format(time.RFC3339), ServiceDate: "2026-07-21", ActorID: primitive.NewObjectID().Hex(), OccurredAt: "2026-07-21T12:30:00Z"}
}

func TestSourceEventMaterializesExactOrderIdempotently(t *testing.T) {
	b := defaultSourceBackend()
	p := NewSourceEventProcessor(b)
	e := validSourceEvent()
	first, created, err := p.Process(context.Background(), e)
	if err != nil || !created || first.Stats.Inserted != 3 {
		t.Fatalf("first=%+v created=%v err=%v", first, created, err)
	}
	second, created, err := p.Process(context.Background(), e)
	if err != nil || created || second.Stats.Unchanged != 3 || len(b.entries) != 3 {
		t.Fatalf("second=%+v created=%v err=%v", second, created, err)
	}
}

func TestSourceEventMaterializesExactTrip(t *testing.T) {
	b := defaultSourceBackend()
	commission, petrol := 30.0, 40.0
	b.trip = TripSource{ID: sourceTestID, OfficeID: sourceTestOffice, RiderID: &sourceTestWorker, Date: "2026-07-21", Status: "completed", Snapshot: &PayableSnapshot{CommissionPayable: &commission, PetrolPayable: &petrol, CapturedAt: sourceTestCaptured}}
	e := validSourceEvent()
	e.SourceType = "trip"
	r, created, err := NewSourceEventProcessor(b).Process(context.Background(), e)
	if err != nil || !created || r.Stats.Inserted != 2 {
		t.Fatalf("result=%+v created=%v err=%v", r, created, err)
	}
}

func TestSourceEventRejectsMismatchesAndIneligibleSources(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*sourceEventBackendStub, *SourceEvent)
	}{
		{"office", func(b *sourceEventBackendStub, e *SourceEvent) { b.order.OfficeID = primitive.NewObjectID() }},
		{"source id", func(b *sourceEventBackendStub, e *SourceEvent) { b.order.ID = primitive.NewObjectID() }},
		{"date", func(b *sourceEventBackendStub, e *SourceEvent) { b.order.BookingInfo.Date = "2026-07-20" }},
		{"status", func(b *sourceEventBackendStub, e *SourceEvent) { b.order.Status = "cancelled" }},
		{"deleted", func(b *sourceEventBackendStub, e *SourceEvent) { b.order.IsDeleted = true }},
		{"missing snapshot", func(b *sourceEventBackendStub, e *SourceEvent) { b.order.Snapshot = nil }},
		{"missing captured at", func(b *sourceEventBackendStub, e *SourceEvent) { b.order.Snapshot.CapturedAt = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := defaultSourceBackend()
			e := validSourceEvent()
			tt.mutate(b, &e)
			_, _, err := NewSourceEventProcessor(b).Process(context.Background(), e)
			if !IsPermanentSourceEventError(err) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestSourceEventCrossingTargetMaterializesEarlierMonthlyGeneralCommission(t *testing.T) {
	b := defaultSourceBackend()
	b.order.Snapshot.OrderCost = floatPointer(30)
	b.orders[0] = b.order
	earlier := b.order
	earlier.ID = primitive.NewObjectID()
	earlier.BookingInfo.Date = "2026-07-02"
	earlier.Snapshot = &CommissionSnapshot{OrderCost: floatPointer(30), GeneralCommission: floatPointer(7), CapturedAt: sourceTestCaptured}
	b.orders = append(b.orders, earlier)

	result, created, err := NewSourceEventProcessor(b).Process(context.Background(), validSourceEvent())
	key := "source:orders:" + earlier.ID.Hex() + ":" + string(ComponentGeneralCommission) + ":v1"
	if err != nil || !created || result.Stats.Inserted != 4 || b.entries[key].AmountPaise != 700 {
		t.Fatalf("result=%+v created=%v earlier=%+v err=%v", result, created, b.entries[key], err)
	}
}

func TestSourceEventOrderMaterializationBranches(t *testing.T) {
	t.Run("zero amounts and unrelated monthly rows", func(t *testing.T) {
		b := defaultSourceBackend()
		b.order.Snapshot.SpecialCommission = floatPointer(0)
		b.orders[0] = b.order
		unrelated := b.order
		unrelated.ID = primitive.NewObjectID()
		unrelated.OfficeID = primitive.NewObjectID()
		b.orders = append(b.orders, unrelated)
		zeroGeneral := b.order
		zeroGeneral.ID = primitive.NewObjectID()
		zeroGeneral.Snapshot = &CommissionSnapshot{OrderCost: floatPointer(10), GeneralCommission: floatPointer(0), CapturedAt: sourceTestCaptured}
		b.orders = append(b.orders, zeroGeneral)
		if _, _, err := NewSourceEventProcessor(b).Process(context.Background(), validSourceEvent()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("general commission store error", func(t *testing.T) {
		b := defaultSourceBackend()
		b.putFailAt = 3
		if _, _, err := NewSourceEventProcessor(b).Process(context.Background(), validSourceEvent()); err == nil || IsPermanentSourceEventError(err) {
			t.Fatalf("expected retryable error, got %v", err)
		}
	})
}

func TestSourceEventVersionOrdering(t *testing.T) {
	b := defaultSourceBackend()
	e := validSourceEvent()
	e.SourceVersion = sourceTestCaptured.Add(-time.Second).Format(time.RFC3339)
	r, created, err := NewSourceEventProcessor(b).Process(context.Background(), e)
	if err != nil || created || !r.Ignored || len(b.entries) != 0 {
		t.Fatalf("stale result=%+v err=%v", r, err)
	}
	e.SourceVersion = sourceTestCaptured.Add(time.Second).Format(time.RFC3339)
	_, _, err = NewSourceEventProcessor(b).Process(context.Background(), e)
	if err == nil || IsPermanentSourceEventError(err) {
		t.Fatalf("future source version should retry: %v", err)
	}
}

func TestSourceEventValidationClosedAndStoreErrors(t *testing.T) {
	mutations := []func(*SourceEvent){func(e *SourceEvent) { e.EventID = "" }, func(e *SourceEvent) { e.EventType = "bad" }, func(e *SourceEvent) { e.SchemaVersion = 2 }, func(e *SourceEvent) { e.SourceType = "bad" }, func(e *SourceEvent) { e.SourceID = "bad" }, func(e *SourceEvent) { e.SourceVersion = "bad" }, func(e *SourceEvent) { e.OfficeID = "bad" }, func(e *SourceEvent) { e.ActorID = "bad" }, func(e *SourceEvent) { e.ServiceDate = "bad" }, func(e *SourceEvent) { e.OccurredAt = "bad" }}
	for _, mutate := range mutations {
		e := validSourceEvent()
		mutate(&e)
		_, _, err := NewSourceEventProcessor(defaultSourceBackend()).Process(context.Background(), e)
		if !IsPermanentSourceEventError(err) {
			t.Fatalf("expected permanent: %v", err)
		}
	}
	b := defaultSourceBackend()
	b.closed = true
	_, _, err := NewSourceEventProcessor(b).Process(context.Background(), validSourceEvent())
	if !IsPermanentSourceEventError(err) {
		t.Fatalf("closed: %v", err)
	}
	for _, b = range []*sourceEventBackendStub{{closedErr: errors.New("mongo")}, func() *sourceEventBackendStub { x := defaultSourceBackend(); x.loadErr = errors.New("mongo"); return x }(), func() *sourceEventBackendStub { x := defaultSourceBackend(); x.putErr = errors.New("mongo"); return x }()} {
		_, _, err = NewSourceEventProcessor(b).Process(context.Background(), validSourceEvent())
		if err == nil || IsPermanentSourceEventError(err) {
			t.Fatalf("retryable: %v", err)
		}
	}
}

func TestSourceEventJSONContract(t *testing.T) {
	p := NewSourceEventProcessor(defaultSourceBackend())
	_, _, err := p.ProcessJSON(context.Background(), []byte(`{"event_id":"e","unknown":true}`))
	if !IsPermanentSourceEventError(err) {
		t.Fatalf("unknown: %v", err)
	}
	base := errors.New("contract")
	wrapped := &PermanentSourceEventError{Err: base}
	if wrapped.Error() != "contract" || !errors.Is(wrapped, base) {
		t.Fatal("unwrap")
	}
}

func TestSourceEventOrderFailuresTargetsAndConflict(t *testing.T) {
	want := errors.New("mongo")
	for _, mutate := range []func(*sourceEventBackendStub){
		func(b *sourceEventBackendStub) { b.monthlyErr = want },
		func(b *sourceEventBackendStub) { b.targetsErr = want },
		func(b *sourceEventBackendStub) { b.orders[0].Snapshot.OrderCost = nil },
		func(b *sourceEventBackendStub) {
			b.order.Snapshot.SpecialCommission = nil
			b.order.Snapshot.GeneralCommission = nil
			b.order.Snapshot.UpgradeAddonCommission = nil
		},
		func(b *sourceEventBackendStub) { b.invalidTarget = true },
	} {
		b := defaultSourceBackend()
		mutate(b)
		_, _, err := NewSourceEventProcessor(b).Process(context.Background(), validSourceEvent())
		if err == nil && b.monthlyErr != nil || err == nil && b.targetsErr != nil {
			t.Fatal("expected store error")
		}
	}
	b := defaultSourceBackend()
	b.bonus = 25
	b.order.Snapshot.OrderCost = floatPointer(250)
	b.orders[0] = b.order
	r, _, err := NewSourceEventProcessor(b).Process(context.Background(), validSourceEvent())
	if err != nil || r.Stats.Inserted != 4 {
		t.Fatalf("target bonus: %+v %v", r, err)
	}
	b2 := defaultSourceBackend()
	b2.bonus = -1
	b2.order.Snapshot.OrderCost = floatPointer(250)
	b2.orders[0] = b2.order
	if _, _, err = NewSourceEventProcessor(b2).Process(context.Background(), validSourceEvent()); !IsPermanentSourceEventError(err) {
		t.Fatalf("invalid bonus: %v", err)
	}
	b4 := defaultSourceBackend()
	b4.bonusErr = errors.New("mongo")
	b4.order.Snapshot.OrderCost = floatPointer(250)
	b4.orders[0] = b4.order
	if _, _, err = NewSourceEventProcessor(b4).Process(context.Background(), validSourceEvent()); err == nil {
		t.Fatal("bonus load error")
	}
	b5 := defaultSourceBackend()
	b5.bonus = 25
	b5.order.Snapshot.OrderCost = floatPointer(250)
	b5.orders[0] = b5.order
	b5.putFailAt = 4
	if _, _, err = NewSourceEventProcessor(b5).Process(context.Background(), validSourceEvent()); err == nil {
		t.Fatal("bonus put error")
	}
	b6 := defaultSourceBackend()
	zero := 0.0
	b6.order.Snapshot.SpecialCommission = &zero
	b6.order.Snapshot.UpgradeAddonCommission = &zero
	b6.order.Snapshot.GeneralCommission = &zero
	b6.orders[0] = b6.order
	if _, _, err = NewSourceEventProcessor(b6).Process(context.Background(), validSourceEvent()); err != nil {
		t.Fatalf("zero order components: %v", err)
	}
	b7 := defaultSourceBackend()
	irrelevant := b7.order
	irrelevant.ID = primitive.NewObjectID()
	irrelevant.Status = "cancelled"
	b7.orders = append(b7.orders, irrelevant)
	b7.putFailAt = 3
	if _, _, err = NewSourceEventProcessor(b7).Process(context.Background(), validSourceEvent()); err == nil {
		t.Fatal("general commission put error")
	}
	b3 := defaultSourceBackend()
	b3.entries = map[string]LedgerEntry{"source:orders:" + sourceTestID.Hex() + ":" + string(ComponentSpecialCommission) + ":v1": {AmountPaise: 1}}
	r, _, err = NewSourceEventProcessor(b3).Process(context.Background(), validSourceEvent())
	if !IsPermanentSourceEventError(err) || r.Stats.Conflicts != 1 {
		t.Fatalf("conflict=%+v err=%v", r, err)
	}
}

func TestSourceEventTripNegativePaths(t *testing.T) {
	commission, petrol := 30.0, 40.0
	base := func() *sourceEventBackendStub {
		b := defaultSourceBackend()
		b.trip = TripSource{ID: sourceTestID, OfficeID: sourceTestOffice, RiderID: &sourceTestWorker, Date: "2026-07-21", Status: "completed", Snapshot: &PayableSnapshot{CommissionPayable: &commission, PetrolPayable: &petrol, CapturedAt: sourceTestCaptured}}
		return b
	}
	e := validSourceEvent()
	e.SourceType = "trip"
	for _, mutate := range []func(*sourceEventBackendStub){func(b *sourceEventBackendStub) { b.trip.ID = primitive.NewObjectID() }, func(b *sourceEventBackendStub) { b.trip.RiderID = nil }, func(b *sourceEventBackendStub) { b.trip.IsDeleted = true }, func(b *sourceEventBackendStub) {
		b.trip.Snapshot.CommissionPayable = nil
		b.trip.Snapshot.PetrolPayable = nil
	}, func(b *sourceEventBackendStub) { b.putErr = errors.New("mongo") }} {
		b := base()
		mutate(b)
		_, _, err := NewSourceEventProcessor(b).Process(context.Background(), e)
		if b.putErr != nil && err == nil {
			t.Fatal("put error missing")
		}
	}
	b := base()
	b.loadErr = errors.New("mongo")
	if _, _, err := NewSourceEventProcessor(b).Process(context.Background(), e); err == nil {
		t.Fatal("trip load error")
	}
	b = base()
	e.SourceVersion = sourceTestCaptured.Add(-time.Second).Format(time.RFC3339)
	r, _, err := NewSourceEventProcessor(b).Process(context.Background(), e)
	if err != nil || !r.Ignored {
		t.Fatalf("stale trip: %+v %v", r, err)
	}
	e.SourceVersion = sourceTestCaptured.Format(time.RFC3339)
	b = base()
	zero := 0.0
	b.trip.Snapshot.CommissionPayable = &zero
	b.trip.Snapshot.PetrolPayable = &zero
	if _, created, err := NewSourceEventProcessor(b).Process(context.Background(), e); err != nil || created {
		t.Fatalf("zero created=%v err=%v", created, err)
	}
	if payableSnapshotTime(nil) != (time.Time{}) {
		t.Fatal("nil payable snapshot")
	}
	for _, trip := range []TripSource{{Status: "completed"}, {KanbanState: "trip_completed"}, {KanbanState: "fare_calculation_pending"}, {KanbanState: "completed"}, {}} {
		_ = tripEligible(trip)
	}
	other := defaultSourceBackend().order
	other.Status = "cancelled"
	if _, ok := monthlyRevenueForWorker([]OrderSource{other}, sourceTestWorker, "2026-07"); !ok {
		t.Fatal("irrelevant monthly order")
	}
}

func floatPointer(v float64) *float64 { return &v }
