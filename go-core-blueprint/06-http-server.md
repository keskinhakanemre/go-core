# 06 — HTTP Server (Fiber) ve Generic Handler

## Kaynak projede (`main.go`)

```go
type HandlerInterface[R Request, Res Response] interface {
	Handle(ctx context.Context, req *R) (*Res, error)
}

func handle[R Request, Res Response](handler HandlerInterface[R, Res]) fiber.Handler {
	// BodyParser → ParamsParser → QueryParser → ReqHeaderParser → Handle → c.JSON(res)
}
```

- Fiber config: `IdleTimeout 5s`, `ReadTimeout 10s`, `WriteTimeout 10s`, `Concurrency 256*1024`.
- Middleware: `otelfiber.Middleware()` + Prometheus `RequestDurationMiddleware`.
- Route'lar `main.go` içinde tanımlı.

**Core'a taşınırken eklenenler:**
1. **Validation** (`go-playground/validator`) — kaynak projede hiç yok.
2. **Tipli hata → HTTP status** (apperror) — kaynak projede her hata 500.
3. **Özel başarı status'u** (POST → 201) — kaynak projede hep 200.
4. **Request timeout** — kaynak projede yorum satırında bırakılmış.
5. **Recover** middleware — panic'te process çökmesin.
6. **Request ID** middleware.
7. Merkezi `ErrorHandler`, tutarlı hata JSON'u.
8. Boş body'de parse hatası vermeme (GET istekleri).

> ⚠️ Fiber v2'de path param tag'i **`params`**'dır (`param` değil). Kaynak projedeki `GetProductRequest` `param:"id"` kullanıyor. Doğrusu: `ID string \`params:"id"\``. Query için `query`, header için `reqHeader` tag'i kullanılır.

## `validation/validation.go`

```go
package validation

import (
	"errors"
	"sync"

	"github.com/go-playground/validator/v10"
	"github.com/hekanemre/go-core/apperror"
)

var (
	once sync.Once
	v    *validator.Validate
)

func Validator() *validator.Validate {
	once.Do(func() { v = validator.New(validator.WithRequiredStructEnabled()) })
	return v
}

type FieldError struct {
	Field string `json:"field"`
	Rule  string `json:"rule"`
	Param string `json:"param,omitempty"`
}

// Struct doğrulama yapar; hata varsa KindValidation tipinde apperror döner.
func Struct(s any) error {
	err := Validator().Struct(s)
	if err == nil {
		return nil
	}
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return apperror.Internal(err)
	}
	fields := make([]FieldError, 0, len(verrs))
	for _, fe := range verrs {
		fields = append(fields, FieldError{Field: fe.Field(), Rule: fe.Tag(), Param: fe.Param()})
	}
	return apperror.New(apperror.KindValidation, "validation_failed", "request validation failed").WithDetails(fields)
}
```

## `httpserver/handler.go`

```go
package httpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/hekanemre/go-core/apperror"
	"github.com/hekanemre/go-core/validation"
)

// Handler HTTP'den bağımsız use-case sözleşmesi.
type Handler[Req any, Res any] interface {
	Handle(ctx context.Context, req *Req) (*Res, error)
}

// HandlerFunc struct yazmadan küçük handler'lar için.
type HandlerFunc[Req any, Res any] func(ctx context.Context, req *Req) (*Res, error)

func (f HandlerFunc[Req, Res]) Handle(ctx context.Context, req *Req) (*Res, error) { return f(ctx, req) }

type handleOptions struct {
	status  int
	timeout time.Duration
}

type Option func(*handleOptions)

// WithStatus başarılı yanıtın status kodunu belirler (ör. 201 Created).
func WithStatus(code int) Option { return func(o *handleOptions) { o.status = code } }

// WithTimeout bu endpoint için server varsayılanını ezer.
func WithTimeout(d time.Duration) Option { return func(o *handleOptions) { o.timeout = d } }

// Handle Handler'ı fiber.Handler'a çevirir.
// Body (JSON) + path param + query + header tek bir Req struct'ına parse edilir, doğrulanır, Handle çağrılır.
func Handle[Req any, Res any](h Handler[Req, Res], opts ...Option) fiber.Handler {
	o := handleOptions{status: http.StatusOK}
	for _, opt := range opts {
		opt(&o)
	}

	return func(c *fiber.Ctx) error {
		var req Req

		if len(c.Body()) > 0 {
			if err := c.BodyParser(&req); err != nil {
				return apperror.BadRequest("invalid_body", "request body could not be parsed").Wrap(err)
			}
		}
		if err := c.ParamsParser(&req); err != nil {
			return apperror.BadRequest("invalid_params", "invalid path parameters").Wrap(err)
		}
		if err := c.QueryParser(&req); err != nil {
			return apperror.BadRequest("invalid_query", "invalid query parameters").Wrap(err)
		}
		if err := c.ReqHeaderParser(&req); err != nil {
			return apperror.BadRequest("invalid_headers", "invalid headers").Wrap(err)
		}
		if err := validation.Struct(&req); err != nil {
			return err
		}

		ctx := c.UserContext() // otelfiber span'ı burada
		timeout := o.timeout
		if timeout == 0 {
			if d, ok := c.Locals(localsRequestTimeout).(time.Duration); ok {
				timeout = d
			}
		}
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		res, err := h.Handle(ctx, &req)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return apperror.New(apperror.KindTimeout, "timeout", "request timed out").Wrap(err)
			}
			return err
		}
		if res == nil {
			return c.SendStatus(o.status)
		}
		return c.Status(o.status).JSON(res)
	}
}
```

## `httpserver/error_handler.go`

```go
package httpserver

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/hekanemre/go-core/apperror"
	"github.com/hekanemre/go-core/logger"
	"go.opentelemetry.io/otel/trace"
)

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
	TraceID string `json:"trace_id,omitempty"`
}

// ErrorHandler tüm hataları tek noktada JSON'a çevirir ve loglar.
func ErrorHandler(c *fiber.Ctx, err error) error {
	// Fiber'ın kendi hataları (404 route yok, 405 vb.)
	var fe *fiber.Error
	if errors.As(err, &fe) {
		return c.Status(fe.Code).JSON(fiber.Map{"error": errorBody{Code: "http_error", Message: fe.Message}})
	}

	ae := apperror.From(err)
	status := apperror.HTTPStatus(ae.Kind)
	ctx := c.UserContext()

	log := logger.FromContext(ctx).With(
		zap.String("method", c.Method()),
		zap.String("route", c.Route().Path),
		zap.Int("status", status),
		zap.String("code", ae.Code),
		zap.Error(err),
	)
	if status >= 500 {
		log.Error("request failed")
	} else {
		log.Warn("request rejected")
	}

	var traceID string
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		traceID = sc.TraceID().String()
	}

	return c.Status(status).JSON(fiber.Map{"error": errorBody{
		Code:    ae.Code,
		Message: ae.Message, // Internal için "internal server error"
		Details: ae.Details,
		TraceID: traceID,
	}})
}
```

## `httpserver/server.go`

```go
package httpserver

import (
	"github.com/gofiber/contrib/otelfiber"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"

	"github.com/hekanemre/go-core/config"
	"github.com/hekanemre/go-core/metrics"
)

const localsRequestTimeout = "core.request_timeout"

// New ortak middleware'lerle yapılandırılmış bir Fiber uygulaması döner.
func New(cfg config.HTTPConfig, appName string) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:               appName,
		ReadTimeout:           cfg.ReadTimeout,
		WriteTimeout:          cfg.WriteTimeout,
		IdleTimeout:           cfg.IdleTimeout,
		BodyLimit:             cfg.BodyLimit,
		ErrorHandler:          ErrorHandler,
		DisableStartupMessage: true,
	})

	app.Use(recover.New(recover.Config{EnableStackTrace: true}))
	app.Use(requestid.New())
	app.Use(otelfiber.Middleware(otelfiber.WithNext(skipInfra)))
	app.Use(metrics.FiberMiddleware(skipInfra))
	app.Use(func(c *fiber.Ctx) error {
		c.Locals(localsRequestTimeout, cfg.RequestTimeout)
		return c.Next()
	})

	app.Get("/metrics", adaptor.HTTPHandler(metrics.Handler()))
	return app
}

// skipInfra metrics/health endpoint'lerini trace ve metrikten hariç tutar.
func skipInfra(c *fiber.Ctx) bool {
	switch c.Path() {
	case "/metrics", "/livez", "/readyz":
		return true
	}
	return false
}
```

> Not: Kaynak projedeki `Concurrency: 256*1024` Fiber'ın zaten varsayılan değeridir; ayrıca set etmeye gerek yok.

## Servis tarafında kullanım

```go
// internal/app/product/get_product.go
type GetProductRequest struct {
	ID string `params:"id" validate:"required,uuid4"`
}

type CreateProductRequest struct {
	Name  string  `json:"name"  validate:"required,min=2,max=120"`
	Price float64 `json:"price" validate:"gte=0"`
}
```

```go
// internal/transport/http/routes.go
func Register(app *fiber.App, h Handlers) {
	v1 := app.Group("/api/v1")
	products := v1.Group("/products")
	products.Get("/:id", httpserver.Handle(h.GetProduct))
	products.Post("/", httpserver.Handle(h.CreateProduct, httpserver.WithStatus(fiber.StatusCreated)))
}
```

Go generic tip çıkarımı sayesinde `Handle[GetProductRequest, GetProductResponse](...)` yazmaya gerek kalmaz; handler struct'ı tipleri belirler.

## Handler unit test örneği (Fiber'sız)

```go
func TestCreateProduct(t *testing.T) {
	repo := &fakeRepo{}
	h := product.NewCreateProductHandler(repo)
	res, err := h.Handle(context.Background(), &product.CreateProductRequest{Name: "kalem"})
	require.NoError(t, err)
	require.NotEmpty(t, res.ID)
}
```

## Route için entegrasyon testi (Fiber ile)

```go
app := httpserver.New(cfg.HTTP, "test")
transporthttp.Register(app, handlers)
req := httptest.NewRequest(http.MethodGet, "/api/v1/products/abc", nil)
resp, _ := app.Test(req)
require.Equal(t, 422, resp.StatusCode) // uuid4 validation
```
