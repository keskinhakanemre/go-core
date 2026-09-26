// Package httpserver builds a Fiber application with production defaults and
// adapts transport independent handlers to Fiber routes.
//
// Middleware chain (outermost first):
//
//	requestid -> otel tracing -> request logger context -> prometheus metrics
//	-> access log -> error renderer -> panic recover -> CORS -> request timeout -> routes
//
// Because errors are rendered inside the chain, metrics, access logs and trace
// spans all observe the final HTTP status.
package httpserver

import (
	"strings"

	"github.com/gofiber/contrib/otelfiber/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"

	"github.com/keskinhakanemre/go-core/config"
	"github.com/keskinhakanemre/go-core/metrics"
)

const localsRequestTimeout = "gocore.request_timeout"

// HeaderRequestID is the header used to read/propagate the request id.
const HeaderRequestID = fiber.HeaderXRequestID

// Paths of the operational endpoints. They are excluded from tracing, metrics and access logs.
const (
	PathLivez   = "/livez"
	PathReadyz  = "/readyz"
	PathMetrics = "/metrics"
)

type serverOptions struct {
	appName     string
	metricsPath string
	middlewares []fiber.Handler
	fiberConfig []func(*fiber.Config)
	skipPaths   map[string]struct{}
}

// Option customises New.
type Option func(*serverOptions)

// WithAppName sets the Fiber application name.
func WithAppName(name string) Option { return func(o *serverOptions) { o.appName = name } }

// WithMetricsPath changes the metrics endpoint path. An empty path disables it.
func WithMetricsPath(path string) Option { return func(o *serverOptions) { o.metricsPath = path } }

// WithMiddleware appends middlewares that run after the core chain and before routes
// (e.g. authentication, rate limiting).
func WithMiddleware(h ...fiber.Handler) Option {
	return func(o *serverOptions) { o.middlewares = append(o.middlewares, h...) }
}

// WithFiberConfig lets callers adjust the fiber.Config before the app is created.
// ErrorHandler should normally be left untouched.
func WithFiberConfig(fn func(*fiber.Config)) Option {
	return func(o *serverOptions) { o.fiberConfig = append(o.fiberConfig, fn) }
}

// WithSkipPaths excludes additional exact paths from tracing, metrics and access logs.
func WithSkipPaths(paths ...string) Option {
	return func(o *serverOptions) {
		for _, p := range paths {
			o.skipPaths[p] = struct{}{}
		}
	}
}

// New returns a Fiber application configured from cfg with the core middleware chain.
func New(cfg config.HTTPConfig, opts ...Option) *fiber.App {
	o := serverOptions{
		metricsPath: PathMetrics,
		skipPaths:   map[string]struct{}{PathLivez: {}, PathReadyz: {}},
	}
	for _, opt := range opts {
		opt(&o)
	}
	if o.metricsPath != "" {
		o.skipPaths[o.metricsPath] = struct{}{}
	}
	skip := func(c *fiber.Ctx) bool {
		_, ok := o.skipPaths[c.Path()]
		return ok
	}

	fcfg := fiber.Config{
		AppName:               o.appName,
		ReadTimeout:           cfg.ReadTimeout,
		WriteTimeout:          cfg.WriteTimeout,
		IdleTimeout:           cfg.IdleTimeout,
		BodyLimit:             cfg.BodyLimit,
		ErrorHandler:          ErrorHandler,
		DisableStartupMessage: true,
	}
	for _, fn := range o.fiberConfig {
		fn(&fcfg)
	}
	app := fiber.New(fcfg)

	app.Use(requestid.New(requestid.Config{Header: HeaderRequestID}))
	app.Use(otelfiber.Middleware(otelfiber.WithNext(skip), otelfiber.WithoutMetrics(true)))
	app.Use(requestLogger)
	app.Use(metrics.FiberMiddleware(skip))
	if cfg.AccessLog {
		app.Use(accessLog(skip))
	}
	app.Use(renderErrors)
	app.Use(recover.New(recover.Config{EnableStackTrace: true, StackTraceHandler: logPanic}))
	if len(cfg.CORSOrigins) > 0 {
		app.Use(cors.New(cors.Config{
			AllowOrigins:  strings.Join(cfg.CORSOrigins, ","),
			ExposeHeaders: HeaderRequestID,
		}))
	}
	if cfg.RequestTimeout > 0 {
		timeout := cfg.RequestTimeout
		app.Use(func(c *fiber.Ctx) error {
			c.Locals(localsRequestTimeout, timeout)
			return c.Next()
		})
	}
	for _, m := range o.middlewares {
		app.Use(m)
	}

	if o.metricsPath != "" {
		app.Get(o.metricsPath, adaptor.HTTPHandler(metrics.Handler()))
	}
	return app
}
