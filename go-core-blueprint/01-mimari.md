# 01 — Mimari

## Kaynak projenin katmanları

```
main.go                 → Composition root: her şeyi oluşturur ve birbirine bağlar
app/<feature>/          → Use-case / handler katmanı (iş akışı)
  ├── *_handler.go      → Request/Response DTO + Handle(ctx, *Req) (*Res, error)
  └── repository.go     → Bu feature'ın İHTİYAÇ duyduğu repository interface'i
domain/                 → Saf domain modelleri (framework bağımsız)
infra/<teknoloji>/      → Interface'lerin somut implementasyonları (Couchbase, Postgres)
pkg/                    → Paylaşılan altyapı (config, log)
config/config.yaml      → Uygulama ayarları
.deploy/                → K8s, Prometheus, Grafana
```

## Bağımlılık yönü (en önemli kural)

```
            ┌──────────────┐
            │   main.go    │  (her şeyi bilir, wiring yapar)
            └──────┬───────┘
       ┌───────────┼─────────────┐
       ▼           ▼             ▼
  ┌─────────┐ ┌─────────┐  ┌──────────┐
  │  app/*  │ │ infra/* │  │  pkg/*   │
  └────┬────┘ └────┬────┘  └──────────┘
       │           │
       ▼           ▼
     ┌───────────────┐
     │    domain/    │  (kimseye bağımlı değil)
     └───────────────┘
```

- `domain` hiçbir iç pakete bağımlı değildir.
- `app/<feature>` sadece `domain`'e ve **kendi tanımladığı interface'lere** bağımlıdır.
- `infra/*`, `app`'in tanımladığı interface'leri **implicit olarak** (Go'nun structural typing'i ile) implement eder; `app`'i import etmez.
- `app` asla `infra`'yı import etmez. Bağlantıyı `main.go` kurar.

> Kaynak projede bu kural doğru uygulanmış: `app/product/repository.go` interface'i tüketici tarafta tanımlıyor ("accept interfaces, return structs"). Core'da da bu korunacak.

## Handler pattern (kaynak projenin kalbi)

Her endpoint, HTTP'den bağımsız bir struct:

```go
type CreateProductRequest struct { Name string `json:"name"` }
type CreateProductResponse struct { ID string `json:"id"` }

type CreateProductHandler struct { repository Repository }

func (h *CreateProductHandler) Handle(ctx context.Context, req *CreateProductRequest) (*CreateProductResponse, error)
```

`main.go`'daki generic `handle[Req, Res]` fonksiyonu bu handler'ı `fiber.Handler`'a çevirir:
body + path param + query + header'ı tek bir `Req` struct'ına parse eder, `Handle`'ı çağırır, sonucu JSON döner.

**Kazanımları:**
- Handler'lar Fiber'dan bağımsız → saf unit test yazılabilir.
- Her endpoint aynı imzaya sahip → tutarlılık, kolay code review.
- Transport değiştirmek (gRPC, queue consumer) handler'ı etkilemez.

Core'da bu pattern `httpserver.Handle` olarak genelleştirilecek; validation, tipli hata → HTTP status eşlemesi ve özel status code desteği eklenecek (bkz. [06-http-server.md](06-http-server.md)).

## Core'a göre hedef mimari

```
go-core (ayrı modül, tüm servisler import eder)
  config / logger / apperror / httpserver / httpclient / tracing / metrics / health / lifecycle / validation

<servis> (her projede)
  cmd/api/main.go            → composition root (core paketlerini kullanır)
  internal/config/           → servise özel config (core BaseConfig'i embed eder)
  internal/domain/           → entity + domain hataları
  internal/app/<feature>/    → handler'lar + repository interface'leri
  internal/infra/<teknoloji>/→ repository implementasyonları
  internal/transport/http/   → route tanımları
  config/config.yaml
  deploy/ Dockerfile docker-compose.yml Makefile
```

`internal/` kullanımı: servis kodunun başka modüller tarafından import edilmesini derleyici seviyesinde engeller. Kaynak proje `internal` kullanmıyor; core tabanlı servislerde kullanılması önerilir.
