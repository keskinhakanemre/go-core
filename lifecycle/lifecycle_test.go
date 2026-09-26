package lifecycle_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/keskinhakanemre/go-core/lifecycle"
)

func TestRunStopsOnContextAndRunsHooksInReverse(t *testing.T) {
	lc := lifecycle.New(time.Second)
	var order []string
	for _, name := range []string{"tracing", "db", "http"} {
		lc.OnShutdown(name, func(context.Context) error {
			order = append(order, name)
			return nil
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()

	err := lc.Run(ctx, func() error { <-stopped; return nil })
	close(stopped)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order, []string{"http", "db", "tracing"}) {
		t.Fatalf("unexpected order %v", order)
	}
}

func TestRunReturnsRunErrorAndHookErrors(t *testing.T) {
	lc := lifecycle.New(time.Second)
	errHook := errors.New("close failed")
	errRun := errors.New("listen failed")
	called := false
	lc.OnShutdown("ok", func(context.Context) error { called = true; return nil })
	lc.OnShutdown("bad", func(context.Context) error { return errHook })

	err := lc.Run(context.Background(), func() error { return errRun })
	if !errors.Is(err, errRun) || !errors.Is(err, errHook) {
		t.Fatalf("expected both errors, got %v", err)
	}
	if !called {
		t.Fatal("hooks after a failing hook must still run")
	}
}

func TestRunRecoversPanic(t *testing.T) {
	err := lifecycle.New(time.Second).Run(context.Background(), func() error { panic("boom") })
	if err == nil {
		t.Fatal("expected panic to be converted into an error")
	}
}

func TestShutdownTimeoutIsShared(t *testing.T) {
	lc := lifecycle.New(20 * time.Millisecond)
	lc.OnShutdown("slow", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	start := time.Now()
	err := lc.Shutdown(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("shutdown did not respect timeout")
	}
	if lc.Shutdown(context.Background()) != nil {
		t.Fatal("hooks must run only once")
	}
}
