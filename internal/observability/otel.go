package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type otelProvider struct{ tp *sdktrace.TracerProvider }

// newOTel exports spans over OTLP/HTTP. Endpoint, headers and TLS come
// from the standard OTEL_EXPORTER_OTLP_* variables. Export runs in a
// batch goroutine, so a dead collector never blocks requests.
func newOTel(ctx context.Context, s Settings) (Provider, error) {
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", s.ServiceName),
		attribute.String("service.version", s.Version),
		attribute.String("deployment.environment.name", s.Environment),
	))
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	return otelProvider{tp: tp}, nil
}

func (p otelProvider) TracerProvider() trace.TracerProvider { return p.tp }

func (p otelProvider) Shutdown(ctx context.Context) error { return p.tp.Shutdown(ctx) }
