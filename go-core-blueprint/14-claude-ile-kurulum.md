# 14 — Claude Code ile Core'u ve Servisleri Üretmek

## 1) Core modülünü ürettirmek

Yeni bir dizin aç, bu `go-core-blueprint` klasörünü içine `docs/` olarak kopyala:

```
C:\dev\go-core\
└── docs\            ← bu klasörün içeriği
```

O dizinde Claude Code'u açıp şu promptu ver:

```text
docs/ klasöründeki blueprint dokümanlarını (README.md ve 01–13) baştan sona oku.
Bu dokümanlara göre `github.com/keskinhakanemre/go-core` adında bir Go modülü oluştur:

- 02-core-modul-yapisi.md'deki klasör yapısını kur (templates/ hariç).
- config, logger, apperror, validation, httpserver, metrics, tracing,
  httpclient, health, lifecycle paketlerini 03–10'daki kodlara göre yaz.
  Kodlar doküman örneğidir; derlenmeyen yerleri düzelt, API'leri
  kullandığın kütüphane sürümüne göre doğrula.
- adapters/couchbase ve adapters/postgres'i ayrı alt paketler olarak ekle.
- Her paket için anlamlı unit testler yaz (özellikle apperror, config.Load,
  httpserver.Handle + ErrorHandler için app.Test ile testler).
- Makefile ve .golangci.yml ekle.
- `go mod tidy && go vet ./... && go test ./...` temiz geçene kadar düzelt.
- Sonunda paketlerin kısa kullanımını anlatan bir README.md yaz.
```

## 2) Servis şablonunu ürettirmek

Core hazır olduktan sonra aynı repoda:

```text
docs/13-yeni-servis-rehberi.md, 10-health-lifecycle.md ve 11-deployment.md'yi oku.
templates/service/ altına örnek bir "product-service" iskeleti oluştur:
cmd/api/main.go, internal/{config,domain,app/product,infra/couchbase,transport/http},
config/config.yaml, Dockerfile, .dockerignore, docker-compose.yml, Makefile,
deploy/{k8s,prometheus,grafana}, CLAUDE.md.
go.mod'da core için `replace github.com/keskinhakanemre/go-core => ../..` kullan
ve iskeletin derlendiğini doğrula.
```

## 3) Yeni bir backend projesi başlatmak

```text
C:\dev\go-core\templates\service iskeletini kopyalayarak
C:\dev\<yeni-servis> altında `github.com/keskinhakanemre/<yeni-servis>` modülünü oluştur.
Product örneğini kaldır, yerine <domain açıklaması> için şu endpoint'leri ekle: ...
go-core'u `replace` ile ../go-core'dan kullan.
```

## 4) Servislere konacak `CLAUDE.md` şablonu

Her servis reposunun köküne koy; Claude Code her oturumda bunu okur ve core kurallarına uyar:

```markdown
# <servis-adi>

Bu servis `github.com/keskinhakanemre/go-core` üzerine kuruludur.

## Komutlar
- Çalıştır: `make run` (önce `docker compose up -d jaeger prometheus grafana <db>`)
- Test: `make test`
- Lint: `make lint`

## Mimari kurallar
- Katmanlar: `internal/domain` ← `internal/app/<feature>` ← `internal/infra/*`; wiring sadece `cmd/api/main.go`.
- `app` paketleri `fiber`, `gocb`, `pgx` import ETMEZ. Dış bağımlılıklar `ports.go`'daki interface'lerle alınır.
- Her endpoint: `XxxRequest` / `XxxResponse` / `XxxHandler` + `Handle(ctx, *Req) (*Res, error)`.
  Route kaydı `internal/transport/http/routes.go`'da `httpserver.Handle(...)` ile.
- Request tag'leri: body `json`, path `params`, query `query`, header `reqHeader`; doğrulama `validate`.

## Hata kuralları
- Domain hataları `internal/domain/errors.go`'da `apperror.NotFound/Conflict/...` ile tanımlanır.
- Repository'ler DB hatalarını domain hatalarına çevirir; diğerlerini `fmt.Errorf("...: %w", err)` ile sarar.
- Hata ya loglanır ya döndürülür; loglama merkezi ErrorHandler'da yapılır.

## Observability
- Log: `logger.FromContext(ctx)`; config/secret loglanmaz.
- `ctx` her fonksiyonun ilk parametresi; `context.Background()` sadece main'de.
- Açılan her span için `defer span.End()`.
- Metrik label'larına sınırsız değer (id, email, url) konmaz.

## Dış servis çağrıları
- Sadece `httpclient.Client` üzerinden, `internal/infra/<servis>` altında tipli adapter ile.
- Non-idempotent isteklerde retry kapalı.

## Yeni endpoint eklerken
go-core `docs/13-yeni-servis-rehberi.md` "B" ve "C" bölümlerini takip et.
```

## 5) Core'u güncel tutmak

- Core'da değişiklik → test → `git tag vX.Y.Z` → servislerde `go get github.com/keskinhakanemre/go-core@vX.Y.Z`.
- Breaking change'leri core `CHANGELOG.md`'ye yaz.
- Bir servis içinde "bu her serviste lazım olur" dediğin kod çıktığında → core'a taşı.
