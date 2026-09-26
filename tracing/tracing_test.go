package tracing_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/hekanemre/go-core/tracing"
)

func TestInitDisabledInstallsPropagator(t *testing.T) {
	shutdown, err := tracing.Init(context.Background(), tracing.Config{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	fields := otel.GetTextMapPropagator().Fields()
	if len(fields) < 2 {
		t.Fatalf("expected traceparent and baggage propagators, got %v", fields)
	}
}

func TestInitEnabled(t *testing.T) {
	// The exporter connects lazily, so no collector is needed here.
	shutdown, err := tracing.Init(context.Background(), tracing.Config{
		Enabled: true, Endpoint: "127.0.0.1:1", Insecure: true, SampleRatio: 1, ServiceName: "svc",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("test").Start(context.Background(), "op")
	if !span.SpanContext().IsSampled() {
		t.Fatal("expected span to be sampled")
	}
	span.End()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // do not wait for the unreachable collector
	_ = shutdown(ctx)
}

func TestResource(t *testing.T) {
	res, err := tracing.Resource(context.Background(), tracing.Config{ServiceName: "svc", Version: "1.2.3", Env: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	attrs := res.Set()
	if v, ok := attrs.Value(semconv.ServiceNameKey); !ok || v.AsString() != "svc" {
		t.Fatalf("service.name = %v", v)
	}
}
