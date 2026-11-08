package queue_test

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	"github.com/xonoko/infra-monitor/internal/db/ent"
)

type entTx = ent.Tx

func traceID(ctx context.Context) string { return trace.SpanContextFromContext(ctx).TraceID().String() }
