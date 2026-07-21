package earnings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	SourceEventType          = "earnings.source.changed"
	SourceEventSchemaVersion = 1
)

// SourceEvent is deliberately a notification, not a second copy of the
// monetary source of truth. The reporting service reloads the exact order/trip
// and its persisted snapshot from Mongo before direct ledger materialization.
type SourceEvent struct {
	EventID       string `json:"event_id"`
	EventType     string `json:"event_type"`
	SchemaVersion int    `json:"schema_version"`
	SourceType    string `json:"source_type"`
	SourceID      string `json:"source_id"`
	SourceVersion string `json:"source_version"`
	OfficeID      string `json:"office_id"`
	ServiceDate   string `json:"service_date"`
	ActorID       string `json:"actor_id,omitempty"`
	OccurredAt    string `json:"occurred_at"`
}

type SourceEventBackend interface {
	HasClosedPeriodOverlap(context.Context, primitive.ObjectID, string, string) (bool, error)
	LoadOrderSource(context.Context, primitive.ObjectID) (OrderSource, error)
	LoadTripSource(context.Context, primitive.ObjectID) (TripSource, error)
	LoadOrderSources(context.Context, primitive.ObjectID, string, string) ([]OrderSource, error)
	LoadWorkerTargets(context.Context, primitive.ObjectID) ([]WorkerTarget, error)
	LoadTarget2Bonus(context.Context, primitive.ObjectID) (float64, error)
	PutSourceEntry(context.Context, LedgerEntry) (LedgerEntry, bool, error)
}

type SourceEventResult struct {
	SourceType string
	SourceID   primitive.ObjectID
	Stats      RebuildStats
	Ignored    bool
}

// PermanentSourceEventError identifies messages that cannot become valid by
// retrying. Consumers may dead-letter and commit these messages; store errors
// remain retryable and must not be committed.
type PermanentSourceEventError struct{ Err error }

func (e *PermanentSourceEventError) Error() string { return e.Err.Error() }
func (e *PermanentSourceEventError) Unwrap() error { return e.Err }

func IsPermanentSourceEventError(err error) bool {
	var target *PermanentSourceEventError
	return errors.As(err, &target)
}

type SourceEventProcessor struct{ backend SourceEventBackend }

func NewSourceEventProcessor(backend SourceEventBackend) *SourceEventProcessor {
	return &SourceEventProcessor{backend: backend}
}

func (p *SourceEventProcessor) ProcessJSON(ctx context.Context, payload []byte) (SourceEventResult, bool, error) {
	var event SourceEvent
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		return SourceEventResult{}, false, permanentSourceEventError("invalid source event JSON: %v", err)
	}
	return p.Process(ctx, event)
}

func (p *SourceEventProcessor) Process(ctx context.Context, event SourceEvent) (SourceEventResult, bool, error) {
	officeID, actorID, err := validateSourceEvent(event)
	if err != nil {
		return SourceEventResult{}, false, err
	}
	sourceID, _ := primitive.ObjectIDFromHex(event.SourceID)
	result := SourceEventResult{SourceType: event.SourceType, SourceID: sourceID}
	closed, err := p.backend.HasClosedPeriodOverlap(ctx, officeID, event.ServiceDate, event.ServiceDate)
	if err != nil {
		return result, false, fmt.Errorf("check closed earnings periods: %w", err)
	}
	if closed {
		return result, false, permanentSourceEventError("source event targets a closed earnings period")
	}
	job := RebuildJob{OfficeID: officeID, StartDate: event.ServiceDate, EndDate: event.ServiceDate, Scope: "commissions", RequestedBy: actorID}
	if event.SourceType == "order" {
		err = p.processOrder(ctx, event, job, &result)
	} else {
		err = p.processTrip(ctx, event, job, &result)
	}
	if err == nil && result.Stats.Conflicts > 0 {
		err = permanentSourceEventError("source event conflicts with %d immutable ledger entries", result.Stats.Conflicts)
	}
	return result, result.Stats.Inserted > 0, err
}

func (p *SourceEventProcessor) processOrder(ctx context.Context, event SourceEvent, job RebuildJob, result *SourceEventResult) error {
	order, err := p.backend.LoadOrderSource(ctx, result.SourceID)
	if err != nil {
		return fmt.Errorf("load order source: %w", err)
	}
	if order.ID != result.SourceID {
		return permanentSourceEventError("loaded order does not match event source_id")
	}
	if err := validateReloadedSource(event, order.OfficeID, orderDate(order), order.Status == "completed" && !order.IsDeleted, order.Snapshot != nil, snapshotTime(order.Snapshot)); err != nil {
		if errors.Is(err, errStaleSourceEvent) {
			result.Ignored = true
			return nil
		}
		return err
	}
	monthStart, monthEnd, _ := coveringMonths(event.ServiceDate, event.ServiceDate)
	orders, err := p.backend.LoadOrderSources(ctx, job.OfficeID, monthStart, monthEnd)
	if err != nil {
		return fmt.Errorf("load monthly order context: %w", err)
	}
	targets, err := p.backend.LoadWorkerTargets(ctx, job.OfficeID)
	if err != nil {
		return fmt.Errorf("load worker targets: %w", err)
	}
	target1, target2 := float64(0), float64(0)
	for _, target := range targets {
		if target.WorkerID.IsZero() || !validMoney(target.Target1) || !validMoney(target.Target2) {
			return permanentSourceEventError("worker targets must be finite non-negative amounts")
		}
		if target.WorkerID == order.BeauticianID {
			target1 = target.Target1
			target2 = target.Target2
		}
	}
	revenue, valid := monthlyRevenueForWorker(orders, order.BeauticianID, event.ServiceDate[:7])
	result.Stats.Scanned = 1
	if !valid {
		return permanentSourceEventError("monthly order context contains an invalid snapshot")
	}
	components := []struct {
		component Component
		amount    *float64
	}{
		{ComponentSpecialCommission, order.Snapshot.SpecialCommission},
		{ComponentUpgradeCommission, order.Snapshot.UpgradeAddonCommission},
	}
	for _, item := range components {
		if item.amount == nil || !validMoney(*item.amount) {
			result.Stats.MissingSnapshots++
			continue
		}
		if *item.amount == 0 {
			continue
		}
		if err := putLedgerEntry(ctx, p.backend, sourceEntry(job, order.ID, order.BeauticianID, "beautician", event.ServiceDate, item.component, BucketCommission, moneyToPaise(*item.amount), order.Snapshot.IsPaid), &result.Stats); err != nil {
			return err
		}
	}
	// General commission is gated by the worker's complete calendar-month
	// revenue. The event that crosses target 1 therefore makes earlier orders
	// eligible too. Materialize just this dependent worker-month set instead of
	// scheduling a broad day rebuild, otherwise those earlier orders would be
	// permanently omitted from the authoritative report.
	if revenue >= target1 {
		for _, monthlyOrder := range orders {
			serviceDate := orderDate(monthlyOrder)
			if monthlyOrder.OfficeID != job.OfficeID || monthlyOrder.BeauticianID != order.BeauticianID || monthlyOrder.Status != "completed" || monthlyOrder.IsDeleted || !strings.HasPrefix(serviceDate, event.ServiceDate[:7]+"-") {
				continue
			}
			if monthlyOrder.ID.IsZero() || !validSourceDate(serviceDate) || monthlyOrder.Snapshot == nil || monthlyOrder.Snapshot.GeneralCommission == nil || !validMoney(*monthlyOrder.Snapshot.GeneralCommission) {
				result.Stats.MissingSnapshots++
				continue
			}
			if *monthlyOrder.Snapshot.GeneralCommission == 0 {
				continue
			}
			if err := putLedgerEntry(ctx, p.backend, sourceEntry(job, monthlyOrder.ID, monthlyOrder.BeauticianID, "beautician", serviceDate, ComponentGeneralCommission, BucketCommission, moneyToPaise(*monthlyOrder.Snapshot.GeneralCommission), monthlyOrder.Snapshot.IsPaid), &result.Stats); err != nil {
				return err
			}
		}
	}
	if target2 > 0 && revenue >= target2 {
		bonus, err := p.backend.LoadTarget2Bonus(ctx, job.OfficeID)
		if err != nil {
			return fmt.Errorf("load target 2 bonus: %w", err)
		}
		if !validMoney(bonus) {
			return permanentSourceEventError("monthly target 2 bonus must be a finite non-negative amount")
		}
		if bonus > 0 {
			month := event.ServiceDate[:7]
			entry := LedgerEntry{
				OfficeID: job.OfficeID, WorkerID: order.BeauticianID, WorkerType: "beautician", ServiceDateKey: monthEndDate(event.ServiceDate),
				Component: ComponentTargetBonus, SettlementBucket: BucketCommission, AmountPaise: moneyToPaise(bonus), Status: StatusOpen,
				SourceType: "targets", CalculationVersion: 1, CreatedBy: job.RequestedBy,
				IdempotencyKey:        fmt.Sprintf("target_bonus:%s:%s:%s:v1", job.OfficeID.Hex(), order.BeauticianID.Hex(), month),
				ConfigurationSnapshot: map[string]interface{}{"target_month": month, "monthly_target2": target2, "monthly_revenue": revenue, "monthly_target2_bonus": bonus},
			}
			if err := putLedgerEntry(ctx, p.backend, entry, &result.Stats); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *SourceEventProcessor) processTrip(ctx context.Context, event SourceEvent, job RebuildJob, result *SourceEventResult) error {
	trip, err := p.backend.LoadTripSource(ctx, result.SourceID)
	if err != nil {
		return fmt.Errorf("load trip source: %w", err)
	}
	if trip.ID != result.SourceID {
		return permanentSourceEventError("loaded trip does not match event source_id")
	}
	if err := validateReloadedSource(event, trip.OfficeID, trip.Date, tripEligible(trip), trip.Snapshot != nil, payableSnapshotTime(trip.Snapshot)); err != nil {
		if errors.Is(err, errStaleSourceEvent) {
			result.Ignored = true
			return nil
		}
		return err
	}
	result.Stats.Scanned = 1
	workerID, workerType, ok := tripWorker(trip)
	if !ok {
		return permanentSourceEventError("trip has no payable worker")
	}
	commissionPayable, petrolPayable := effectiveTripPayables(trip)
	items := []struct {
		component Component
		bucket    SettlementBucket
		amount    *float64
	}{
		{ComponentTripCommission, BucketCommission, commissionPayable},
		{ComponentPetrol, BucketPetrol, petrolPayable},
	}
	for _, item := range items {
		if item.amount == nil || !validMoney(*item.amount) {
			result.Stats.MissingSnapshots++
			continue
		}
		if *item.amount == 0 {
			continue
		}
		if err := putLedgerEntry(ctx, p.backend, sourceEntry(job, trip.ID, workerID, workerType, event.ServiceDate, item.component, item.bucket, moneyToPaise(*item.amount), trip.Snapshot.IsPaid), &result.Stats); err != nil {
			return err
		}
	}
	return nil
}

var errStaleSourceEvent = errors.New("stale source event")

func validateReloadedSource(event SourceEvent, officeID primitive.ObjectID, serviceDate string, eligible, hasSnapshot bool, capturedAt time.Time) error {
	if officeID.Hex() != event.OfficeID {
		return permanentSourceEventError("source office does not match event office")
	}
	if serviceDate != event.ServiceDate {
		return permanentSourceEventError("source service date does not match event service date")
	}
	if !eligible {
		return permanentSourceEventError("source is not eligible for earnings")
	}
	if !hasSnapshot || capturedAt.IsZero() {
		return permanentSourceEventError("source snapshot or captured_at is missing")
	}
	eventVersion, _ := time.Parse(time.RFC3339, event.SourceVersion)
	if capturedAt.Equal(eventVersion) {
		return nil
	}
	if capturedAt.After(eventVersion) {
		return errStaleSourceEvent
	}
	return fmt.Errorf("source snapshot version %s is older than event version %s", capturedAt.UTC().Format(time.RFC3339Nano), event.SourceVersion)
}

func snapshotTime(snapshot *CommissionSnapshot) time.Time {
	if snapshot == nil {
		return time.Time{}
	}
	return snapshot.CapturedAt
}
func payableSnapshotTime(snapshot *PayableSnapshot) time.Time {
	if snapshot == nil {
		return time.Time{}
	}
	return snapshot.CapturedAt
}

func monthlyRevenueForWorker(orders []OrderSource, workerID primitive.ObjectID, month string) (float64, bool) {
	revenue := float64(0)
	for _, order := range orders {
		if order.Status != "completed" || order.IsDeleted || order.BeauticianID != workerID || !strings.HasPrefix(orderDate(order), month+"-") {
			continue
		}
		if order.Snapshot == nil || order.Snapshot.OrderCost == nil || !validMoney(*order.Snapshot.OrderCost) {
			return 0, false
		}
		revenue += *order.Snapshot.OrderCost
	}
	return revenue, true
}

func tripEligible(trip TripSource) bool {
	if trip.IsDeleted {
		return false
	}
	return trip.Status == "completed" || trip.KanbanState == "trip_completed" || trip.KanbanState == "fare_calculation_pending" || trip.KanbanState == "completed"
}

func validateSourceEvent(event SourceEvent) (primitive.ObjectID, primitive.ObjectID, error) {
	if strings.TrimSpace(event.EventID) == "" || len(event.EventID) > 200 {
		return primitive.NilObjectID, primitive.NilObjectID, permanentSourceEventError("event_id is required and must not exceed 200 characters")
	}
	if event.EventType != SourceEventType {
		return primitive.NilObjectID, primitive.NilObjectID, permanentSourceEventError("unsupported event_type %q", event.EventType)
	}
	if event.SchemaVersion != SourceEventSchemaVersion {
		return primitive.NilObjectID, primitive.NilObjectID, permanentSourceEventError("unsupported schema_version %d", event.SchemaVersion)
	}
	if event.SourceType != "order" && event.SourceType != "trip" {
		return primitive.NilObjectID, primitive.NilObjectID, permanentSourceEventError("source_type must be order or trip")
	}
	if _, err := primitive.ObjectIDFromHex(event.SourceID); err != nil {
		return primitive.NilObjectID, primitive.NilObjectID, permanentSourceEventError("source_id must be a Mongo ObjectID")
	}
	if _, err := time.Parse(time.RFC3339, event.SourceVersion); err != nil {
		return primitive.NilObjectID, primitive.NilObjectID, permanentSourceEventError("source_version must be the snapshot captured_at RFC3339 timestamp")
	}
	officeID, err := primitive.ObjectIDFromHex(event.OfficeID)
	if err != nil {
		return primitive.NilObjectID, primitive.NilObjectID, permanentSourceEventError("office_id must be a Mongo ObjectID")
	}
	if _, err := time.Parse("2006-01-02", event.ServiceDate); err != nil {
		return primitive.NilObjectID, primitive.NilObjectID, permanentSourceEventError("service_date must use YYYY-MM-DD")
	}
	if _, err := time.Parse(time.RFC3339, event.OccurredAt); err != nil {
		return primitive.NilObjectID, primitive.NilObjectID, permanentSourceEventError("occurred_at must be an RFC3339 timestamp")
	}
	actorID := primitive.NilObjectID
	if event.ActorID != "" {
		actorID, err = primitive.ObjectIDFromHex(event.ActorID)
		if err != nil {
			return primitive.NilObjectID, primitive.NilObjectID, permanentSourceEventError("actor_id must be a Mongo ObjectID")
		}
	}
	return officeID, actorID, nil
}

func permanentSourceEventError(format string, args ...interface{}) error {
	return &PermanentSourceEventError{Err: fmt.Errorf(format, args...)}
}
