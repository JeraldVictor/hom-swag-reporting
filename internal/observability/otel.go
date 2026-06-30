package observability

import (
	"context"
	"log"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
)

type Config struct {
	Enabled     bool
	TracesURL   string
	ServiceName string
	Environment string
}

func Init(ctx context.Context, cfg Config) func(context.Context) error {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }
	}

	endpoint, path, insecure := parseOTLPEndpoint(cfg.TracesURL)
	exporter, err := otlptracehttp.New(
		ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithURLPath(path),
		otlptracehttp.WithCompression(otlptracehttp.GzipCompression),
		withInsecure(insecure),
	)
	if err != nil {
		log.Printf("Failed to initialize OTEL trace exporter: %v", err)
		return func(context.Context) error { return nil }
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceNamespace("homswag"),
		semconv.DeploymentEnvironmentName(cfg.Environment),
	)
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return provider.Shutdown
}

func parseOTLPEndpoint(raw string) (string, string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return strings.TrimPrefix(strings.TrimPrefix(raw, "http://"), "https://"), "/v1/traces", true
	}

	path := parsed.Path
	if path == "" {
		path = "/v1/traces"
	}

	return parsed.Host, path, parsed.Scheme != "https"
}

func withInsecure(enabled bool) otlptracehttp.Option {
	if enabled {
		return otlptracehttp.WithInsecure()
	}
	return otlptracehttp.WithHeaders(map[string]string{})
}
