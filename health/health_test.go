package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/keskinhakanemre/go-core/health"
)

func get(t *testing.T, app *fiber.App, path string) (int, health.Report, string) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var rep health.Report
	_ = json.Unmarshal(b, &rep)
	return resp.StatusCode, rep, string(b)
}

func TestReadiness(t *testing.T) {
	app := fiber.New()
	reg := health.New(50 * time.Millisecond)
	reg.Register(app)

	reg.Add("db", health.CheckerFunc(func(context.Context) error { return nil }))
	if code, rep, _ := get(t, app, "/readyz"); code != 200 || rep.Checks["db"] != "ok" {
		t.Fatalf("code=%d rep=%+v", code, rep)
	}

	reg.Add("cache", health.CheckerFunc(func(context.Context) error {
		return errors.New("dial tcp 10.1.2.3:6379: connection refused")
	}))
	code, rep, body := get(t, app, "/readyz")
	if code != 503 || rep.Status != "fail" || rep.Checks["cache"] != "fail" || rep.Checks["db"] != "ok" {
		t.Fatalf("code=%d rep=%+v", code, rep)
	}
	if strings.Contains(body, "10.1.2.3") {
		t.Fatal("check error details must not be exposed")
	}
}

func TestReadinessTimeoutAndPanic(t *testing.T) {
	app := fiber.New()
	reg := health.New(20 * time.Millisecond)
	reg.Register(app)
	reg.Add("slow", health.CheckerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	reg.Add("buggy", health.CheckerFunc(func(context.Context) error { panic("x") }))

	code, rep, _ := get(t, app, "/readyz")
	if code != 503 || rep.Checks["slow"] != "fail" || rep.Checks["buggy"] != "fail" {
		t.Fatalf("code=%d rep=%+v", code, rep)
	}
}

func TestDrainingAndLiveness(t *testing.T) {
	app := fiber.New()
	reg := health.New(0)
	reg.Register(app)
	reg.SetDraining()

	if code, rep, _ := get(t, app, "/readyz"); code != 503 || rep.Status != "draining" {
		t.Fatalf("code=%d rep=%+v", code, rep)
	}
	if code, _, _ := get(t, app, "/livez"); code != 200 {
		t.Fatal("liveness must stay up while draining")
	}
}
