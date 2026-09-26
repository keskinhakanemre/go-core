// Package tracing configures the global OpenTelemetry TracerProvider with an
// OTLP/HTTP exporter (Jaeger, Tempo, OTel Collector, ...).
package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

type Config struct {
	Enabled     bool
	Endpoint    string // host:port, e.g. "jaeger:4318"; empty uses OTEL_EXPORTER_OTLP_* env or localhost:4318
	Insecure    bool
	SampleRatio float64 // 0..1, applied to root spans; child spans follow their parent
	ServiceName string
	Version     string
	Env         string
}

// ShutdownFunc flushes pending spans and stops the exporter.
type ShutdownFunc func(context.Context) error

// Init installs the W3C TraceContext + Baggage propagator and, when enabled, a
// batching TracerProvider as the global provider. The returned ShutdownFunc
// must be called on shutdown, otherwise buffered spans are lost.
//
// When tracing is disabled the propagator is still installed so that incoming
// trace headers are forwarded to downstream services.
func Init(ctx context.Context, cfg Config) (ShutdownFunc, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	var opts []otlptracehttp.Option
	if cfg.Endpoint != "" {
		opts = append(opts, otlptracehttp.WithEndpoint(cfg.Endpoint))
	}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("tracing: exporter: %w", err)
	}

	res, err := Resource(ctx, cfg)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// Resource describes this service instance. OTEL_RESOURCE_ATTRIBUTES and
// OTEL_SERVICE_NAME are honoured and take precedence.
func Resource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	res, err := resource.New(ctx,
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.Version),
			semconv.DeploymentEnvironmentNameKey.String(cfg.Env),
		),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithFromEnv(),
	)
	if err != nil {
		return nil, fmt.Errorf("tracing: resource: %w", err)
	}
	return res, nil
}
