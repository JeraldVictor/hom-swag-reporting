package earnings

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type sourceEventBackendStub struct {
	closed    bool
	closedErr error
	queueErr  error
	queued    []RebuildJob
	stored    map[string]RebuildJob
}

func (b *sourceEventBackendStub) HasClosedPeriodOverlap(context.Context, primitive.ObjectID, string, string) (bool, error) {
	return b.closed, b.closedErr
}

func (b *sourceEventBackendStub) QueueRebuild(_ context.Context, job RebuildJob) (RebuildJob, bool, error) {
	if b.queueErr != nil {
		return RebuildJob{}, false, b.queueErr
	}
	if existing, ok := b.stored[job.IdempotencyKey]; ok {
		return existing, false, nil
	}
	if b.stored == nil {
		b.stored = map[string]RebuildJob{}
	}
	job.ID = primitive.NewObjectID()
	b.stored[job.IdempotencyKey] = job
	b.queued = append(b.queued, job)
	return job, true, nil
}

func validSourceEvent() SourceEvent {
	return SourceEvent{
		EventID: "018f-event", EventType: SourceEventType, SchemaVersion: SourceEventSchemaVersion,
		SourceType: "order", SourceID: primitive.NewObjectID().Hex(), OfficeID: primitive.NewObjectID().Hex(),
		SourceVersion: "2026-07-21T12:29:59Z", ServiceDate: "2026-07-21", ActorID: primitive.NewObjectID().Hex(), OccurredAt: "2026-07-21T12:30:00Z",
	}
}

func TestSourceEventQueuesIdempotentRebuild(t *testing.T) {
	backend := &sourceEventBackendStub{}
	processor := NewSourceEventProcessor(backend)
	event := validSourceEvent()

	first, created, err := processor.Process(context.Background(), event)
	if err != nil || !created {
		t.Fatalf("first event: created=%v err=%v", created, err)
	}
	second, created, err := processor.Process(context.Background(), event)
	if err != nil || created || first.ID != second.ID || len(backend.queued) != 1 {
		t.Fatalf("duplicate event was not idempotent: created=%v jobs=%d err=%v", created, len(backend.queued), err)
	}
	if first.Scope != "commissions" || first.StartDate != event.ServiceDate || first.EndDate != event.ServiceDate || first.IdempotencyKey != "source-event:"+event.EventID {
		t.Fatalf("unexpected rebuild: %+v", first)
	}
}

func TestSourceEventRejectsInvalidAndClosedMessagesPermanently(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SourceEvent)
	}{
		{"missing event id", func(e *SourceEvent) { e.EventID = "" }},
		{"event id too long", func(e *SourceEvent) { e.EventID = string(make([]byte, 201)) }},
		{"wrong event type", func(e *SourceEvent) { e.EventType = "order.updated" }},
		{"wrong version", func(e *SourceEvent) { e.SchemaVersion = 2 }},
		{"wrong source type", func(e *SourceEvent) { e.SourceType = "settlement" }},
		{"bad source id", func(e *SourceEvent) { e.SourceID = "no" }},
		{"bad source version", func(e *SourceEvent) { e.SourceVersion = "v2" }},
		{"bad office id", func(e *SourceEvent) { e.OfficeID = "no" }},
		{"bad actor id", func(e *SourceEvent) { e.ActorID = "no" }},
		{"bad date", func(e *SourceEvent) { e.ServiceDate = "21-07-2026" }},
		{"bad timestamp", func(e *SourceEvent) { e.OccurredAt = "yesterday" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := validSourceEvent()
			test.mutate(&event)
			_, _, err := NewSourceEventProcessor(&sourceEventBackendStub{}).Process(context.Background(), event)
			if !IsPermanentSourceEventError(err) {
				t.Fatalf("expected permanent error, got %v", err)
			}
		})
	}

	_, _, err := NewSourceEventProcessor(&sourceEventBackendStub{closed: true}).Process(context.Background(), validSourceEvent())
	if !IsPermanentSourceEventError(err) {
		t.Fatalf("closed period should be permanent, got %v", err)
	}
}

func TestSourceEventStoreFailuresRemainRetryable(t *testing.T) {
	want := errors.New("mongo unavailable")
	for _, backend := range []*sourceEventBackendStub{{closedErr: want}, {queueErr: want}} {
		_, _, err := NewSourceEventProcessor(backend).Process(context.Background(), validSourceEvent())
		if err == nil || IsPermanentSourceEventError(err) {
			t.Fatalf("store failure should remain retryable: %v", err)
		}
	}
}

func TestSourceEventJSONRejectsUnknownFields(t *testing.T) {
	payload := []byte(`{"event_id":"e","event_type":"earnings.source.changed","schema_version":1,"source_type":"order","source_id":"000000000000000000000001","source_version":"2026-07-21T00:00:00Z","office_id":"000000000000000000000002","service_date":"2026-07-21","occurred_at":"2026-07-21T00:00:00Z","money":42}`)
	_, _, err := NewSourceEventProcessor(&sourceEventBackendStub{}).ProcessJSON(context.Background(), payload)
	if !IsPermanentSourceEventError(err) {
		t.Fatalf("unknown payload fields must be rejected, got %v", err)
	}
}

func TestSourceEventJSONSuccessAndPermanentErrorDetails(t *testing.T) {
	backend := &sourceEventBackendStub{}
	payload := sourceEventPayload(t)
	if _, created, err := NewSourceEventProcessor(backend).ProcessJSON(context.Background(), payload); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	base := errors.New("contract")
	err := &PermanentSourceEventError{Err: base}
	if err.Error() != "contract" || !errors.Is(err, base) {
		t.Fatalf("permanent error does not expose cause: %v", err)
	}
}
