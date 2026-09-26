# 07 — Observability: Tracing + Metrics

Kaynak projenin "production-ready" iddiasının ana dayanağı bu bölüm. Üç sinyal:

| Sinyal | Araç | Kaynak projede | Core'da |
|---|---|---|---|
| Traces | OpenTelemetry → OTLP HTTP → Jaeger | `initTracer` (main.go) | `tracing` paketi |
| Metrics | Prometheus client + `/metrics` | `RequestDurationMiddleware` | `metrics` paketi |
| Logs | Zap JSON | `pkg/log` | `logger` paketi + trace_id |

## Tracing

### Kaynak projede

- `otlptracehttp` exporter, `WithInsecure`, `AlwaysSample`, batch span processor.
- Servis adı sabit: `"microservice-go"`.
- Propagator: `TraceContext` + `Baggage` (W3C) → servisler arası trace zinciri.
- Otomatik instrumentation: `otelfiber` (gelen istekler), `otelhttp.NewTransport` (giden istekler), `gocb-opentelemetry` (Couchbase).
- **Eksik:** `tp.Shutdown()` hiç çağrılmıyor → kapanışta buffer'daki span'ler kayboluyor. Sampling sabit %100, tracing kapatılamıyor.

### `tracing/tracing.go`

```go
package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type Config struct {
	Enabled     bool
	Endpoint    string // host:port, ör. "jaeger:4318"
	Insecure    bool
	SampleRatio float64
	ServiceName string
	Version     string
	Env         string
}

// Init global TracerProvider ve propagator'ı kurar. Dönen shutdown fonksiyonu
// kapanışta MUTLAKA çağrılmalı (kalan span'leri flush eder).
func Init(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	// Propagator tracing kapalı olsa bile kurulur → gelen trace header'ları downstream'e taşınır.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("tracing: exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),      // OTEL_RESOURCE_ATTRIBUTES
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
			semconv.ServiceVersionKey.String(cfg.Version),
			attribute.String("deployment.environment", cfg.Env),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("tracing: resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		// Parent örneklenmişse biz de örnekleriz; kök span'lerde oran uygulanır.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}
```

> Couchbase gibi `*sdktrace.TracerProvider` isteyen kütüphaneler için `Init`'in ikinci bir versiyonu tp'yi de dönebilir. Genel kural: kütüphanelere mümkünse `otel.GetTracerProvider()` (interface) ver.

### Manuel span açma (iş mantığında)

```go
var tracer = otel.Tracer("product-service/app/product")

func (h *CreateProductHandler) Handle(ctx context.Context, req *CreateProductRequest) (*CreateProductResponse, error) {
	ctx, span := tracer.Start(ctx, "CreateProduct")
	defer span.End()
	span.SetAttributes(attribute.String("product.name", req.Name))

	if err := h.repo.CreateProduct(ctx, p); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "create failed")
		return nil, err
	}
	...
}
```

Kurallar:
- `ctx`'i her fonksiyona ilk parametre olarak geçir; yeni `context.Background()` açma (trace zinciri kopar).
- Her `Start` için `defer span.End()` (kaynak projede `UpdateProduct`'ta unutulmuş).

## Metrics

### Kaynak projede

```go
http_request_duration_seconds{route, method, status}  // Histogram
```

- `route` label'ı `c.Route().Path` → `/products/:id` (gerçek ID değil) ✅ düşük kardinalite. Bu doğru yaklaşım, korunmalı.
- `/metrics` endpoint `promhttp.Handler()` ile Fiber'a adapte edilmiş.

### `metrics/metrics.go`

```go
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/keskinhakanemre/go-core/apperror"
)

var (
	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Duration of HTTP requests in seconds",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"route", "method", "status"})

	requestsInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "http_requests_in_flight",
		Help: "Current number of HTTP requests being served",
	})
)

// FiberMiddleware istek süresini ölçer. skip true dönerse ölçmez.
func FiberMiddleware(skip func(*fiber.Ctx) bool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if skip != nil && skip(c) {
			return c.Next()
		}
		start := time.Now()
		requestsInFlight.Inc()
		defer requestsInFlight.Dec()

		err := c.Next()

		status := c.Response().StatusCode()
		if err != nil {
			// ErrorHandler henüz çalışmadı; status'u hatadan tahmin et
			if fe, ok := err.(*fiber.Error); ok {
				status = fe.Code
			} else {
				status = statusFromError(err)
			}
		}
		requestDuration.WithLabelValues(c.Route().Path, c.Method(), strconv.Itoa(status)).
			Observe(time.Since(start).Seconds())
		return err
	}
}

func Handler() http.Handler { return promhttp.Handler() }
```

`statusFromError`:

```go
func statusFromError(err error) int {
	return apperror.HTTPStatus(apperror.From(err).Kind)
}
```

> ⚠️ Kaynak projede bir incelik var: Fiber'da middleware `c.Next()`'ten hata döndüğünde `ErrorHandler` henüz çalışmamıştır, bu yüzden `c.Response().StatusCode()` 200 görünür. Hata dönen istekler metrikte **200** olarak sayılıyordu. Yukarıdaki kod bunu düzeltir.

### Servise özel iş metrikleri

```go
var productsCreated = promauto.NewCounter(prometheus.CounterOpts{
	Name: "products_created_total",
	Help: "Total number of products created",
})
```

Kurallar:
- Label değerlerine asla user ID, e-posta, ham URL gibi sınırsız değerler koyma.
- İsimlendirme: `<namespace>_<ad>_<birim>` (`_seconds`, `_bytes`, `_total`).

### Faydalı PromQL (Grafana)

```promql
# p95 latency / route
histogram_quantile(0.95, sum by (le, route) (rate(http_request_duration_seconds_bucket[5m])))
# RPS
sum by (route) (rate(http_request_duration_seconds_count[1m]))
# 5xx oranı
sum(rate(http_request_duration_seconds_count{status=~"5.."}[5m])) / sum(rate(http_request_duration_seconds_count[5m]))
```

Kaynak projede Grafana'ya hazır dashboard olarak `.deploy/grafana/dashboards/10826_migrated.json` (Go runtime metrikleri) provision ediliyor; core template'ine kopyalanabilir.
