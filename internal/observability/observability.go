// Package observability hides the telemetry backend behind Provider.
// Application code uses the OpenTelemetry API (otel.Tracer, span events)
// and never imports an exporter. Swap backends by adding an adapter here.
package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Provider owns telemetry exporters for the life of the process.
type Provider interface {
	TracerProvider() trace.TracerProvider
	// Shutdown flushes pending telemetry. Call it once, last.
	Shutdown(ctx context.Context) error
}

// Settings selects and describes the backend.
type Settings struct {
	Driver      string // "otel" | "noop"
	ServiceName string
	Version     string
	Environment string
}

// New builds the provider for s.Driver and installs it as the global
// tracer provider and W3C propagator.
func New(ctx context.Context, s Settings) (Provider, error) {
	var (
		p   Provider
		err error
	)
	switch s.Driver {
	case "otel":
		p, err = newOTel(ctx, s)
	case "noop":
		p = Noop()
	default:
		return nil, fmt.Errorf("observability: unknown driver %q", s.Driver)
	}
	if err != nil {
		return nil, err
	}
	otel.SetTracerProvider(p.TracerProvider())
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return p, nil
}

// Noop returns a provider that records nothing. Tests use it.
func Noop() Provider { return noopProvider{} }

type noopProvider struct{}

func (noopProvider) TracerProvider() trace.TracerProvider { return noop.NewTracerProvider() }
func (noopProvider) Shutdown(context.Context) error       { return nil }
