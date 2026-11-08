// Package telemetry sets up OpenTelemetry tracing and metrics.
// Metrics are exposed in Prometheus format on /metrics (and optionally pushed over OTLP).
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/xonoko/infra-monitor/internal/buildinfo"
	"github.com/xonoko/infra-monitor/internal/config"
)

// ScopeName is the instrumentation scope used by infra-monitor packages.
const ScopeName = "github.com/xonoko/infra-monitor"

type Telemetry struct {
	Registry *prometheus.Registry
	shutdown []func(context.Context) error
}

// Setup installs global tracer/meter providers and the W3C propagator.
func Setup(ctx context.Context, cfg config.TelemetryConfig, log *slog.Logger) (*Telemetry, error) {
	t := &Telemetry{Registry: prometheus.NewRegistry()}
	t.Registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	host, _ := os.Hostname()
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(buildinfo.Version),
		semconv.ServiceInstanceID(host),
	))
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	if cfg.Tracing.Enabled {
		exp, err := traceExporter(ctx, cfg.Tracing)
		if err != nil {
			return nil, err
		}
		ratio := cfg.Tracing.SampleRatio
		if ratio <= 0 || ratio > 1 {
			ratio = 1
		}
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(exp),
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
		)
		otel.SetTracerProvider(tp)
		t.shutdown = append(t.shutdown, tp.Shutdown)
		log.Info("tracing enabled", "exporter", cfg.Tracing.Exporter, "endpoint", cfg.Tracing.Endpoint)
	} else {
		// A real SDK provider without exporter still creates valid span contexts,
		// so trace ids keep correlating logs and timeline events.
		tp := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithSampler(sdktrace.NeverSample()))
		otel.SetTracerProvider(tp)
		t.shutdown = append(t.shutdown, tp.Shutdown)
	}

	var readers []sdkmetric.Option
	if cfg.Metrics.Enabled {
		pe, err := promexporter.New(promexporter.WithRegisterer(t.Registry))
		if err != nil {
			return nil, fmt.Errorf("prometheus exporter: %w", err)
		}
		readers = append(readers, sdkmetric.WithReader(pe))
	}
	if cfg.Metrics.OTLPEnabled {
		opts := []otlpmetricgrpc.Option{}
		if cfg.Metrics.OTLPEndpoint != "" {
			opts = append(opts, otlpmetricgrpc.WithEndpoint(cfg.Metrics.OTLPEndpoint), otlpmetricgrpc.WithInsecure())
		}
		me, err := otlpmetricgrpc.New(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("otlp metric exporter: %w", err)
		}
		interval := cfg.Metrics.OTLPInterval
		if interval <= 0 {
			interval = 30 * time.Second
		}
		readers = append(readers, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(me, sdkmetric.WithInterval(interval))))
	}
	mp := sdkmetric.NewMeterProvider(append(readers, sdkmetric.WithResource(res))...)
	otel.SetMeterProvider(mp)
	t.shutdown = append(t.shutdown, mp.Shutdown)
	return t, nil
}

func traceExporter(ctx context.Context, cfg config.TracingConfig) (sdktrace.SpanExporter, error) {
	switch cfg.Exporter {
	case "", "otlp-grpc", "otlp":
		opts := []otlptracegrpc.Option{}
		if cfg.Endpoint != "" {
			opts = append(opts, otlptracegrpc.WithEndpoint(cfg.Endpoint))
		}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracegrpc.WithHeaders(cfg.Headers))
		}
		return otlptracegrpc.New(ctx, opts...)
	case "otlp-http":
		opts := []otlptracehttp.Option{}
		if cfg.Endpoint != "" {
			opts = append(opts, otlptracehttp.WithEndpoint(cfg.Endpoint))
		}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
		}
		return otlptracehttp.New(ctx, opts...)
	case "stdout":
		return stdouttrace.New(stdouttrace.WithPrettyPrint())
	default:
		return nil, fmt.Errorf("unknown trace exporter %q", cfg.Exporter)
	}
}

// Shutdown flushes exporters.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	for i := len(t.shutdown) - 1; i >= 0; i-- {
		errs = append(errs, t.shutdown[i](ctx))
	}
	return errors.Join(errs...)
}

// MetricsHandler serves the Prometheus registry.
func (t *Telemetry) MetricsHandler() http.Handler {
	return promhttp.HandlerFor(t.Registry, promhttp.HandlerOpts{Registry: t.Registry})
}

// Tracer returns the infra-monitor tracer.
func Tracer() trace.Tracer { return otel.Tracer(ScopeName) }

// HTTPClient returns an http.Client whose requests create client spans and propagate context.
func HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: otelhttp.NewTransport(http.DefaultTransport)}
}

// TraceID returns the current trace id or "".
func TraceID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}

// Traceparent serializes the current span context (W3C).
func Traceparent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier["traceparent"]
}

// ContextWithTraceparent returns ctx carrying the remote span context from a traceparent string.
func ContextWithTraceparent(ctx context.Context, traceparent string) context.Context {
	if traceparent == "" {
		return ctx
	}
	return propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": traceparent})
}

// RecordError marks the span as failed.
func RecordError(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codesError, err.Error())
}
