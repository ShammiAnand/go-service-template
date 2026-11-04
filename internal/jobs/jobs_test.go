package jobs_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ShammiAnand/go-service-template/internal/jobs"
	"github.com/ShammiAnand/go-service-template/internal/platform/safe"
)

type payload struct {
	N int `json:"n"`
}

var job = jobs.Define[payload]("test:job")

func TestInlineRoundTrip(t *testing.T) {
	reg := jobs.NewRegistry(slog.New(slog.DiscardHandler))
	var got payload
	jobs.Handle(reg, job, func(_ context.Context, p payload) error { got = p; return nil })

	require.NoError(t, job.Enqueue(t.Context(), jobs.Inline{Registry: reg}, payload{N: 7}))
	assert.Equal(t, 7, got.N)
}

func TestDispatchRecoversPanic(t *testing.T) {
	reg := jobs.NewRegistry(slog.New(slog.DiscardHandler))
	jobs.Handle(reg, job, func(context.Context, payload) error { panic("boom") })

	err := job.Enqueue(t.Context(), jobs.Inline{Registry: reg}, payload{})
	var pe *safe.PanicError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, "boom", pe.Value)
	assert.False(t, jobs.IsPermanent(err), "panics are retried")
}

func TestPermanentFailures(t *testing.T) {
	reg := jobs.NewRegistry(slog.New(slog.DiscardHandler))
	jobs.Handle(reg, job, func(context.Context, payload) error { return nil })

	err := reg.Dispatch(t.Context(), jobs.Task{Type: "unknown"})
	assert.True(t, jobs.IsPermanent(err), "unknown type")

	err = reg.Dispatch(t.Context(), jobs.Task{Type: job.Type, Payload: []byte("{bad")})
	assert.True(t, jobs.IsPermanent(err), "bad payload")

	assert.False(t, jobs.IsPermanent(errors.New("transient")))
}

func TestDuplicateHandlerPanics(t *testing.T) {
	reg := jobs.NewRegistry(slog.New(slog.DiscardHandler))
	jobs.Handle(reg, job, func(context.Context, payload) error { return nil })
	assert.Panics(t, func() { jobs.Handle(reg, job, func(context.Context, payload) error { return nil }) })
}
