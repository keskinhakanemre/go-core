# 09 — Persistence (Repository Pattern)

## Kaynak projede

- `app/product/repository.go`: tüketici tarafında tanımlanmış interface.
- `infra/couchbase/repository.go`: `gocb/v2` ile implementasyon, `gocb-opentelemetry` tracer, 3s timeout'lar, JSON transcoder.
- `infra/postgres/repository.go`: sadece TODO iskelet — **aynı interface'i farklı DB ile implement edebilme** fikrini gösteriyor.

## Core'a ne girer, ne girmez?

| Parça | Nereye |
|---|---|
| Repository interface'leri | **Servis** (`internal/app/<feature>/repository.go`) |
| Repository implementasyonları | **Servis** (`internal/infra/<db>/`) |
| Bağlantı açma, pool ayarları, tracing, health `Ping` | **Core** (`adapters/<db>/`) — opsiyonel |

Core'un domain entity'lerini bilmesi gerekmez; sadece "tracing'li, timeout'lu, health-check'li bağlantı" üretir.

## Couchbase adapter (`adapters/couchbase/couchbase.go`)

```go
package couchbase

import (
	"context"
	"fmt"
	"time"

	gocbopentelemetry "github.com/couchbase/gocb-opentelemetry"
	"github.com/couchbase/gocb/v2"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type Config struct {
	URL      string
	Username string
	Password string
	Bucket   string
	Timeout  time.Duration // varsayılan 3s
}

type DB struct {
	Cluster *gocb.Cluster
	Bucket  *gocb.Bucket
	Tracer  *gocbopentelemetry.OpenTelemetryRequestTracer
}

func Connect(cfg Config, tp *sdktrace.TracerProvider) (*DB, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 3 * time.Second
	}
	opts := gocb.ClusterOptions{
		Authenticator: gocb.PasswordAuthenticator{Username: cfg.Username, Password: cfg.Password},
		TimeoutsConfig: gocb.TimeoutsConfig{
			ConnectTimeout: cfg.Timeout, KVTimeout: cfg.Timeout, QueryTimeout: cfg.Timeout,
		},
		Transcoder: gocb.NewJSONTranscoder(),
	}
	var tracer *gocbopentelemetry.OpenTelemetryRequestTracer
	if tp != nil {
		tracer = gocbopentelemetry.NewOpenTelemetryRequestTracer(tp)
		opts.Tracer = tracer
	}

	cluster, err := gocb.Connect(cfg.URL, opts)
	if err != nil {
		return nil, fmt.Errorf("couchbase connect: %w", err)
	}
	bucket := cluster.Bucket(cfg.Bucket)
	if err := bucket.WaitUntilReady(cfg.Timeout, nil); err != nil {
		_ = cluster.Close(nil)
		return nil, fmt.Errorf("couchbase bucket %q not ready: %w", cfg.Bucket, err)
	}
	return &DB{Cluster: cluster, Bucket: bucket, Tracer: tracer}, nil
}

// Ping health.Checker'ı implement eder.
func (d *DB) Ping(ctx context.Context) error {
	_, err := d.Bucket.Ping(&gocb.PingOptions{Context: ctx})
	return err
}

func (d *DB) Close(ctx context.Context) error { return d.Cluster.Close(nil) }
```

Servis tarafındaki repository:

```go
// internal/infra/couchbase/product_repository.go
type ProductRepository struct{ db *couchbase.DB }

func (r *ProductRepository) GetProduct(ctx context.Context, id string) (*domain.Product, error) {
	res, err := r.db.Bucket.DefaultCollection().Get(id, &gocb.GetOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return nil, domain.ErrProductNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("couchbase get product %s: %w", id, err)
	}
	var p domain.Product
	if err := res.Content(&p); err != nil {
		return nil, fmt.Errorf("couchbase decode product %s: %w", id, err)
	}
	return &p, nil
}

func (r *ProductRepository) CreateProduct(ctx context.Context, p *domain.Product) error {
	_, err := r.db.Bucket.DefaultCollection().Insert(p.ID, p, &gocb.InsertOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentExists) {
		return domain.ErrProductAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("couchbase insert product %s: %w", p.ID, err)
	}
	return nil
}
```

> Couchbase span'lerini HTTP span'ine bağlamak için kaynak projedeki gibi `ParentSpan: gocbopentelemetry.NewOpenTelemetryRequestSpan(ctx, span)` kullanılabilir. Gerekirse küçük bir helper'ı core adapter'a ekle.

## Postgres adapter (`adapters/postgres/postgres.go`) — önerilen stack

Kaynak projede boş. Önerilen kütüphaneler:

- **`github.com/jackc/pgx/v5/pgxpool`** — driver + connection pool
- **`github.com/exaring/otelpgx`** — pgx için OpenTelemetry tracing
- **`sqlc`** — SQL'den tip güvenli Go kodu üretimi (ORM yerine)
- **`golang-migrate/migrate`** veya **`pressly/goose`** — migration

```go
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	DSN             string // postgres://user:pass@host:5432/db?sslmode=disable
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
}

func Connect(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres parse dsn: %w", err)
	}
	if cfg.MaxConns > 0 { pc.MaxConns = cfg.MaxConns }
	if cfg.MinConns > 0 { pc.MinConns = cfg.MinConns }
	if cfg.MaxConnLifetime > 0 { pc.MaxConnLifetime = cfg.MaxConnLifetime }
	pc.ConnConfig.Tracer = otelpgx.NewTracer()

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("postgres connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return pool, nil
}
```

`*pgxpool.Pool` zaten `Ping(ctx) error` metoduna sahip → doğrudan `health.Checker` olarak kullanılabilir.

Hata eşleme örneği:

```go
if errors.Is(err, pgx.ErrNoRows) { return nil, domain.ErrProductNotFound }
var pgErr *pgconn.PgError
if errors.As(err, &pgErr) && pgErr.Code == "23505" { return domain.ErrProductAlreadyExists }
```

## Kurallar

- Repository'ler DB hatalarını **domain hatalarına** çevirir; app katmanı `gocb`/`pgx` import etmez.
- Repository'ler loglamaz, sarmalayıp döner (`fmt.Errorf("...: %w", err)`).
- Her DB çağrısı `ctx` alır (timeout ve trace için).
- Bağlantı hatası açılışta **fatal** olmalı (kaynak projede `zap.L().Fatal` ile yapılıyor, core'da `error` dönülüp `main` karar veriyor).
- Bucket/tablo adı gibi değerler config'ten gelir (kaynak projede `"products"` sabit).
- Transaction gerekiyorsa `UnitOfWork`/`WithTx(ctx, func(tx) error)` pattern'i servis seviyesinde kurulur.
