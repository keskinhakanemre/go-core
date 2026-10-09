package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/keskinhakanemre/go-core/metrics"
)

// Fiber reuses request buffers when Immutable is off; label values must be
// copies, or later requests rewrite the stored "method" labels and /metrics
// fails with duplicate series.
func TestLabelsSurviveBufferReuse(t *testing.T) {
	app := fiber.New() // Immutable: false
	app.Use(metrics.FiberMiddleware(nil))
	app.All("/label-test", func(c *fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })

	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodGet, http.MethodDelete} {
		resp, err := app.Test(httptest.NewRequest(m, "/label-test", nil))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather (duplicate series?): %v", err)
	}
	seen := map[string]bool{}
	for _, f := range families {
		if f.GetName() != "http_request_duration_seconds" {
			continue
		}
		for _, m := range f.GetMetric() {
			var route, method string
			for _, l := range m.GetLabel() {
				switch l.GetName() {
				case "route":
					route = l.GetValue()
				case "method":
					method = l.GetValue()
				}
			}
			if route == "/label-test" {
				seen[method] = true
			}
		}
	}
	for _, m := range []string{"GET", "POST", "PUT", "DELETE"} {
		if !seen[m] {
			t.Errorf("missing method label %q, have %v", m, seen)
		}
	}
	if len(seen) != 4 {
		t.Errorf("unexpected method labels: %v", seen)
	}
}
