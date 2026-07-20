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
// monetary source of truth. The reporting service reloads the order/trip and
// its persisted snapshot from Mongo while processing the queued rebuild.
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
	QueueRebuild(context.Context, RebuildJob) (RebuildJob, bool, error)
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

func (p *SourceEventProcessor) ProcessJSON(ctx context.Context, payload []byte) (RebuildJob, bool, error) {
	var event SourceEvent
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		return RebuildJob{}, false, permanentSourceEventError("invalid source event JSON: %v", err)
	}
	return p.Process(ctx, event)
}

func (p *SourceEventProcessor) Process(ctx context.Context, event SourceEvent) (RebuildJob, bool, error) {
	officeID, actorID, err := validateSourceEvent(event)
	if err != nil {
		return RebuildJob{}, false, err
	}
	closed, err := p.backend.HasClosedPeriodOverlap(ctx, officeID, event.ServiceDate, event.ServiceDate)
	if err != nil {
		return RebuildJob{}, false, fmt.Errorf("check closed earnings periods: %w", err)
	}
	if closed {
		return RebuildJob{}, false, permanentSourceEventError("source event targets a closed earnings period")
	}

	// A commissions rebuild materializes order commissions and both trip
	// commission/petrol components. It intentionally excludes leaderboard
	// awards, whose ranking needs an explicitly closed comparison period.
	return p.backend.QueueRebuild(ctx, RebuildJob{
		OfficeID:       officeID,
		StartDate:      event.ServiceDate,
		EndDate:        event.ServiceDate,
		Scope:          "commissions",
		IdempotencyKey: "source-event:" + event.EventID,
		RequestedBy:    actorID,
	})
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
