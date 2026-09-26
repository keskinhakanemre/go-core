# go-core

[![ci](https://github.com/hekanemre/go-core/actions/workflows/ci.yml/badge.svg)](https://github.com/hekanemre/go-core/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/hekanemre/go-core.svg)](https://pkg.go.dev/github.com/hekanemre/go-core)

Go backend servisleri için tekrar kullanılabilir, production-ready core framework.
Fiber tabanlı HTTP server, tipli hata yönetimi, validation, OpenTelemetry tracing, Prometheus metrikleri,
dayanıklı (retry + circuit breaker) HTTP client, health check'ler ve graceful shutdown tek bir modülde.

Tasarım gerekçeleri ve kaynak projenin analizi: [`go-core-blueprint/`](go-core-blueprint/README.md).

## İçindekiler

- [Paketler](#paketler)
- [Hızlı başlangıç](#hızlı-başlangıç)
- [Kullanım](#kullanım)
- [Konfigürasyon referansı](#konfigürasyon-referansı)
- [HTTP davranışı](#http-davranışı)
- [Versiyonlama ve yayınlama](#versiyonlama-ve-yayınlama)
- [Geliştirme](#geliştirme)
- [Blueprint'e göre iyileştirmeler](#blueprinte-göre-iyileştirmeler)

## Paketler

| Paket | Görev |
|---|---|
| [`service`](service) | Hepsini bağlayan bootstrap: logger + tracing + HTTP server + health + graceful shutdown |
| [`config`](config) | Generic `Load[T]`: YAML + ortam overlay'i + env override + validation, `Secret` tipi |
| [`httpserver`](httpserver) | Fiber app, middleware zinciri, generic `Handle[Req, Res]`, merkezi hata render'ı |
| [`apperror`](apperror) | Transport'tan bağımsız tipli hatalar (`NotFound`, `Conflict`, ...) → HTTP status |
| [`validation`](validation) | `go-playground/validator` sarmalayıcı, client dostu alan isimleri |
| [`logger`](logger) | Zap logger, `FromContext(ctx)` ile trace_id / request_id'li log |
| [`tracing`](tracing) | OpenTelemetry TracerProvider (OTLP/HTTP), W3C propagation, sampling |
| [`metrics`](metrics) | Prometheus HTTP RED metrikleri + `/metrics` |
| [`httpclient`](httpclient) | Downstream client: circuit breaker + retry + timeout + tracing + metrik |
| [`health`](health) | `/livez` ve `/readyz`, paralel ve timeout'lu `Checker`'lar |
| [`lifecycle`](lifecycle) | Sinyal yakalama + ters sırada, tek zaman bütçeli kapatma |
| [`adapters/postgres`](adapters/postgres) | *Ayrı modül.* pgx pool + otelpgx + `WithTx` + hata yardımcıları |
| [`adapters/couchbase`](adapters/couchbase) | *Ayrı modül.* gocb + OpenTelemetry + `Ping` |
| [`cmd/gocore`](cmd/gocore) | Yeni servis iskeleti üreten CLI |

Adapter'lar ayrı Go modülleridir: Postgres kullanmayan bir servis pgx'i, Couchbase kullanmayan bir servis gocb'yi indirmez.

## Hızlı başlangıç

Gereksinim: **Go 1.26+**

### A) Yeni servis üret (önerilen)

```bash
go run github.com/hekanemre/go-core/cmd/gocore@latest new github.com/<kullanici>/order-service
cd order-service
make run
```

Üretilen iskelet: katmanlı mimari (`cmd/api`, `internal/{config,domain,app,infra,transport}`), örnek `product`
feature'ı (in-memory repo ile, çalışır halde), unit + route testleri, `config/*.yaml`, distroless Dockerfile,
docker-compose (Jaeger + Prometheus + Grafana + hazır dashboard), Kubernetes manifestleri (probe, limit, HPA, PDB),
GitHub Actions CI, golangci-lint config'i ve Claude Code için `CLAUDE.md`.

```bash
curl -X POST localhost:8080/api/v1/products -H 'Content-Type: application/json' -d '{"name":"kalem","price":1500}'
curl localhost:8080/api/v1/products
curl localhost:8080/readyz
```

CLI bayrakları: `-dir`, `-core-version v0.1.0`, `-core-replace ../go-core` (lokal core checkout'u ile), `-no-tidy`, `-force`.

### B) Mevcut bir projeye ekle

```bash
go get github.com/hekanemre/go-core@latest
```

```go
package main

import (
	"context"
	"os"

	"github.com/hekanemre/go-core/config"
	"github.com/hekanemre/go-core/httpserver"
	"github.com/hekanemre/go-core/service"
)

type Config struct {
	config.BaseConfig `mapstructure:",squash"`
}

type HelloRequest struct {
	Name string `query:"name" validate:"required,min=2"`
}

type HelloResponse struct {
	Message string `json:"message"`
}

func main() {
	ctx := context.Background()
	cfg, err := config.Load[Config](config.Options{EnvPrefix: "APP"})
	if err != nil {
		panic(err)
	}
	svc, err := service.New(ctx, cfg.BaseConfig)
	if err != nil {
		panic(err)
	}
	svc.HTTP.Get("/hello", httpserver.HandleFunc(func(ctx context.Context, req *HelloRequest) (*HelloResponse, error) {
		return &HelloResponse{Message: "merhaba " + req.Name}, nil
	}))
	if err := svc.Run(ctx); err != nil {
		os.Exit(1)
	}
}
```

`APP_APP_NAME=hello go run .` (ya da `config/config.yaml` içinde `app.name`) → `GET /hello?name=dunya`,
`/livez`, `/readyz`, `/metrics` hazır.

## Kullanım

### Handler'lar: HTTP'den bağımsız use case'ler

```go
type GetProductRequest struct {
	ID     string `params:"id"        validate:"required,uuid4"`
	Expand bool   `query:"expand"`
	Tenant string `reqHeader:"X-Tenant"`
}

type GetProductHandler struct{ repo Repository }

func (h *GetProductHandler) Handle(ctx context.Context, req *GetProductRequest) (*ProductResponse, error) {
	p, err := h.repo.Get(ctx, req.ID)
	if err != nil {
		return nil, err // domain.ErrProductNotFound → 404
	}
	return toResponse(p), nil
}
```

```go
products := app.Group("/api/v1/products")
products.Get("/:id", httpserver.Handle(getHandler))
products.Post("", httpserver.Handle(createHandler, httpserver.WithStatus(fiber.StatusCreated)))
products.Delete("/:id", httpserver.Handle(deleteHandler, httpserver.WithStatus(fiber.StatusNoContent)))
products.Post("/import", httpserver.Handle(importHandler, httpserver.WithTimeout(30*time.Second)))
```

`Handle`: body (`json`) + path (`params`) + query (`query`) + header (`reqHeader`) → tek struct'a bind eder,
`validate` tag'lerini çalıştırır, request timeout'lu context ile `Handle`'ı çağırır, sonucu JSON döner.
Handler'lar Fiber import etmez, düz Go fonksiyonu gibi test edilir.

### Hatalar

```go
// internal/domain/errors.go
var ErrProductNotFound = apperror.NotFound("product_not_found", "product not found")

// repository
if errors.Is(err, pgx.ErrNoRows) {
	return nil, domain.ErrProductNotFound
}
return nil, fmt.Errorf("get product %s: %w", id, err) // → 500, detay sadece logda
```

Kind → status: `BadRequest` 400 · `Unauthorized` 401 · `Forbidden` 403 · `NotFound` 404 · `Conflict` 409 ·
`Validation` 422 · `TooManyRequests` 429 · `Internal` 500 · `Unavailable` 503 · `Timeout` 504.

### Konfigürasyon

```go
type Config struct {
	config.BaseConfig `mapstructure:",squash"`
	Postgres struct {
		DSN config.Secret `mapstructure:"dsn" validate:"required"`
	} `mapstructure:"postgres"`
}

cfg, err := config.Load[Config](config.Options{EnvPrefix: "APP"})
pool, err := postgres.Connect(ctx, postgres.Config{DSN: cfg.Postgres.DSN.Value()})
```

- Öncelik: `APP_*` env > `config.<APP_ENV>.yaml` > `config.yaml` > varsayılanlar.
- Struct'taki **her** alan env ile ezilebilir (yaml'da bulunmasa bile): `postgres.dsn` → `APP_POSTGRES_DSN`.
- `validate` tag'leri ve opsiyonel `Validate() error` metodu yüklemede çalışır → eksik config ile servis açılmaz.
- `config.Secret` loglanırken / JSON'a çevrilirken `******` olarak maskelenir.

### Downstream servis çağrıları

```go
inventory := httpclient.New(httpclient.Config{Name: "inventory-service", Timeout: 2 * time.Second, RetryMax: 2})

req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/stock/"+url.PathEscape(id), nil)
resp, err := inventory.Do(req) // 5xx → ErrUpstream, breaker açık → 503 upstream_unavailable
```

- Downstream başına **bir** client oluştur (breaker durumu paylaşılır).
- POST/PATCH yalnızca `Idempotency-Key` header'ı varsa retry edilir.
- 4xx ve caller'ın iptal ettiği istekler breaker'ı açmaz.
- Metrikler: `http_client_request_duration_seconds`, `http_client_circuit_breaker_state`.

### Veritabanı + health + shutdown

```go
pool, err := postgres.Connect(ctx, postgres.Config{DSN: cfg.Postgres.DSN.Value()})
if err != nil {
	return err
}
svc.Health.Add("postgres", pool)                 // /readyz'e eklenir
svc.OnShutdown("postgres", postgres.Close(pool)) // HTTP kapandıktan sonra kapanır

err = postgres.WithTx(ctx, pool, func(tx pgx.Tx) error { ... })
if postgres.IsUniqueViolation(err) { return domain.ErrProductAlreadyExists }
```

Kapanış sırası: readiness `draining` → `http.drain_delay` → HTTP server (açık istekler biter) →
`OnShutdown` hook'ları (ters sırada) → tracing flush → log sync. Hepsi `http.shutdown_timeout` bütçesi içinde.

### Loglama

```go
logger.FromContext(ctx).Info("order created", zap.String("order_id", id))
// {"msg":"order created","order_id":"...","trace_id":"...","span_id":"...","request_id":"...","service":"...",...}
```

## Konfigürasyon referansı

Env adları `EnvPrefix: "APP"` içindir.

| Anahtar | Env | Varsayılan | Açıklama |
|---|---|---|---|
| `app.name` | `APP_APP_NAME` | — (zorunlu) | Servis adı (log, trace, metrik) |
| `app.env` | `APP_ENV` | `local` | Ortam; `config.<env>.yaml` overlay'ini de seçer |
| `app.version` | `APP_APP_VERSION` | `dev` | Build'de `-X main.version` ile de verilebilir |
| `http.host` / `http.port` | `APP_HTTP_PORT` | `""` / `8080` | Dinlenen adres |
| `http.read_timeout` / `write_timeout` / `idle_timeout` | | `10s` / `10s` / `60s` | Bağlantı timeout'ları |
| `http.request_timeout` | `APP_HTTP_REQUEST_TIMEOUT` | `5s` | Handler context deadline'ı (`0` kapatır) |
| `http.shutdown_timeout` | | `15s` | Tüm kapanış için toplam süre |
| `http.drain_delay` | | `0s` | Readiness düştükten sonra bekleme (K8s'te `5s` önerilir) |
| `http.body_limit` | | `4194304` | Maksimum body (byte) |
| `http.access_log` | | `true` | Her istek için tek satır access log |
| `http.cors_origins` | `APP_HTTP_CORS_ORIGINS` | `[]` | Boşsa CORS kapalı; `a.com,b.com` |
| `log.level` / `log.format` | `APP_LOG_LEVEL` | `info` / `json` | `debug\|info\|warn\|error`, `json\|console` |
| `tracing.enabled` | `APP_TRACING_ENABLED` | `false` | OTLP/HTTP export |
| `tracing.endpoint` | `APP_TRACING_ENDPOINT` | — | `host:4318`; boşsa `OTEL_EXPORTER_OTLP_*` |
| `tracing.insecure` | | `true` | TLS'siz OTLP |
| `tracing.sample_ratio` | | `1.0` | Kök span örnekleme oranı (parent-based) |
| `metrics.enabled` / `metrics.path` | | `true` / `/metrics` | Prometheus endpoint'i |

> `APP_ENV` hem ortam seçici hem `app.env` değeridir. `EnvPrefix` boş bırakılırsa anahtarlar prefix'siz okunur (`HTTP_PORT`).

## HTTP davranışı

**Middleware sırası:** requestid → OpenTelemetry → request logger → Prometheus → access log → hata render'ı → panic recover → CORS → request timeout → (sizin middleware'leriniz) → route.
Hatalar zincirin içinde render edildiği için metrik, access log ve span'ler **gerçek** status kodunu görür.

**Hata gövdesi** (tüm servislerde aynı):

```json
{
  "error": {
    "code": "validation_failed",
    "message": "request validation failed",
    "details": [{ "field": "name", "rule": "min", "param": "2" }],
    "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
    "request_id": "0b8a4b6e-0f3e-4a8b-9a55-6b3c8e1f0a11"
  }
}
```

5xx'lerde mesaj her zaman `internal server error`'dır; gerçek sebep `trace_id` / `request_id` ile loglanır.

**Operasyonel endpoint'ler** (trace, metrik ve access log dışı): `/livez`, `/readyz`, `/metrics`.

**Metrikler:** `http_request_duration_seconds{route,method,status}` (route şablonu, ham path değil),
`http_requests_in_flight`, Go runtime ve process metrikleri.

## Versiyonlama ve yayınlama

Semver, `v0.x` ile başlanır (breaking change → minor artar). Core ve adapter'lar ayrı etiketlenir:

```bash
git tag v0.1.0                       # github.com/hekanemre/go-core
git tag adapters/postgres/v0.1.0     # github.com/hekanemre/go-core/adapters/postgres
git tag adapters/couchbase/v0.1.0    # github.com/hekanemre/go-core/adapters/couchbase
git push origin --tags
```

Servislerde: `go get github.com/hekanemre/go-core@v0.1.0`. Repo private ise:

```bash
go env -w GOPRIVATE=github.com/hekanemre/*
```

Core'u bir servisle aynı anda geliştirirken servisin `go.mod`'una
`replace github.com/hekanemre/go-core => ../go-core` ekle ya da `go work init ./go-core ./order-service` kullan.
Değişiklikler [CHANGELOG.md](CHANGELOG.md)'ye yazılır.

## Geliştirme

```bash
make test         # tüm modüller (scaffold uçtan uca testi dahil: servis üretip build + vet + test eder)
make test-short   # hızlı testler
make lint         # golangci-lint (tüm modüller)
make vuln         # govulncheck
make scaffold-demo
```

CI (GitHub Actions): üç modül için `go mod tidy -diff`, `go vet`, golangci-lint, `go test -race`
(Postgres adapter'ı gerçek bir Postgres'e karşı), ayrıca `govulncheck`.

## Blueprint'e göre iyileştirmeler

Blueprint'teki tasarım korunarak açık kalan noktalar kapatıldı:

- **Env override bug'ı çözüldü:** Viper yalnızca bildiği anahtarları env'den okur; `config.Load` hedef struct'tan tüm
  anahtarları türetip bind eder → secret'ların yaml'da boş durmasına gerek yok.
- **`config.Secret`** tipi ve yüklemede **validation** (`validate` tag + `Validate()`), eksik config'le açılış engellenir.
- **Doğru status her yerde:** hatalar middleware zincirinin içinde render edilir; otelfiber'ın ErrorHandler'ı ikinci
  kez çağırması, metrikte hatalı isteklerin 200 görünmesi ve access log'daki yanlış status sorunları yok.
- **request_id** header'ı, loglarda ve hata gövdesinde; 5xx'lerde span'e hata kaydı.
- **Panic → 500** JSON yanıtı + stack'li log; eşleşmeyen route'lar metrikte `unmatched` (kardinalite patlaması yok).
- **Validation alan adları** client'ın gördüğü isimlerle (`json`/`params`/`query` tag'i), iç içe alanlar dahil (`items[0].name`).
- **httpclient:** gobreaker v2 (generic), idempotent olmayan isteklerde otomatik retry kapatma, retry logları
  context logger'ına, breaker durumu ve süre metrikleri.
- **Readiness** check hatalarını dışarı sızdırmaz (host/IP), loglar; check panikleri yakalanır.
- **Lifecycle:** `run` paniği yakalanır, ikinci sinyal process'i hemen öldürür, kapanış adımı süreleri loglanır.
- **Adapter'lar ayrı modül**, Postgres DSN parse hatası şifreyi sızdırmaz, `WithTx` panik/rollback güvenli.
- **`gocore new` scaffolder:** şablon core'un CI'ında her seferinde üretilip build + vet + test edilir, yani şablon hiçbir
  zaman core API'siyle uyumsuz kalmaz.
