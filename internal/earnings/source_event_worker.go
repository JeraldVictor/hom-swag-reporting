package earnings

import (
	"context"
	"log"
	"time"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel/trace"
)

type sourceEventConsumer interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
	StartMessageSpan(context.Context, kafka.Message) (context.Context, trace.Span)
}

type sourceEventDeadLetter interface {
	SendMessage(context.Context, string, interface{}) error
}

type SourceEventWorker struct {
	consumer   sourceEventConsumer
	deadLetter sourceEventDeadLetter
	processor  *SourceEventProcessor
	wait       func(context.Context) bool
}

func NewSourceEventWorker(consumer sourceEventConsumer, deadLetter sourceEventDeadLetter, backend SourceEventBackend) *SourceEventWorker {
	return &SourceEventWorker{consumer: consumer, deadLetter: deadLetter, processor: NewSourceEventProcessor(backend), wait: waitForSourceEventRetry}
}

func (w *SourceEventWorker) Start(ctx context.Context) {
	log.Printf("earnings source-event worker started")
	for ctx.Err() == nil {
		message, err := w.consumer.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("earnings source-event fetch failed: %v", err)
			}
			continue
		}
		messageCtx, span := w.consumer.StartMessageSpan(ctx, message)
		for messageCtx.Err() == nil {
			result, created, processErr := w.processor.ProcessJSON(messageCtx, message.Value)
			if processErr == nil {
				if err := w.consumer.CommitMessages(messageCtx, message); err != nil {
					log.Printf("earnings source-event commit failed: %v", err)
					continue
				}
				log.Printf("earnings source event accepted: source=%s/%s created=%t ignored=%t", result.SourceType, result.SourceID.Hex(), created, result.Ignored)
				break
			}
			span.RecordError(processErr)
			if IsPermanentSourceEventError(processErr) {
				if err := w.deadLetter.SendMessage(messageCtx, string(message.Key), message.Value); err != nil {
					log.Printf("earnings source-event dead-letter failed: %v", err)
					if !w.wait(messageCtx) {
						break
					}
					continue
				}
				if err := w.consumer.CommitMessages(messageCtx, message); err != nil {
					log.Printf("earnings source-event dead-letter commit failed: %v", err)
					continue
				}
				log.Printf("earnings source event rejected permanently: %v", processErr)
				break
			}
			log.Printf("earnings source-event processing failed; retrying: %v", processErr)
			if !w.wait(messageCtx) {
				break
			}
		}
		span.End()
	}
}

func waitForSourceEventRetry(ctx context.Context) bool {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
