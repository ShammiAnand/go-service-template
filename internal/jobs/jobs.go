// Package jobs runs background work behind a Queue interface. Adapters:
// asynq (Redis, durable, retries) and inline (runs the handler at once in
// the caller; for tests and Redis-free development).
//
// Define a job once with Define, enqueue it with Def.Enqueue, and handle
// it with Handle. Payloads are JSON; trace context travels in headers so
// a job's span joins the request that enqueued it.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/ShammiAnand/go-service-template/internal/platform/safe"
)

// Task is the wire form of a job.
type Task struct {
	Type    string
	Payload []byte
	Headers map[string]string
}

// Queue accepts tasks for later processing.
type Queue interface {
	Enqueue(ctx context.Context, t Task) error
}

// HandlerFunc processes one raw task payload.
type HandlerFunc func(ctx context.Context, payload []byte) error

// permanentError marks a failure that retrying cannot fix.
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// Permanent wraps err so adapters do not retry the task.
func Permanent(err error) error { return permanentError{err} }

// IsPermanent reports whether err was wrapped with Permanent.
func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// Registry maps task types to handlers. Build it once at startup.
type Registry struct {
	handlers map[string]HandlerFunc
	log      *slog.Logger
}

// NewRegistry returns an empty registry.
func NewRegistry(log *slog.Logger) *Registry {
	return &Registry{handlers: map[string]HandlerFunc{}, log: log}
}

// Dispatch runs the handler for t inside a span, with panic recovery. A
// panic becomes an error, so the adapter retries instead of crashing.
func (r *Registry) Dispatch(ctx context.Context, t Task) error {
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(t.Headers))
	ctx, span := otel.Tracer("jobs").Start(ctx, "job "+t.Type,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("job.type", t.Type)))
	defer span.End()

	h, ok := r.handlers[t.Type]
	if !ok {
		err := Permanent(fmt.Errorf("no handler for job type %q", t.Type))
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	err := safe.Call(func() error { return h(ctx, t.Payload) })
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		attrs := []any{"job_type", t.Type, "error", err, "permanent", IsPermanent(err)}
		if pe := (*safe.PanicError)(nil); errors.As(err, &pe) {
			attrs = append(attrs, "stack", string(pe.Stack))
		}
		r.log.ErrorContext(ctx, "job failed", attrs...)
	}
	return err
}

// Def is a typed job definition.
type Def[T any] struct{ Type string }

// Define declares a job type with payload T.
func Define[T any](typ string) Def[T] { return Def[T]{Type: typ} }

// Enqueue serializes p and hands it to q with the current trace context.
func (d Def[T]) Enqueue(ctx context.Context, q Queue, p T) error {
	b, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode %s payload: %w", d.Type, err)
	}
	headers := map[string]string{}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(headers))
	return q.Enqueue(ctx, Task{Type: d.Type, Payload: b, Headers: headers})
}

// Handle registers fn for d. A payload that does not decode is a permanent
// failure.
func Handle[T any](r *Registry, d Def[T], fn func(ctx context.Context, p T) error) {
	if _, dup := r.handlers[d.Type]; dup {
		panic("jobs: duplicate handler for " + d.Type) // startup wiring bug
	}
	r.handlers[d.Type] = func(ctx context.Context, raw []byte) error {
		var p T
		if err := json.Unmarshal(raw, &p); err != nil {
			return Permanent(fmt.Errorf("decode %s payload: %w", d.Type, err))
		}
		return fn(ctx, p)
	}
}

// Inline runs tasks synchronously through a registry.
type Inline struct{ Registry *Registry }

// Enqueue dispatches t at once and returns the handler's error.
func (q Inline) Enqueue(ctx context.Context, t Task) error { return q.Registry.Dispatch(ctx, t) }
