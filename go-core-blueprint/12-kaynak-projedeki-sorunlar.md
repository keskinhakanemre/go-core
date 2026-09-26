# 12 — Kaynak Projede Tespit Edilen Sorunlar ve Core'daki Çözümleri

Kaynak proje iyi bir başlangıç ama bir "örnek" projesi. Core'a **kopyalanmaması** gereken noktalar:

## 🔴 Kritik

| # | Sorun | Yer | Core'daki çözüm |
|---|---|---|---|
| 1 | Her handler hatası **500** dönüyor; "product not found" bile 500 | `main.go` `handle()` | `apperror` Kind → HTTP status ([05](05-errors.md)) |
| 2 | `err.Error()` doğrudan client'a gidiyor → iç detay sızıntısı | `main.go` `handle()` | 5xx'te genel mesaj, detay sadece logda |
| 3 | Şifre `config.yaml`'da ve git'te (`couchbase_password: "123456789"`) | `config/config.yaml` | Env override, secret'lar env/K8s Secret ([03](03-config.md)) |
| 4 | Tüm config (şifre dahil) açılışta loglanıyor: `zap.Any("appConfig", appConfig)` | `main.go` | Config loglanmaz veya maskelenir |
| 5 | `tp.Shutdown()` çağrılmıyor → kapanışta span kaybı | `main.go` | `lifecycle.OnShutdown("tracing", ...)` ([10](10-health-lifecycle.md)) |
| 6 | Path param tag'i yanlış: `param:"id"` (Fiber v2 `params` bekler) | `get_product_handler.go` | `params:"id"` ([06](06-http-server.md)) |
| 7 | Prometheus middleware, hata dönen isteklerde status'u **200** olarak kaydediyor (ErrorHandler henüz çalışmamış) | `RequestDurationMiddleware` | Status hatadan türetilir ([07](07-observability.md)) |

## 🟠 Önemli

| # | Sorun | Yer | Core'daki çözüm |
|---|---|---|---|
| 8 | Request validation yok | tüm handler'lar | `validation` paketi + `validate` tag'leri |
| 9 | Request timeout yorum satırında, uygulanmıyor | `main.go` | `http.request_timeout` + `WithTimeout` |
| 10 | Panic recover middleware yok → panic process'i düşürür | `main.go` | `recover.New()` |
| 11 | Health check DB'ye bakmıyor; liveness/readiness ayrımı yok | `app/healthcheck` | `/livez` + `/readyz` + `Checker` |
| 12 | `UpdateProduct`'ta açılan span `End()` edilmiyor (leak) | `infra/couchbase` | `defer span.End()` kuralı |
| 13 | `WaitUntilReady` hatası yok sayılıyor | `infra/couchbase` | Hata dönülür, açılış fail eder |
| 14 | `CouchbaseRepository.tp` alanı hiç set edilmiyor; bucket adı `"products"` sabit | `infra/couchbase` | Adapter config'ten alır |
| 15 | Couchbase bağlantısı kapanışta kapatılmıyor | `main.go` | `lifecycle.OnShutdown("couchbase", db.Close)` |
| 16 | Aynı hata hem repository'de, hem handler wrapper'da loglanıyor (çift log) | genel | "Ya logla ya dön" kuralı; tek nokta ErrorHandler |
| 17 | Circuit breaker handler içinde oluşturuluyor; 5xx yanıtlar başarı sayılıyor | `get_product_handler.go` | Downstream başına `httpclient.Client` ([08](08-http-client-resilience.md)) |
| 18 | `retryClient.RetryMax = 0` → retry altyapısı var ama kapalı; retryablehttp kendi logger'ıyla stderr'e yazıyor | `main.go` | Config'ten `RetryMax`, `Logger = nil` |
| 19 | Server goroutine'inde `os.Exit(1)` → defer'lar çalışmaz | `main.go` | `lifecycle.Run` hata döner, `main` exit eder |
| 20 | Başarılı POST 201 yerine 200 dönüyor | `handle()` | `httpserver.WithStatus(201)` |

## 🟡 İyileştirme

| # | Sorun | Core'daki çözüm |
|---|---|---|
| 21 | Log level sabit, trace_id loglarda yok | `logger.New(cfg)` + `logger.FromContext(ctx)` |
| 22 | Logger `init()` side-effect + blank import ile kuruluyor | Açık `logger.New()` çağrısı |
| 23 | Servis adı sabit `"microservice-go"`; `AlwaysSample` | Config'ten service name, `ParentBased(TraceIDRatioBased)` |
| 24 | `semconv/v1.4.0` (çok eski) | Güncel semconv sürümü |
| 25 | Global `viper` instance | `viper.New()` ile izole instance |
| 26 | `http.Transport` `MaxIdleConnsPerHost` ayarsız, client toplam timeout yok | `httpclient` varsayılanları |
| 27 | Route'lar, middleware'ler, tracer, metrikler hepsi `main.go`'da | Core paketleri + `transport/http/routes.go` |
| 28 | Test yok, lint yok, Makefile yok | Makefile + golangci-lint + CI ([02](02-core-modul-yapisi.md), [11](11-deployment.md)) |
| 29 | `docker-compose`'da kullanılmayan `JAEGER_AGENT_*` env'leri; Couchbase servisi yok | Compose template'i ([11](11-deployment.md)) |
| 30 | K8s'te probe, memory limit, securityContext, PDB yok; HPA hedefi %5 | K8s template'i ([11](11-deployment.md)) |
| 31 | API versiyonlama yok (`/products`) | `/api/v1/...` grupları |
| 32 | `internal/` kullanılmıyor, giriş noktası kök dizinde | `cmd/api/main.go` + `internal/` |
| 33 | `Concurrency: 256*1024` Fiber varsayılanıyla aynı, gereksiz | Kaldırıldı |

## Kaynak projeden **korunması gereken** iyi fikirler

- ✅ Generic `Handle[Req, Res]` pattern'i — handler'lar HTTP'den bağımsız.
- ✅ Repository interface'inin tüketici pakette tanımlanması.
- ✅ Prometheus'ta `c.Route().Path` ile düşük kardinaliteli route label'ı.
- ✅ W3C TraceContext + Baggage propagator.
- ✅ `otelhttp.NewTransport` ile giden isteklerin trace'e dahil edilmesi.
- ✅ Retry'da `ctx.Err()` kontrolü (iptal edilmiş istek retry edilmez).
- ✅ Circuit breaker state değişimlerinin loglanması.
- ✅ Multi-stage, non-root Dockerfile.
- ✅ Grafana datasource/dashboard provisioning.
- ✅ HPA `behavior` ile agresif scale-up, kontrollü scale-down.
