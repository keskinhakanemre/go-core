// Package service wires the core packages into a ready to run HTTP service:
// logger, tracing, Fiber server with the core middleware chain, /metrics,
// /livez, /readyz and graceful shutdown.
//
//	svc, err := service.New(ctx, cfg.BaseConfig)
//	if err != nil { return err }
//	db, err := postgres.Connect(ctx, ...)
//	svc.Health.Add("postgres", db)
//	svc.OnShutdown("postgres", func(context.Context) error { db.Close(); return nil })
//	routes.Register(svc.HTTP, handlers)
//	return svc.Run(ctx)
//
// Shutdown order: readiness flips to "draining" -> optional drain delay ->
// HTTP server stops accepting and finishes in-flight requests -> hooks
// registered with OnShutdown (reverse order) -> tracing flush -> log sync.
package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/hekanemre/go-core/config"
	"github.com/hekanemre/go-core/health"
	"github.com/hekanemre/go-core/httpserver"
	"github.com/hekanemre/go-core/lifecycle"
	"github.com/hekanemre/go-core/logger"
	"github.com/hekanemre/go-core/tracing"
)

type Service struct {
	Config config.BaseConfig
	Logger *zap.Logger
	HTTP   *fiber.App
	Health *health.Registry

	lc *lifecycle.Lifecycle
}

type options struct {
	httpOpts      []httpserver.Option
	healthTimeout time.Duration
}

// Option customises New.
type Option func(*options)

// WithHTTPOptions passes options to httpserver.New (extra middleware, fiber config, ...).
func WithHTTPOptions(opts ...httpserver.Option) Option {
	return func(o *options) { o.httpOpts = append(o.httpOpts, opts...) }
}

// WithHealthTimeout bounds each readiness evaluation (default 2s).
func WithHealthTimeout(d time.Duration) Option {
	return func(o *options) { o.healthTimeout = d }
}

// New initialises logging, tracing, the HTTP server and health endpoints.
func New(ctx context.Context, cfg config.BaseConfig, opts ...Option) (*Service, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	log, _, err := logger.New(logger.Config{
		Level: cfg.Log.Level, Format: cfg.Log.Format,
		Service: cfg.App.Name, Version: cfg.App.Version, Env: cfg.App.Env,
	})
	if err != nil {
		return nil, fmt.Errorf("service: logger: %w", err)
	}

	lc := lifecycle.New(cfg.HTTP.ShutdownTimeout)

	shutdownTracing, err := tracing.Init(ctx, tracing.Config{
		Enabled:     cfg.Tracing.Enabled,
		Endpoint:    cfg.Tracing.Endpoint,
		Insecure:    cfg.Tracing.Insecure,
		SampleRatio: cfg.Tracing.SampleRatio,
		ServiceName: cfg.App.Name,
		Version:     cfg.App.Version,
		Env:         cfg.App.Env,
	})
	if err != nil {
		return nil, fmt.Errorf("service: %w", err)
	}
	lc.OnShutdown("tracing", lifecycle.Hook(shutdownTracing))

	metricsPath := ""
	if cfg.Metrics.Enabled {
		metricsPath = cfg.Metrics.Path
	}
	httpOpts := append([]httpserver.Option{
		httpserver.WithAppName(cfg.App.Name),
		httpserver.WithMetricsPath(metricsPath),
	}, o.httpOpts...)
	app := httpserver.New(cfg.HTTP, httpOpts...) //nolint:contextcheck // handlers use the per-request context

	hr := health.New(o.healthTimeout)
	hr.Register(app)

	return &Service{Config: cfg, Logger: log, HTTP: app, Health: hr, lc: lc}, nil
}

// OnShutdown registers a hook that runs after the HTTP server stopped and
// before tracing is flushed. Hooks run in reverse registration order.
func (s *Service) OnShutdown(name string, fn lifecycle.Hook) { s.lc.OnShutdown(name, fn) }

// Run listens on Config.HTTP.Addr() and blocks until shutdown completes.
func (s *Service) Run(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.Config.HTTP.Addr())
	if err != nil {
		return fmt.Errorf("service: listen %s: %w", s.Config.HTTP.Addr(), err)
	}
	return s.Serve(ctx, ln)
}

// Serve is Run with a caller provided listener (useful for tests).
func (s *Service) Serve(ctx context.Context, ln net.Listener) error {
	defer func() { _ = s.Logger.Sync() }()

	drainDelay := s.Config.HTTP.DrainDelay
	s.lc.OnShutdown("http", func(ctx context.Context) error {
		s.Health.SetDraining()
		if drainDelay > 0 {
			// Give load balancers time to observe the failing readiness probe.
			select {
			case <-time.After(drainDelay):
			case <-ctx.Done():
			}
		}
		return s.HTTP.ShutdownWithContext(ctx)
	})

	// service, version and env are already logger fields.
	s.Logger.Info("service starting", zap.String("addr", ln.Addr().String()))
	err := s.lc.Run(ctx, func() error {
		if err := s.HTTP.Listener(ln); err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return nil
	})
	if err == nil {
		s.Logger.Info("service stopped")
	}
	return err
}
