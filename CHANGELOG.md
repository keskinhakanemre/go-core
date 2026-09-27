# Changelog

Bu proje [Semantic Versioning](https://semver.org/) kullanır. `v1.0.0` öncesinde minor sürümler breaking change içerebilir.

## [Unreleased]

## [v0.2.0] - 2026-09-27

### Eklendi
- `openapi`: request/response struct'larından (`params`/`query`/`reqHeader`/`json`/`validate`/`doc`/`example` tag'leri) OpenAPI 3.0 dokümanı üretimi.
- `httpserver.API`: dokümante route kaydı (`Get`/`Post`/`Put`/`Patch`/`Delete`/`Route`, `Group`, `Func`), `ServeDocs` ile `/openapi.json` + Swagger UI.
- Route seçenekleri: `WithSummary`, `WithDescription`, `WithTags`, `WithErrors`, `WithOperationID`, `WithDeprecated`, `WithoutDocs`.
- `config.DocsConfig` (`docs.enabled`, `docs.path`, `docs.spec_path`), varsayılan kapalı.
- `service.Service.API`; `docs.enabled` iken doküman endpoint'leri otomatik açılır.
- Servis şablonu route'ları `svc.API` ile kaydeder; local/staging'de `/docs` açık, prod'da kapalı.

### Değişmedi
- `httpserver.Handle` ve doğrudan Fiber route kaydı aynen çalışır (breaking change yok).

## [v0.1.0] - 2026-09-26

İlk sürüm.

### Eklendi
- `config`: generic `Load[T]`, ortam overlay'i, struct'tan türetilen env binding, validation, `Secret` tipi.
- `logger`: zap logger, `FromContext` / `WithFields` ile trace ve request id'li log.
- `apperror`: tipli hatalar ve HTTP status eşlemesi.
- `validation`: validator sarmalayıcı, client alan isimleri.
- `httpserver`: Fiber app, middleware zinciri, `Handle` / `HandleFunc`, merkezi `ErrorHandler`, CORS, request timeout.
- `metrics`: Prometheus HTTP metrikleri ve `/metrics`.
- `tracing`: OTLP/HTTP TracerProvider, W3C propagation, parent-based sampling.
- `httpclient`: circuit breaker + retry + timeout + tracing + metrik.
- `health`: `/livez`, `/readyz`, draining.
- `lifecycle`: sinyal yakalama ve sıralı kapanış.
- `service`: tüm paketleri bağlayan bootstrap.
- `adapters/postgres` (ayrı modül): pgx pool, otelpgx, `WithTx`, hata yardımcıları.
- `adapters/couchbase` (ayrı modül): gocb bağlantısı, tracing, `Ping`.
- `cmd/gocore`: servis iskeleti üreten CLI.
