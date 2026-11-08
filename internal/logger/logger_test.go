package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestTraceIDsAreInjected(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithWriter(&buf, "debug", "json")
	tp := sdktrace.NewTracerProvider()
	ctx, span := tp.Tracer("t").Start(context.Background(), "op")
	l.InfoContext(ctx, "hello")
	span.End()

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	require.Equal(t, span.SpanContext().TraceID().String(), rec["trace_id"])
	require.Equal(t, span.SpanContext().SpanID().String(), rec["span_id"])
}

func TestNoTraceWithoutSpan(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithWriter(&buf, "info", "json")
	l.Info("plain")
	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	require.NotContains(t, rec, "trace_id")
}
