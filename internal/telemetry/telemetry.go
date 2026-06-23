// Package telemetry wires up OpenTelemetry tracing (exported to an OTLP agent)
// and metrics (exposed via a Prometheus exporter).
package telemetry

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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

// Setup initialises tracing and metrics. serviceVersion is reported as the
// service.version resource attribute on all emitted telemetry.
//
//   - Traces are exported over OTLP/gRPC to the agent configured via the standard
//     OTEL_EXPORTER_OTLP_ENDPOINT / OTEL_EXPORTER_OTLP_TRACES_ENDPOINT env vars.
//   - Metrics are registered with the Prometheus exporter; scrape them from the
//     /metrics endpoint that main.go mounts.
//   - The global propagator is set to W3C trace-context + baggage so incoming
//     trace headers are honoured and outbound requests carry them forward.
func Setup(ctx context.Context, serviceName, serviceVersion string) (*Providers, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(serviceVersion),
	}
	// Tag spans with the originating pod when running in Kubernetes. The pod
	// name/namespace are injected via the downward API (see k8s/deployment.yaml).
	if podName := os.Getenv("POD_NAME"); podName != "" {
		attrs = append(attrs, semconv.K8SPodName(podName))
	}
	if podNamespace := os.Getenv("POD_NAMESPACE"); podNamespace != "" {
		attrs = append(attrs, semconv.K8SNamespaceName(podNamespace))
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(attrs...),
	)
	if err != nil {
		return nil, fmt.Errorf("build resource: %w", err)
	}

	// Tracing: OTLP/gRPC exporter to the agent. Authenticate with a bearer token
	// when one is provided (sourced from the otel-bearer-token secret in k8s).
	var traceOpts []otlptracegrpc.Option
	if token := os.Getenv("OTEL_EXPORTER_OTLP_BEARER_TOKEN"); token != "" {
		traceOpts = append(traceOpts, otlptracegrpc.WithHeaders(map[string]string{
			"Authorization": "Bearer " + token,
		}))
	}
	traceExp, err := otlptracegrpc.New(ctx, traceOpts...)
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
