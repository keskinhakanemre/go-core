# 02 — Core Modül Yapısı

## Dağıtım stratejisi: iki seçenek

### Seçenek A — Paylaşılan kütüphane modülü (ÖNERİLEN)
Core ayrı bir Go modülü (ayrı git repo) olur, servisler `go get` ile import eder.

- ✅ Bir bug fix / iyileştirme tüm servislere versiyon yükseltmesiyle gider.
- ✅ Servislerde tekrar eden kod olmaz.
- ⚠️ Semver disiplini gerekir (`v0.x` ile başla, breaking change'lerde minor artır; `v1`'den sonra major).

### Seçenek B — Template repo (kopyala-yapıştır)
Core kodu her servise kopyalanır.

- ✅ Her servis bağımsız evrilebilir.
- ❌ Düzeltmeler her servise elle taşınır.

**Önerilen hibrit:** Altyapı paketleri (config, logger, tracing, httpserver...) **Seçenek A** ile kütüphane; servis iskeleti (klasör yapısı, `main.go`, Dockerfile, Makefile) ise `templates/service/` altında **Seçenek B** ile şablon.

## Core modülünün klasör yapısı

```
go-core/
├── go.mod                        # module github.com/hekanemre/go-core
├── README.md
├── Makefile
├── .golangci.yml
├── config/
│   └── config.go                 # Generic Load[T], BaseConfig
├── logger/
│   └── logger.go                 # New(cfg), FromContext(ctx)
├── apperror/
│   └── apperror.go               # Tipli hatalar + HTTP status eşlemesi
├── validation/
│   └── validation.go             # go-playground/validator sarmalayıcı
├── httpserver/
│   ├── server.go                 # New(cfg) *fiber.App, ortak middleware'ler
│   ├── handler.go                # Generic Handle[Req, Res]
│   └── error_handler.go          # apperror → JSON response
├── metrics/
│   └── metrics.go                # Prometheus HTTP middleware + /metrics handler
├── tracing/
│   └── tracing.go                # OTel TracerProvider kurulumu + shutdown
├── httpclient/
│   └── client.go                 # otelhttp + retry + circuit breaker
├── health/
│   └── health.go                 # /livez, /readyz, Checker interface
├── lifecycle/
│   └── lifecycle.go              # Signal yakalama + sıralı kapatma
├── adapters/                     # (opsiyonel) DB bağlantı yardımcıları
│   ├── couchbase/couchbase.go
│   └── postgres/postgres.go
└── templates/
    └── service/                  # Yeni servis iskeleti (bkz. 13)
```

> Kural: Core paketleri **birbirini minimum düzeyde** import etmeli. `httpserver` → `apperror`, `validation`, `metrics` import edebilir; `config` ve `apperror` hiçbir core paketini import etmez.

## go.mod (başlangıç)

```go
module github.com/hekanemre/go-core

go 1.23

require (
	github.com/gofiber/fiber/v2 v2.52.6
	github.com/gofiber/contrib/otelfiber v1.0.10
	github.com/go-playground/validator/v10 v10.22.0
	github.com/hashicorp/go-retryablehttp v0.7.7
	github.com/prometheus/client_golang v1.20.5
	github.com/sony/gobreaker v1.0.0
	github.com/spf13/viper v1.19.0
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.59.0
	go.opentelemetry.io/otel v1.34.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.34.0
	go.opentelemetry.io/otel/sdk v1.34.0
	go.opentelemetry.io/otel/trace v1.34.0
	go.uber.org/zap v1.27.0
)
```

Versiyonlar kaynak projeden alındı. Yeni kurulumda `go get -u ./... && go mod tidy` ile güncelle ve Go'nun güncel stabil sürümünü kullan.

## Yerel geliştirmede core'u servise bağlamak

Core henüz push edilmemişken servisin `go.mod`'una:

```go
require github.com/hekanemre/go-core v0.0.0
replace github.com/hekanemre/go-core => ../go-core
```

Ya da birden fazla modül üzerinde aynı anda çalışırken `go work`:

```bash
go work init ./go-core ./product-service
```

Yayınlamak için: `git tag v0.1.0 && git push --tags`. Private repo ise:

```bash
go env -w GOPRIVATE=github.com/hekanemre/*
```

## Kalite araçları (core ve servislerde ortak)

`Makefile`:

```makefile
.PHONY: tidy lint test build run
tidy:  ; go mod tidy
lint:  ; golangci-lint run ./...
test:  ; go test -race -cover ./...
build: ; CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/app ./cmd/api
run:   ; go run ./cmd/api
```

`.golangci.yml` (minimum):

```yaml
linters:
  enable: [errcheck, govet, staticcheck, ineffassign, unused, gosec, revive, bodyclose, contextcheck, errorlint]
```
