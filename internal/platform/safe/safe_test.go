package safe

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCall(t *testing.T) {
	require.NoError(t, Call(func() error { return nil }))

	want := errors.New("x")
	require.ErrorIs(t, Call(func() error { return want }), want)

	err := Call(func() error { panic("boom") })
	var pe *PanicError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, "boom", pe.Value)
	assert.NotEmpty(t, pe.Stack)
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }

func (s *syncBuf) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestGoLogsPanic(t *testing.T) {
	var buf syncBuf
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	Go(context.Background(), log, "worker-x", func(context.Context) error { panic("kaboom") })
	require.Eventually(t, func() bool { return bytes.Contains([]byte(buf.String()), []byte("kaboom")) }, time.Second, 5*time.Millisecond)
	assert.Contains(t, buf.String(), `"goroutine":"worker-x"`)
}
