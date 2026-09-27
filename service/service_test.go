package service_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keskinhakanemre/go-core/apperror"
	"github.com/keskinhakanemre/go-core/config"
	"github.com/keskinhakanemre/go-core/health"
	"github.com/keskinhakanemre/go-core/httpserver"
	"github.com/keskinhakanemre/go-core/service"
)

type pingRes struct {
	Message string `json:"message"`
}

func baseConfig(t *testing.T) config.BaseConfig {
	t.Helper()
	t.Setenv("APP_NAME", "test-svc")
	cfg, err := config.Load[config.BaseConfig](config.Options{Paths: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Log.Level = "error"
	return *cfg
}

func TestServiceEndToEnd(t *testing.T) {
	cfg := baseConfig(t)
	svc, err := service.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var order []string
	record := func(name string) func(context.Context) error {
		return func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, name)
			return nil
		}
	}
	svc.OnShutdown("db", record("db"))
	svc.OnShutdown("cache", record("cache"))
	svc.Health.Add("db", health.CheckerFunc(func(context.Context) error { return nil }))

	svc.HTTP.Get("/ping", httpserver.HandleFunc(func(context.Context, *struct{}) (*pingRes, error) {
		return &pingRes{Message: "pong"}, nil
	}))
	svc.HTTP.Get("/missing", httpserver.HandleFunc(func(context.Context, *struct{}) (*pingRes, error) {
		return nil, apperror.NotFound("missing", "missing")
	}))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Serve(ctx, ln) }()

	expect := func(path string, status int, contains string) {
		t.Helper()
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != status || !strings.Contains(string(b), contains) {
			t.Fatalf("GET %s = %d %s, want %d containing %q", path, resp.StatusCode, b, status, contains)
		}
	}
	expect("/ping", 200, "pong")
	expect("/missing", 404, `"code":"missing"`)
	expect("/livez", 200, "ok")
	expect("/readyz", 200, `"db":"ok"`)
	expect("/metrics", 200, "http_request_duration_seconds")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service did not stop")
	}

	if !svc.Health.Draining() {
		t.Fatal("readiness must be draining after shutdown")
	}
	if !slices.Equal(order, []string{"cache", "db"}) {
		t.Fatalf("unexpected hook order %v", order)
	}
	if _, err := http.Get(base + "/ping"); err == nil {
		t.Fatal("server should be closed")
	}
}

func TestRunFailsOnBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	cfg := baseConfig(t)
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	cfg.HTTP.Host, cfg.HTTP.Port = "127.0.0.1", port

	svc, err := service.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Run(context.Background()); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("expected listen error, got %v", err)
	}
}

func TestDocsEndpoints(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		cfg := baseConfig(t)
		cfg.Docs.Enabled = enabled
		svc, err := service.New(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		httpserver.Get(svc.API, "/ping", httpserver.Func(func(context.Context, *struct{}) (*pingRes, error) {
			return &pingRes{Message: "pong"}, nil
		}))

		for _, path := range []string{"/openapi.json", "/docs"} {
			resp, err := svc.HTTP.Test(httptest.NewRequest(http.MethodGet, path, nil))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			switch {
			case enabled && resp.StatusCode != http.StatusOK:
				t.Fatalf("docs enabled: GET %s = %d", path, resp.StatusCode)
			case !enabled && resp.StatusCode != http.StatusNotFound:
				t.Fatalf("docs disabled: GET %s = %d", path, resp.StatusCode)
			case enabled && path == "/openapi.json" && (!strings.Contains(string(b), `"/ping"`) || !strings.Contains(string(b), `"title":"test-svc"`)):
				t.Fatalf("spec does not describe the service: %s", b)
			}
		}
	}
}
