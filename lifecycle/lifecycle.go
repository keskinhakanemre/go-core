// Package lifecycle runs a service until it receives SIGINT/SIGTERM (or its
// main loop fails) and then executes shutdown hooks in reverse registration
// order within a single time budget.
//
// Register hooks in dependency order - tracing, then databases, then the HTTP
// server - and they will be closed as: HTTP server -> databases -> tracing.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// Hook is a shutdown function; it must return once ctx is done.
type Hook func(ctx context.Context) error

type hook struct {
	name string
	fn   Hook
}

type Lifecycle struct {
	mu      sync.Mutex
	hooks   []hook
	timeout time.Duration
	signals []os.Signal
}

// New creates a Lifecycle whose shutdown hooks share shutdownTimeout (default 15s).
func New(shutdownTimeout time.Duration) *Lifecycle {
	if shutdownTimeout <= 0 {
		shutdownTimeout = 15 * time.Second
	}
	return &Lifecycle{timeout: shutdownTimeout, signals: []os.Signal{os.Interrupt, syscall.SIGTERM}}
}

// OnShutdown registers a hook. Hooks run in reverse order of registration.
func (l *Lifecycle) OnShutdown(name string, fn Hook) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hooks = append(l.hooks, hook{name: name, fn: fn})
}

// Run starts run in a goroutine and blocks until ctx is cancelled, a
// termination signal arrives, or run returns. It then executes all hooks and
// returns the joined error of run and the hooks.
//
// run should block while serving (e.g. app.Listen) and return nil after a
// graceful stop.
func (l *Lifecycle) Run(ctx context.Context, run func() error) error {
	ctx, stop := signal.NotifyContext(ctx, l.signals...)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				errCh <- fmt.Errorf("lifecycle: run panicked: %v", p)
			}
		}()
		errCh <- run()
	}()

	var runErr error
	select {
	case <-ctx.Done():
		zap.L().Info("shutdown initiated", zap.NamedError("reason", context.Cause(ctx)))
	case runErr = <-errCh:
		if runErr != nil {
			zap.L().Error("service stopped unexpectedly", zap.Error(runErr))
		}
	}
	stop() // a second signal now kills the process immediately

	// ctx is already cancelled here; keep its values but not its cancellation.
	return errors.Join(runErr, l.Shutdown(context.WithoutCancel(ctx)))
}

// Shutdown runs every hook once in reverse order, bounded by the shutdown
// timeout. It is called by Run; call it directly only when not using Run.
func (l *Lifecycle) Shutdown(ctx context.Context) error {
	l.mu.Lock()
	hooks := l.hooks
	l.hooks = nil
	l.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()

	var errs []error
	for i := len(hooks) - 1; i >= 0; i-- {
		h := hooks[i]
		start := time.Now()
		if err := h.fn(ctx); err != nil {
			zap.L().Error("shutdown step failed", zap.String("step", h.name), zap.Error(err))
			errs = append(errs, fmt.Errorf("shutdown %s: %w", h.name, err))
			continue
		}
		zap.L().Info("shutdown step done", zap.String("step", h.name), zap.Duration("took", time.Since(start)))
	}
	return errors.Join(errs...)
}
