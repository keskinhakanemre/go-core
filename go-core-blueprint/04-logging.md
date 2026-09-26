# 04 — Logging

## Kaynak projede

`pkg/log/log.go`: `init()` içinde production-benzeri bir Zap logger kuruluyor (JSON, ISO8601 `timestamp`, `pid` alanı, stderr) ve `zap.ReplaceGlobals` ile global yapılıyor. `main.go` bunu `_ "microservicetest/pkg/log"` blank import ile tetikliyor; tüm kod `zap.L()` kullanıyor.

**İyi yanları:** Structured JSON log, tutarlı zaman formatı, tek noktadan konfigürasyon.

**Eksikler:**
- Level sabit (`Info`), config'ten ayarlanamıyor.
- `init()` side-effect'i → import sırasına bağımlı, test edilemez.
- Loglarda `trace_id` yok → Jaeger'daki trace ile log eşleştirilemiyor.
- Servis adı / versiyon / ortam alanları yok.

## `logger/logger.go`

```go
package logger

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Config struct {
	Level   string // debug | info | warn | error
	Format  string // json | console
	Service string
	Version string
	Env     string
}

// New logger oluşturur ve global logger olarak ayarlar (zap.L()).
// Dönen fonksiyon uygulama kapanırken çağrılmalıdır (buffer flush).
func New(cfg Config) (*zap.Logger, func(), error) {
	level := zap.NewAtomicLevel()
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		level.SetLevel(zap.InfoLevel)
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "timestamp"
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	encoding := "json"
	if cfg.Format == "console" {
		encoding = "console"
		encCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	zcfg := zap.Config{
		Level:            level,
		Encoding:         encoding,
		EncoderConfig:    encCfg,
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
		InitialFields: map[string]any{
			"pid":     os.Getpid(),
			"service": cfg.Service,
			"version": cfg.Version,
			"env":     cfg.Env,
		},
	}

	l, err := zcfg.Build(zap.AddStacktrace(zap.ErrorLevel))
	if err != nil {
		return nil, nil, err
	}
	undo := zap.ReplaceGlobals(l)
	return l, func() { _ = l.Sync(); undo() }, nil
}

// FromContext aktif span varsa trace_id/span_id alanlarını ekleyerek logger döner.
func FromContext(ctx context.Context) *zap.Logger {
	l := zap.L()
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return l
	}
	return l.With(
		zap.String("trace_id", sc.TraceID().String()),
		zap.String("span_id", sc.SpanID().String()),
	)
}
```

## Kullanım kuralları

- Request akışı içindeki her log `logger.FromContext(ctx)` ile atılır → log ↔ trace eşleşir.
- Uygulama başlangıcı/kapanışı gibi context'siz yerlerde `zap.L()` kullanılabilir.
- **Bir hatayı ya logla ya döndür, ikisini birden yapma.** Kaynak projede repository hem logluyor hem dönüyor, sonra handler wrapper tekrar logluyor → aynı hata 2 kez yazılıyor. Core'da hatalar tek noktada (HTTP error handler) loglanır.
- `zap.Any` ile config/secret içeren struct loglama.
- Log alanları snake_case: `user_id`, `order_id`, `duration_ms`.
- Kubernetes'te stdout JSON → Loki / ELK / Cloud Logging tarafından toplanır.
