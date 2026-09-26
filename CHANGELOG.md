# Changelog

Bu proje [Semantic Versioning](https://semver.org/) kullanır. `v1.0.0` öncesinde minor sürümler breaking change içerebilir.

## [Unreleased]

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
