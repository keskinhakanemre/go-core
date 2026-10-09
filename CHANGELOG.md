# Changelog

Bu proje [Semantic Versioning](https://semver.org/) kullanır. `v1.0.0` öncesinde minor sürümler breaking change içerebilir.

## [Unreleased]

## [v0.3.1] - 2026-10-09

### Güvenlik
- `golang.org/x/net` v0.60.0'a ve Go toolchain'i go1.26.9'a yükseltildi (GO-2026-6617: HTTP/2 HPACK encoder yarışı
  sunucuyu çökertebiliyordu). Servisler de kendi `toolchain` satırını go1.26.9'a çekmeli.

## [v0.3.0] - 2026-10-09 · adapters/postgres [v0.2.0]

### Eklendi
- `openapi`: güvenlik şemaları — `SecurityScheme`, `BearerJWT`, `Builder.AddSecurityScheme`, `OperationSpec.Security`;
  doküman `components.securitySchemes` ve operasyon `security` alanlarını içerir (Swagger UI "Authorize").
- `httpserver.API`: `AddSecurityScheme`, `Secure(schemes...)` (grup ve alt gruplar için varsayılan güvenlik) ve
  route seçeneği `WithSecurity(schemes...)` (`WithSecurity()` route'u açık işaretler).
- `adapters/postgres`: `ConstraintName(err)` (ihlal edilen kısıtın adı) ve goose ile `Migrate(ctx, pool, fsys)`
  (advisory lock'lu, `[]MigrationResult` döner). Adapter modülü artık `github.com/pressly/goose/v3`'e bağlı.

### Düzeltildi
- `metrics.FiberMiddleware`: `method` ve `route` etiketleri kopyalanarak saklanır. Önceden Fiber'ın (Immutable kapalıyken)
  yeniden kullandığı tampona işaret ediyordu; sonraki istekler etiketleri bozuyor ve `/metrics` "duplicate series"
  hatası veriyordu. Servislerdeki `Immutable: true` geçici çözümüne artık gerek yok.

### Değişmedi
- Mevcut API'ler aynen çalışır (breaking change yok); güvenlik tanımlanmazsa doküman önceki gibidir.

## [v0.2.0] - 2026-09-27

### Eklendi
- `openapi`: request/response struct'larından (`params`/`query`/`reqHeader`/`json`/`validate`/`doc`/`example` tag'leri) OpenAPI 3.0 dokümanı üretimi.
- `httpserver.API`: dokümante route kaydı (`Get`/`Post`/`Put`/`Patch`/`Delete`/`Route`, `Group`, `Func`), `ServeDocs` ile `/openapi.json` + Swagger UI.
- Route seçenekleri: `WithSummary`, `WithDescription`, `WithTags`, `WithErrors`, `WithOperationID`, `WithDeprecated`, `WithoutDocs`.
- `config.DocsConfig` (`docs.enabled`, `docs.path`, `docs.spec_path`), varsayılan kapalı.
- `service.Service.API`; `docs.enabled` iken doküman endpoint'leri otomatik açılır.
- Servis şablonu route'ları `svc.API` ile kaydeder; local/staging'de `/docs` açık, prod'da kapalı.

### Değişti
- CI ve servis şablonunda golangci-lint sürümü `v2.14.0`'a sabitlendi (`latest` ağ hatalarına ve habersiz kırılmalara açıktı).

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
