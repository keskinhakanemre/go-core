# go-core

`github.com/hekanemre/go-core` — servislerin import ettiği core framework. Tasarım gerekçeleri `go-core-blueprint/` altında.

## Komutlar
- Test: `make test` (hızlı: `make test-short`) · Lint: `make lint` · Tidy: `make tidy`
- Üç modül var: kök (`.`), `adapters/postgres`, `adapters/couchbase`. Komutları her modülde ayrı çalıştır (Makefile bunu yapar).

## Kurallar
- Bu bir kütüphane: public API değişikliği = breaking change. `CHANGELOG.md`'ye yaz, gerekiyorsa minor sürümü artır.
- Paketler birbirini minimum import eder. `config` ve `apperror` hiçbir core paketini import etmez;
  `httpserver` → `apperror`, `validation`, `metrics`, `logger`, `config`; `service` hepsini bağlar.
- Adapter modülleri core'u import etmez; ağır bağımlılıklar (DB driver'ları) sadece adapter modüllerinde olur.
- Yeni davranış = test. HTTP davranışı `app.Test` ile, kapanış/ağ davranışı gerçek listener ile test edilir.
- Hatalar ya loglanır ya döndürülür; loglama merkezi `httpserver.ErrorHandler`'da.
- 5xx yanıtlarda ve readiness çıktısında iç detay (host, DSN, SQL) client'a gitmez.
- Godoc yorumları İngilizce, README/CHANGELOG Türkçe.

## Servis şablonu
- `cmd/gocore/templates/service/` altındaki dosyaların hepsi `.tmpl` uzantılıdır, `[[ ]]` delimiter kullanır
  (`{{ }}` GitHub Actions ve Grafana için serbest kalsın diye).
- Şablonu değiştirdikten sonra `go test ./cmd/gocore/` çalıştır: servisi üretip build + vet + test + gofmt kontrolü yapar.
- Core API'sini değiştirirsen şablonu da güncelle; aksi halde bu test kırılır.

## Sürüm çıkarma
`git tag vX.Y.Z` (core), `git tag adapters/postgres/vX.Y.Z`, `git tag adapters/couchbase/vX.Y.Z`, sonra `git push origin --tags`.
