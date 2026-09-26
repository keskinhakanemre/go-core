# 10 — Health Check, Lifecycle ve `main.go` Kompozisyonu

## Kaynak projede

- `/healthcheck` → her zaman `{"status":"OK"}` (DB durumuna bakmıyor). Kendisi de generic handler pattern'i ile yazılmış.
- Graceful shutdown: `SIGINT`/`SIGTERM` beklenir, `app.ShutdownWithTimeout(5s)`.
- **Eksik:** Tracer provider, Couchbase bağlantısı ve logger kapanışta düzgün kapatılmıyor. Server goroutine'inde hata olursa `os.Exit(1)` → defer'lar çalışmaz.

## Health: liveness vs readiness

| Endpoint | Soru | Kontrol | K8s probe |
|---|---|---|---|
| `/livez` | Process yaşıyor mu? | Hiçbir dış bağımlılık kontrol edilmez | `livenessProbe` (başarısızsa pod restart) |
| `/readyz` | Trafik alabilir mi? | DB ping, kritik bağımlılıklar | `readinessProbe` (başarısızsa trafik kesilir) |

> Liveness'ta DB kontrol etme: DB çöktüğünde tüm pod'lar restart döngüsüne girer.

### `health/health.go`

```go
package health

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Checker bir bağımlılığın sağlığını kontrol eder (pgxpool.Pool, couchbase.DB vb. zaten uyumlu).
type Checker interface {
	Ping(ctx context.Context) error
}

type CheckerFunc func(ctx context.Context) error

func (f CheckerFunc) Ping(ctx context.Context) error { return f(ctx) }

type Registry struct {
	mu       sync.RWMutex
	checks   map[string]Checker
	draining atomic.Bool
	timeout  time.Duration
}

func New() *Registry {
	return &Registry{checks: map[string]Checker{}, timeout: 2 * time.Second}
}

func (r *Registry) Add(name string, c Checker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = c
}

// SetDraining kapanış başladığında readiness'ı false yapar (yeni trafik gelmesin).
func (r *Registry) SetDraining() { r.draining.Store(true) }

func (r *Registry) Register(app *fiber.App) {
	app.Get("/livez", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	app.Get("/readyz", func(c *fiber.Ctx) error {
		if r.draining.Load() {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"status": "draining"})
		}
		ctx, cancel := context.WithTimeout(c.UserContext(), r.timeout)
		defer cancel()

		r.mu.RLock()
		defer r.mu.RUnlock()

		results := make(map[string]string, len(r.checks))
		healthy := true
		var wg sync.WaitGroup
		var mu sync.Mutex
		for name, chk := range r.checks {
			wg.Add(1)
			go func(name string, chk Checker) {
				defer wg.Done()
				status := "ok"
				if err := chk.Ping(ctx); err != nil {
					status = err.Error()
					mu.Lock(); healthy = false; mu.Unlock()
				}
				mu.Lock(); results[name] = status; mu.Unlock()
			}(name, chk)
		}
		wg.Wait()

		code := fiber.StatusOK
		if !healthy {
			code = fiber.StatusServiceUnavailable
		}
		return c.Status(code).JSON(fiber.Map{"status": map[bool]string{true: "ok", false: "fail"}[healthy], "checks": results})
	})
}
```

## Lifecycle

### `lifecycle/lifecycle.go`

```go
package lifecycle

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
)

type closer struct {
	name string
	fn   func(ctx context.Context) error
}

type Lifecycle struct {
	closers []closer
	timeout time.Duration
}

func New(shutdownTimeout time.Duration) *Lifecycle {
	return &Lifecycle{timeout: shutdownTimeout}
}

// OnShutdown kapanışta çağrılacak fonksiyonu ekler. Eklenme sırasının TERSİNE çalışır
// (önce server, sonra DB, en son tracer).
func (l *Lifecycle) OnShutdown(name string, fn func(ctx context.Context) error) {
	l.closers = append(l.closers, closer{name, fn})
}

// Run start fonksiyonunu çalıştırır; sinyal gelene veya start hata dönene kadar bekler,
// sonra kayıtlı closer'ları sırayla çalıştırır.
func (l *Lifecycle) Run(start func() error) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- start() }()

	var runErr error
	select {
	case <-ctx.Done():
		zap.L().Info("shutdown signal received")
	case runErr = <-errCh:
		if runErr != nil {
			zap.L().Error("server stopped with error", zap.Error(runErr))
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), l.timeout)
	defer cancel()

	var errs []error
	for i := len(l.closers) - 1; i >= 0; i-- {
		c := l.closers[i]
		if err := c.fn(shutdownCtx); err != nil {
			zap.L().Error("shutdown step failed", zap.String("step", c.name), zap.Error(err))
			errs = append(errs, err)
		} else {
			zap.L().Info("shutdown step done", zap.String("step", c.name))
		}
	}
	return errors.Join(append([]error{runErr}, errs...)...)
}
```

## Servisin `cmd/api/main.go`'su (tam kompozisyon)

```go
package main

import (
	"context"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/keskinhakanemre/go-core/health"
	"github.com/keskinhakanemre/go-core/httpclient"
	"github.com/keskinhakanemre/go-core/httpserver"
	"github.com/keskinhakanemre/go-core/lifecycle"
	"github.com/keskinhakanemre/go-core/logger"
	"github.com/keskinhakanemre/go-core/tracing"
	cbadapter "github.com/keskinhakanemre/go-core/adapters/couchbase"

	"github.com/keskinhakanemre/product-service/internal/app/product"
	"github.com/keskinhakanemre/product-service/internal/config"
	cbrepo "github.com/keskinhakanemre/product-service/internal/infra/couchbase"
	"github.com/keskinhakanemre/product-service/internal/infra/inventory"
	transporthttp "github.com/keskinhakanemre/product-service/internal/transport/http"
)

func main() {
	if err := run(); err != nil {
		zap.L().Error("application exited with error", zap.Error(err))
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// 1) Config
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// 2) Logger
	_, syncLog, err := logger.New(logger.Config{
		Level: cfg.Log.Level, Format: cfg.Log.Format,
		Service: cfg.App.Name, Version: cfg.App.Version, Env: cfg.App.Env,
	})
	if err != nil {
		return err
	}
	defer syncLog()

	lc := lifecycle.New(cfg.HTTP.ShutdownTimeout)

	// 3) Tracing
	shutdownTracing, err := tracing.Init(ctx, tracing.Config{
		Enabled: cfg.Tracing.Enabled, Endpoint: cfg.Tracing.Endpoint, Insecure: cfg.Tracing.Insecure,
		SampleRatio: cfg.Tracing.SampleRatio, ServiceName: cfg.App.Name, Version: cfg.App.Version, Env: cfg.App.Env,
	})
	if err != nil {
		return err
	}
	lc.OnShutdown("tracing", shutdownTracing)

	// 4) Altyapı (DB, downstream client'lar)
	db, err := cbadapter.Connect(cbadapter.Config{
		URL: cfg.Couchbase.URL, Username: cfg.Couchbase.Username,
		Password: cfg.Couchbase.Password, Bucket: cfg.Couchbase.Bucket,
	}, nil)
	if err != nil {
		return err
	}
	lc.OnShutdown("couchbase", db.Close)

	inventoryClient := inventory.NewClient(cfg.Downstream.ExampleServiceURL, httpclient.New(httpclient.Config{
		Name: "inventory-service", Timeout: 2 * time.Second, RetryMax: 2,
	}))

	// 5) Repository + handler'lar
	productRepo := cbrepo.NewProductRepository(db)
	handlers := transporthttp.Handlers{
		GetProduct:    product.NewGetProductHandler(productRepo, inventoryClient),
		CreateProduct: product.NewCreateProductHandler(productRepo),
	}

	// 6) HTTP server
	app := httpserver.New(cfg.HTTP, cfg.App.Name)
	hr := health.New()
	hr.Add("couchbase", db)
	hr.Register(app)
	transporthttp.Register(app, handlers)

	lc.OnShutdown("http", func(ctx context.Context) error {
		hr.SetDraining()
		return app.ShutdownWithContext(ctx)
	})

	zap.L().Info("server starting", zap.String("port", cfg.HTTP.Port))
	return lc.Run(func() error { return app.Listen(":" + cfg.HTTP.Port) })
}
```

**Kapanış sırası** (OnShutdown ters sırada çalışır): `http` → `couchbase` → `tracing` → (defer) `logger sync`.
Yani önce yeni istek alımı durur ve açık istekler biter, sonra DB kapanır, en son kalan span'ler gönderilir.

> Kubernetes'te pod silinirken endpoint'ten çıkarılması birkaç saniye sürebilir. İstenirse `SetDraining()` sonrası 3-5 sn `time.Sleep` eklenip sonra server kapatılır; `terminationGracePeriodSeconds` bu toplamdan büyük olmalı.
