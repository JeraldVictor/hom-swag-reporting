package earnings

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type sourceConsumerStub struct {
	message       kafka.Message
	fetchErr      error
	fetchCalls    int
	cancelOnFetch int
	commitErrors  []error
	commits       int
	cancel        context.CancelFunc
}

func (c *sourceConsumerStub) FetchMessage(ctx context.Context) (kafka.Message, error) {
	c.fetchCalls++
	if c.fetchErr != nil {
		if c.cancel != nil && (c.cancelOnFetch == 0 || c.fetchCalls >= c.cancelOnFetch) {
			c.cancel()
		}
		return kafka.Message{}, c.fetchErr
	}
	return c.message, nil
}

func (c *sourceConsumerStub) CommitMessages(_ context.Context, _ ...kafka.Message) error {
	c.commits++
	if len(c.commitErrors) > 0 {
		err := c.commitErrors[0]
		c.commitErrors = c.commitErrors[1:]
		if err != nil {
			return err
		}
	}
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}

func (c *sourceConsumerStub) StartMessageSpan(ctx context.Context, _ kafka.Message) (context.Context, trace.Span) {
	return otel.Tracer("earnings-source-test").Start(ctx, "consume")
}

type sourceDeadLetterStub struct {
	err   error
	sends int
}

func (d *sourceDeadLetterStub) SendMessage(context.Context, string, interface{}) error {
	d.sends++
	return d.err
}

func sourceEventPayload(t *testing.T) []byte {
	t.Helper()
	e := validSourceEvent()
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestSourceEventWorkerCommitsAcceptedAndPermanentMessages(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload []byte
		wantDLQ int
	}{
		{name: "accepted", payload: sourceEventPayload(t)},
		{name: "permanent", payload: []byte(`{"bad":true}`), wantDLQ: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			consumer := &sourceConsumerStub{message: kafka.Message{Key: []byte("source"), Value: test.payload}, cancel: cancel, commitErrors: []error{errors.New("commit once"), nil}}
			dlq := &sourceDeadLetterStub{}
			worker := NewSourceEventWorker(consumer, dlq, defaultSourceBackend())
			worker.wait = func(context.Context) bool { return true }
			worker.Start(ctx)
			if consumer.commits != 2 || dlq.sends != test.wantDLQ {
				t.Fatalf("commits=%d dlq=%d", consumer.commits, dlq.sends)
			}
		})
	}
}

func TestSourceEventWorkerRetriesWithoutCommit(t *testing.T) {
	for _, test := range []struct {
		name    string
		backend *sourceEventBackendStub
		dlq     *sourceDeadLetterStub
	}{
		{name: "store failure", backend: func() *sourceEventBackendStub { b := defaultSourceBackend(); b.loadErr = errors.New("mongo"); return b }(), dlq: &sourceDeadLetterStub{}},
		{name: "dead letter failure", backend: defaultSourceBackend(), dlq: &sourceDeadLetterStub{err: errors.New("kafka")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			payload := sourceEventPayload(t)
			if test.name == "dead letter failure" {
				payload = []byte(`{"bad":true}`)
			}
			consumer := &sourceConsumerStub{message: kafka.Message{Value: payload}, cancel: cancel}
			worker := NewSourceEventWorker(consumer, test.dlq, test.backend)
			waits := 0
			worker.wait = func(context.Context) bool {
				waits++
				if test.name == "dead letter failure" && waits == 1 {
					return true
				}
				cancel()
				return false
			}
			worker.Start(ctx)
			if consumer.commits != 0 {
				t.Fatalf("unexpected commits=%d", consumer.commits)
			}
		})
	}
}

func TestSourceEventWorkerStopsAfterFetchErrorAndRetryWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	consumer := &sourceConsumerStub{fetchErr: errors.New("fetch"), cancel: cancel, cancelOnFetch: 2}
	NewSourceEventWorker(consumer, &sourceDeadLetterStub{}, defaultSourceBackend()).Start(ctx)

	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if waitForSourceEventRetry(cancelled) {
		t.Fatal("cancelled retry wait succeeded")
	}
	started := time.Now()
	if !waitForSourceEventRetry(context.Background()) || time.Since(started) < 1900*time.Millisecond {
		t.Fatal("retry timer did not elapse")
	}
}
