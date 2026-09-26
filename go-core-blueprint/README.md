# Go Core Blueprint

Bu klasör, `production-ready-microservice-example` projesinin analiz edilmesiyle çıkarılmış, **tüm backend projelerinde tekrar kullanılacak bir Go "core" altyapısının** tasarım ve uygulama rehberidir.

Amaç: Bu klasörü başka bir dizine kopyalayıp, buradaki dokümanlara bakarak (elle veya Claude Code ile) sıfırdan bir `go-core` modülü üretmek ve bundan sonraki her servisi bu core üzerine kurmak.

## Kaynak projede neler var? (Özet)

| Konu | Kaynak projede kullanılan | Core'a alınacak mı? |
|---|---|---|
| HTTP framework | Fiber v2 | ✅ (httpserver paketi) |
| Generic handler (`Handle[Req, Res]`) | `main.go` içinde | ✅ (en değerli parça, iyileştirilerek) |
| Config | Viper + `config/config.yaml` | ✅ (env override eklenerek) |
| Logging | Zap, JSON, global logger | ✅ (trace_id ile zenginleştirilerek) |
| Tracing | OpenTelemetry + OTLP HTTP → Jaeger | ✅ (shutdown + sampling eklenerek) |
| Metrics | Prometheus histogram middleware + `/metrics` | ✅ |
| Dış HTTP çağrıları | retryablehttp + gobreaker + otelhttp | ✅ (tek bir `httpclient` paketinde) |
| Veritabanı | Couchbase (+ boş Postgres) repository | ⚠️ Core'a değil, adapter paketi / servis içine |
| Health check | `/healthcheck` | ✅ (`/livez` + `/readyz` olarak) |
| Graceful shutdown | SIGINT/SIGTERM + Fiber shutdown | ✅ (tracer/DB kapatma eklenerek) |
| Docker | Multi-stage, non-root | ✅ (template) |
| Docker Compose | Jaeger, Prometheus, Grafana | ✅ (template) |
| Kubernetes | Deployment, Service, HPA | ✅ (probe ve limitler eklenerek) |

## Doküman sırası

| # | Dosya | İçerik |
|---|---|---|
| 01 | [01-mimari.md](01-mimari.md) | Katmanlı mimari, bağımlılık yönü, kaynak projenin analizi |
| 02 | [02-core-modul-yapisi.md](02-core-modul-yapisi.md) | Core modülünün klasör yapısı ve dağıtım stratejisi |
| 03 | [03-config.md](03-config.md) | Viper tabanlı generic config yükleyici |
| 04 | [04-logging.md](04-logging.md) | Zap logger, trace-aware log |
| 05 | [05-errors.md](05-errors.md) | Tipli uygulama hataları ve HTTP status eşlemesi |
| 06 | [06-http-server.md](06-http-server.md) | Fiber server, generic handler, middleware, validation |
| 07 | [07-observability.md](07-observability.md) | OpenTelemetry tracing + Prometheus metrics |
| 08 | [08-http-client-resilience.md](08-http-client-resilience.md) | Retry + circuit breaker + timeout'lu HTTP client |
| 09 | [09-persistence.md](09-persistence.md) | Repository pattern, Couchbase/Postgres adapter'ları |
| 10 | [10-health-lifecycle.md](10-health-lifecycle.md) | Health check'ler, graceful shutdown, `main.go` kompozisyonu |
| 11 | [11-deployment.md](11-deployment.md) | Dockerfile, docker-compose, Kubernetes, Prometheus/Grafana |
| 12 | [12-kaynak-projedeki-sorunlar.md](12-kaynak-projedeki-sorunlar.md) | Orijinal projede tespit edilen hatalar/eksikler ve core'daki çözümleri |
| 13 | [13-yeni-servis-rehberi.md](13-yeni-servis-rehberi.md) | Core'u kullanarak yeni servis açma + yeni endpoint ekleme adımları |
| 14 | [14-claude-ile-kurulum.md](14-claude-ile-kurulum.md) | Bu dokümanlarla Claude Code'a core'u ürettirme promptları + `CLAUDE.md` şablonu |

## Hızlı başlangıç

1. Bu `go-core-blueprint` klasörünü yeni bir dizine kopyala (ör. `C:\dev\go-core\docs`).
2. [02-core-modul-yapisi.md](02-core-modul-yapisi.md)'deki yapıyı oluştur.
3. 03 → 10 arası dokümanlardaki kodları ilgili paketlere yerleştir (veya [14-claude-ile-kurulum.md](14-claude-ile-kurulum.md)'deki promptu kullan).
4. `go mod tidy && go build ./... && go test ./...`
5. Yeni servisleri [13-yeni-servis-rehberi.md](13-yeni-servis-rehberi.md)'ye göre aç.

## Yer tutucular

Dokümanlarda şu yer tutucular kullanılıyor, kendi değerlerinle değiştir:

- `github.com/hekanemre/go-core` → core modülünün import yolu
- `github.com/hekanemre/<servis-adi>` → servis modülünün import yolu
- `<servis-adi>` → servis adı (ör. `product-service`)
