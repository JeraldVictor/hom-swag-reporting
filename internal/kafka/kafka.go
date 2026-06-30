package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type Consumer struct {
	Reader  *kafka.Reader
	Topic   string
	GroupID string
}

func NewConsumer(brokers []string, topic, groupID string) *Consumer {
	return &Consumer{
		Topic:   topic,
		GroupID: groupID,
		Reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers:  brokers,
			Topic:    topic,
			GroupID:  groupID,
			MinBytes: 10e3, // 10KB
			MaxBytes: 10e6, // 10MB
		}),
	}
}

func (c *Consumer) ReadMessage(ctx context.Context) (kafka.Message, error) {
	return c.Reader.ReadMessage(ctx)
}

func (c *Consumer) StartMessageSpan(ctx context.Context, msg kafka.Message) (context.Context, trace.Span) {
	parentCtx := otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(headersToMap(msg.Headers)))
	spanCtx, span := otel.Tracer("homswag-reporting-kafka").Start(
		parentCtx,
		c.Topic+" receive",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			messagingAttributes(c.Topic, "receive")...,
		),
		trace.WithAttributes(
			attribute.String("messaging.kafka.consumer.group", c.GroupID),
			attribute.Int("messaging.kafka.destination.partition", msg.Partition),
			attribute.Int64("messaging.kafka.message.offset", msg.Offset),
			attribute.String("messaging.kafka.message.key", string(msg.Key)),
		),
	)
	return spanCtx, span
}

func (c *Consumer) Close() error {
	return c.Reader.Close()
}

type Producer struct {
	Writer *kafka.Writer
	Topic  string
}

func NewProducer(brokers []string, topic string) *Producer {
	return &Producer{
		Topic: topic,
		Writer: &kafka.Writer{
			Addr:     kafka.TCP(brokers...),
			Topic:    topic,
			Balancer: &kafka.LeastBytes{},
		},
	}
}

func (p *Producer) SendMessage(ctx context.Context, key string, value interface{}) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	ctx, span := otel.Tracer("homswag-reporting-kafka").Start(
		ctx,
		p.Topic+" publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			messagingAttributes(p.Topic, "publish")...,
		),
		trace.WithAttributes(attribute.String("messaging.kafka.message.key", key)),
	)
	defer span.End()

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)

	if err := p.Writer.WriteMessages(ctx, kafka.Message{
		Key:     []byte(key),
		Value:   payload,
		Headers: mapToHeaders(carrier),
	}); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	return nil
}

func (p *Producer) Close() error {
	return p.Writer.Close()
}

func ParseBrokers(brokers string) []string {
	return strings.Split(brokers, ",")
}

func messagingAttributes(topic string, operation string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("messaging.system", "kafka"),
		attribute.String("messaging.operation", operation),
		attribute.String("messaging.destination.name", topic),
		attribute.String("messaging.destination_kind", "topic"),
		attribute.String("messaging.kafka.destination.name", topic),
	}
}

func headersToMap(headers []kafka.Header) map[string]string {
	carrier := make(map[string]string, len(headers))
	for _, header := range headers {
		carrier[header.Key] = string(header.Value)
	}
	return carrier
}

func mapToHeaders(carrier propagation.MapCarrier) []kafka.Header {
	headers := make([]kafka.Header, 0, len(carrier))
	for key, value := range carrier {
		headers = append(headers, kafka.Header{Key: key, Value: []byte(value)})
	}
	return headers
}
