package logging

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestJSONWithCorrelationIDs(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "info", "svc", "v1")

	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	ctx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid}))
	ctx = WithRequestID(ctx, "req-1")

	log.InfoContext(ctx, "hello", "k", "v")
	log.DebugContext(ctx, "dropped below level")

	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line), "exactly one JSON line: %s", buf.String())
	assert.Equal(t, "hello", line["msg"])
	assert.Equal(t, "svc", line["service"])
	assert.Equal(t, "v1", line["version"])
	assert.Equal(t, "req-1", line["request_id"])
	assert.Equal(t, tid.String(), line["trace_id"])
	assert.Equal(t, sid.String(), line["span_id"])
}
