// Package logger builds a zap logger and provides trace aware, context scoped
// logging.
//
// Inside a request always log through FromContext(ctx): the returned logger
// carries trace_id/span_id (so logs can be correlated with traces) and any
// fields attached with WithFields (e.g. request_id).
package logger

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Config struct {
	Level   string // debug | info | warn | error (default info)
	Format  string // json | console (default json)
	Service string
	Version string
	Env     string
}

// New builds a logger and installs it as the global logger (zap.L()).
// The returned function flushes buffers and restores the previous global
// logger; call it on shutdown.
func New(cfg Config) (*zap.Logger, func(), error) {
	level := zap.NewAtomicLevel()
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil || cfg.Level == "" {
		level.SetLevel(zap.InfoLevel)
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "timestamp"
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encCfg.EncodeDuration = zapcore.MillisDurationEncoder

	encoding := "json"
	if cfg.Format == "console" {
		encoding = "console"
		encCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	fields := map[string]any{"pid": os.Getpid()}
	for k, v := range map[string]string{"service": cfg.Service, "version": cfg.Version, "env": cfg.Env} {
		if v != "" {
			fields[k] = v
		}
	}

	zcfg := zap.Config{
		Level:            level,
		Encoding:         encoding,
		EncoderConfig:    encCfg,
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
		InitialFields:    fields,
	}

	l, err := zcfg.Build(zap.AddStacktrace(zap.ErrorLevel))
	if err != nil {
		return nil, nil, err
	}
	undo := zap.ReplaceGlobals(l)
	return l, func() { _ = l.Sync(); undo() }, nil
}

type ctxKey struct{}

// NewContext returns a context carrying l. FromContext will return it
// (enriched with trace fields) instead of the global logger.
func NewContext(ctx context.Context, l *zap.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// WithFields returns a context whose logger has the given fields attached.
func WithFields(ctx context.Context, fields ...zap.Field) context.Context {
	return NewContext(ctx, loggerFrom(ctx).With(fields...))
}

// FromContext returns the context logger (or the global logger) with
// trace_id/span_id fields when ctx carries a valid span.
func FromContext(ctx context.Context) *zap.Logger {
	l := loggerFrom(ctx)
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return l
	}
	return l.With(
		zap.String("trace_id", sc.TraceID().String()),
		zap.String("span_id", sc.SpanID().String()),
	)
}

func loggerFrom(ctx context.Context) *zap.Logger {
	if ctx != nil {
		if l, ok := ctx.Value(ctxKey{}).(*zap.Logger); ok && l != nil {
			return l
		}
	}
	return zap.L()
}
