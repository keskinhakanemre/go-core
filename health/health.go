// Package health implements Kubernetes style liveness and readiness endpoints.
//
//	/livez  - is the process alive? Never checks dependencies (a DB outage must
//	          not restart every pod).
//	/readyz - can the instance take traffic? Runs all registered checks in
//	          parallel and fails while the service is draining.
package health

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// Checker checks a dependency. *pgxpool.Pool, *sql.DB (PingContext aside) and
// the couchbase adapter already satisfy it.
type Checker interface {
	Ping(ctx context.Context) error
}

// CheckerFunc adapts a function to Checker.
type CheckerFunc func(ctx context.Context) error

func (f CheckerFunc) Ping(ctx context.Context) error { return f(ctx) }

const (
	StatusOK       = "ok"
	StatusFail     = "fail"
	StatusDraining = "draining"
)

// Report is the readiness response body.
type Report struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

type Registry struct {
	mu       sync.RWMutex
	checks   map[string]Checker
	draining atomic.Bool
	timeout  time.Duration
}

// New returns an empty registry. timeout bounds each readiness evaluation
// (default 2s when zero).
func New(timeout time.Duration) *Registry {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Registry{checks: map[string]Checker{}, timeout: timeout}
}

// Add registers a readiness check. Adding the same name twice replaces it.
func (r *Registry) Add(name string, c Checker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = c
}

// SetDraining makes readiness fail so load balancers stop sending traffic.
// Called at the start of graceful shutdown.
func (r *Registry) SetDraining() { r.draining.Store(true) }

// Draining reports whether SetDraining was called.
func (r *Registry) Draining() bool { return r.draining.Load() }

// Check runs all checks concurrently and returns the report.
func (r *Registry) Check(ctx context.Context) Report {
	if r.draining.Load() {
		return Report{Status: StatusDraining}
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	r.mu.RLock()
	names := make([]string, 0, len(r.checks))
	for name := range r.checks {
		names = append(names, name)
	}
	sort.Strings(names)
	checks := make([]Checker, len(names))
	for i, name := range names {
		checks[i] = r.checks[name]
	}
	r.mu.RUnlock()

	results := make([]error, len(names))
	var wg sync.WaitGroup
	for i := range checks {
		wg.Go(func() { results[i] = safePing(ctx, checks[i]) })
	}
	wg.Wait()

	rep := Report{Status: StatusOK, Checks: make(map[string]string, len(names))}
	for i, name := range names {
		if err := results[i]; err != nil {
			rep.Status = StatusFail
			rep.Checks[name] = StatusFail
			// Error details can contain hosts/credentials: log them, do not expose them.
			zap.L().Warn("readiness check failed", zap.String("check", name), zap.Error(err))
			continue
		}
		rep.Checks[name] = StatusOK
	}
	return rep
}

func safePing(ctx context.Context, c Checker) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("health check panicked: %v", p)
		}
	}()
	return c.Ping(ctx)
}

// LivenessHandler always answers 200 while the process can serve HTTP.
func (r *Registry) LivenessHandler(c *fiber.Ctx) error {
	return c.JSON(Report{Status: StatusOK})
}

// ReadinessHandler answers 200 when all checks pass, 503 otherwise.
func (r *Registry) ReadinessHandler(c *fiber.Ctx) error {
	rep := r.Check(c.UserContext())
	code := fiber.StatusOK
	if rep.Status != StatusOK {
		code = fiber.StatusServiceUnavailable
	}
	return c.Status(code).JSON(rep)
}

// Register mounts /livez and /readyz on app.
func (r *Registry) Register(app fiber.Router) {
	app.Get("/livez", r.LivenessHandler)
	app.Get("/readyz", r.ReadinessHandler)
}
