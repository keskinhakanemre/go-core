package logger_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/hekanemre/go-core/logger"
)

func TestNewInstallsGlobal(t *testing.T) {
	l, sync, err := logger.New(logger.Config{Level: "debug", Service: "svc"})
	if err != nil {
		t.Fatal(err)
	}
	defer sync()
	if zap.L() != l {
		t.Fatal("expected logger to be installed globally")
	}
	if !l.Core().Enabled(zap.DebugLevel) {
		t.Fatal("expected debug level")
	}
}

func TestInvalidLevelFallsBackToInfo(t *testing.T) {
	l, sync, err := logger.New(logger.Config{Level: "loud"})
	if err != nil {
		t.Fatal(err)
	}
	defer sync()
	if l.Core().Enabled(zap.DebugLevel) || !l.Core().Enabled(zap.InfoLevel) {
		t.Fatal("expected info level")
	}
}

func TestFromContextAddsTraceAndFields(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	ctx := logger.NewContext(context.Background(), zap.New(core))
	ctx = logger.WithFields(ctx, zap.String("request_id", "r1"))

	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))

	logger.FromContext(ctx).Info("hello")

	entry := logs.All()[0]
	fields := entry.ContextMap()
	if fields["trace_id"] != traceID.String() || fields["span_id"] != spanID.String() || fields["request_id"] != "r1" {
		t.Fatalf("unexpected fields: %v", fields)
	}
}

func TestFromContextFallsBackToGlobal(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	defer zap.ReplaceGlobals(zap.New(core))()
	logger.FromContext(context.Background()).Info("x")
	if logs.Len() != 1 {
		t.Fatal("expected global logger to be used")
	}
}
