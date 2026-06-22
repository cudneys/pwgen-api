// Package telemetry wires up OpenTelemetry tracing (exported to an OTLP agent)
// and metrics (exposed via a Prometheus exporter).
package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Providers holds the configured telemetry providers and a shutdown hook.
type Providers struct {
	shutdown func(context.Context) error
}

// Shutdown flushes and stops the telemetry pipelines.
func (p *Providers) Shutdown(ctx context.Context) error {
	if p == nil || p.shutdown == nil {
		return nil
	}
	return p.shutdown(ctx)
}

// Setup initialises tracing and metrics.
//
//   - Traces are exported over OTLP/gRPC to the agent configured via the standard
//     OTEL_EXPORTER_OTLP_ENDPOINT / OTEL_EXPORTER_OTLP_TRACES_ENDPOINT env vars.
//   - Metrics are registered with the Prometheus exporter; scrape them from the
//     /metrics endpoint that main.go mounts.
//   - The global propagator is set to W3C trace-context + baggage so incoming
//     trace headers are honoured and outbound requests carry them forward.
func Setup(ctx context.Context, serviceName string) (*Providers, error) {
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("build resource: %w", err)
	}

	// Tracing: OTLP/gRPC exporter to the agent.
	traceExp, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create otlp trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	// Metrics: Prometheus exporter (pull based).
	promExp, err := promexporter.New()
	if err != nil {
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}
	mp := metric.NewMeterProvider(
		metric.WithReader(promExp),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	// Honour and forward W3C trace headers.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	shutdown := func(ctx context.Context) error {
		var firstErr error
		for _, fn := range []func(context.Context) error{tp.Shutdown, mp.Shutdown} {
			if err := fn(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}

	return &Providers{shutdown: shutdown}, nil
}
