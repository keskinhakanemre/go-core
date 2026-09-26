package httpserver_test

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

	"github.com/keskinhakanemre/go-core/apperror"
	"github.com/keskinhakanemre/go-core/config"
	"github.com/keskinhakanemre/go-core/httpserver"
	"github.com/keskinhakanemre/go-core/validation"
)

var errItemNotFound = apperror.NotFound("item_not_found", "item not found")

type getItemReq struct {
	ID     string `params:"id" validate:"required,uuid4"`
	Expand bool   `query:"expand"`
	Tenant string `reqHeader:"X-Tenant"`
}

type item struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Expand bool   `json:"expand"`
	Tenant string `json:"tenant"`
}

type createItemReq struct {
	Name string `json:"name" validate:"required,min=2"`
}

const (
	knownID   = "0b8a4b6e-0f3e-4a8b-9a55-6b3c8e1f0a11"
	missingID = "7c1e2d3f-4a5b-4c6d-8e7f-9a0b1c2d3e4f"
)

func newApp(t *testing.T, cfg config.HTTPConfig) *fiber.App {
	t.Helper()
	app := httpserver.New(cfg)

	app.Get("/items/:id", httpserver.HandleFunc(func(_ context.Context, r *getItemReq) (*item, error) {
		if r.ID != knownID {
			return nil, errItemNotFound
		}
		return &item{ID: r.ID, Name: "pen", Expand: r.Expand, Tenant: r.Tenant}, nil
	}))
	app.Post("/items", httpserver.HandleFunc(func(_ context.Context, r *createItemReq) (*item, error) {
		return &item{ID: knownID, Name: r.Name}, nil
	}, httpserver.WithStatus(http.StatusCreated)))
	app.Delete("/items/:id", httpserver.HandleFunc(func(context.Context, *getItemReq) (*struct{}, error) {
		return &struct{}{}, nil
	}, httpserver.WithStatus(http.StatusNoContent)))
	app.Get("/boom", httpserver.HandleFunc(func(context.Context, *struct{}) (*item, error) {
		return nil, errors.New("pq: connection refused to 10.0.0.1")
	}))
	app.Get("/panic", httpserver.HandleFunc(func(context.Context, *struct{}) (*item, error) {
		panic("kaboom")
	}))
	app.Get("/slow", httpserver.HandleFunc(func(ctx context.Context, _ *struct{}) (*item, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
			return &item{}, nil
		}
	}, httpserver.WithTimeout(20*time.Millisecond)))
	return app
}

func do(t *testing.T, app *fiber.App, req *http.Request) (*http.Response, string) {
	t.Helper()
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, string(b)
}

func decodeErr(t *testing.T, body string) httpserver.ErrorBody {
	t.Helper()
	var r httpserver.ErrorResponse
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("invalid error body %q: %v", body, err)
	}
	return r.Error
}

func TestHandleBindsAllSources(t *testing.T) {
	app := newApp(t, config.HTTPConfig{})
	req := httptest.NewRequest(http.MethodGet, "/items/"+knownID+"?expand=true", nil)
	req.Header.Set("X-Tenant", "acme")
	resp, body := do(t, app, req)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got item
	_ = json.Unmarshal([]byte(body), &got)
	if got.ID != knownID || !got.Expand || got.Tenant != "acme" {
		t.Fatalf("unexpected response %+v", got)
	}
	if resp.Header.Get(httpserver.HeaderRequestID) == "" {
		t.Fatal("missing request id header")
	}
}

func TestHandleStatusCodes(t *testing.T) {
	app := newApp(t, config.HTTPConfig{})

	resp, body := do(t, app, jsonReq(http.MethodPost, "/items", `{"name":"pen"}`))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}

	resp, body = do(t, app, httptest.NewRequest(http.MethodDelete, "/items/"+knownID, nil))
	if resp.StatusCode != http.StatusNoContent || body != "" {
		t.Fatalf("delete: %d %q", resp.StatusCode, body)
	}
}

func TestErrorMapping(t *testing.T) {
	app := newApp(t, config.HTTPConfig{})
	cases := []struct {
		name   string
		req    *http.Request
		status int
		code   string
	}{
		{"typed not found", httptest.NewRequest(http.MethodGet, "/items/"+missingID, nil), 404, "item_not_found"},
		{"validation", httptest.NewRequest(http.MethodGet, "/items/not-a-uuid", nil), 422, "validation_failed"},
		{"invalid body", jsonReq(http.MethodPost, "/items", `{"name":`), 400, "invalid_body"},
		{"route not found", httptest.NewRequest(http.MethodGet, "/nope", nil), 404, "not_found"},
		{"method not allowed", httptest.NewRequest(http.MethodPut, "/items", nil), 405, "method_not_allowed"},
		{"internal", httptest.NewRequest(http.MethodGet, "/boom", nil), 500, "internal_error"},
		{"panic", httptest.NewRequest(http.MethodGet, "/panic", nil), 500, "internal_error"},
		{"timeout", httptest.NewRequest(http.MethodGet, "/slow", nil), 504, "timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := do(t, app, tc.req)
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d (%s)", resp.StatusCode, tc.status, body)
			}
			eb := decodeErr(t, body)
			if eb.Code != tc.code {
				t.Fatalf("code = %q, want %q", eb.Code, tc.code)
			}
			if eb.RequestID == "" {
				t.Fatal("missing request_id in error body")
			}
		})
	}
}

func TestInternalErrorsDoNotLeak(t *testing.T) {
	app := newApp(t, config.HTTPConfig{})
	_, body := do(t, app, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if strings.Contains(body, "10.0.0.1") || strings.Contains(body, "pq:") {
		t.Fatalf("internal details leaked: %s", body)
	}
}

func TestValidationDetails(t *testing.T) {
	app := newApp(t, config.HTTPConfig{})
	_, body := do(t, app, jsonReq(http.MethodPost, "/items", `{"name":"x"}`))
	var r struct {
		Error struct {
			Details []validation.FieldError `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &r)
	if len(r.Error.Details) != 1 || r.Error.Details[0].Field != "name" || r.Error.Details[0].Rule != "min" {
		t.Fatalf("unexpected details: %s", body)
	}
}

func TestServerRequestTimeout(t *testing.T) {
	app := httpserver.New(config.HTTPConfig{RequestTimeout: 10 * time.Millisecond})
	app.Get("/wait", httpserver.HandleFunc(func(ctx context.Context, _ *struct{}) (*item, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	resp, body := do(t, app, httptest.NewRequest(http.MethodGet, "/wait", nil))
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
}

func TestMetricsEndpointRecordsFinalStatus(t *testing.T) {
	app := newApp(t, config.HTTPConfig{AccessLog: true})
	do(t, app, httptest.NewRequest(http.MethodGet, "/items/"+missingID, nil))
	do(t, app, httptest.NewRequest(http.MethodGet, "/random/garbage/path", nil))

	resp, body := do(t, app, httptest.NewRequest(http.MethodGet, httpserver.PathMetrics, nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status %d", resp.StatusCode)
	}
	want := `http_request_duration_seconds_count{method="GET",route="/items/:id",status="404"}`
	if !strings.Contains(body, want) {
		t.Fatalf("expected %s in metrics output", want)
	}
	if strings.Contains(body, "/random/garbage/path") {
		t.Fatal("raw path leaked into metric labels")
	}
	if strings.Contains(body, `route="/metrics"`) {
		t.Fatal("metrics endpoint must not be measured")
	}
}

func TestCORS(t *testing.T) {
	app := httpserver.New(config.HTTPConfig{CORSOrigins: []string{"https://example.com"}})
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendString("ok") })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://example.com")
	resp, _ := do(t, app, req)
	if resp.Header.Get("Access-Control-Allow-Origin") != "https://example.com" {
		t.Fatal("CORS header missing")
	}
}

func TestCustomMiddlewareAndMetricsDisabled(t *testing.T) {
	app := httpserver.New(config.HTTPConfig{},
		httpserver.WithMetricsPath(""),
		httpserver.WithMiddleware(func(c *fiber.Ctx) error {
			if c.Get("Authorization") == "" {
				return apperror.Unauthorized("unauthorized", "missing token")
			}
			return c.Next()
		}),
	)
	app.Get("/secure", func(c *fiber.Ctx) error { return c.SendString("ok") })

	resp, body := do(t, app, httptest.NewRequest(http.MethodGet, "/secure", nil))
	if resp.StatusCode != http.StatusUnauthorized || decodeErr(t, body).Code != "unauthorized" {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	resp, _ = do(t, app, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if resp.StatusCode == http.StatusOK {
		t.Fatal("metrics endpoint should be disabled")
	}
}

func jsonReq(method, target, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}
