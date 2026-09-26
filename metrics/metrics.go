// Package metrics exposes Prometheus RED metrics for HTTP servers and the
// /metrics handler.
//
// Metrics are registered on prometheus.DefaultRegisterer, which also carries the
// Go runtime and process collectors. Register service specific metrics with
// promauto in the same way:
//
//	var ordersCreated = promauto.NewCounter(prometheus.CounterOpts{
//		Name: "orders_created_total", Help: "Total number of created orders",
//	})
//
// Never use unbounded values (ids, emails, raw URLs) as label values.
package metrics

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/hekanemre/go-core/apperror"
)

var (
	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Duration of HTTP requests in seconds.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"route", "method", "status"})

	requestsInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "http_requests_in_flight",
		Help: "Current number of HTTP requests being served.",
	})
)

// FiberMiddleware records request duration and in-flight requests. Requests for
// which skip returns true are not measured.
//
// The route label is the route template (/products/:id), never the raw path,
// which keeps cardinality bounded.
func FiberMiddleware(skip func(*fiber.Ctx) bool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if skip != nil && skip(c) {
			return c.Next()
		}
		start := time.Now()
		requestsInFlight.Inc()
		defer requestsInFlight.Dec()

		err := c.Next()

		status := c.Response().StatusCode()
		if err != nil {
			// The app ErrorHandler has not run yet, so derive the status from the error.
			status = StatusFromError(err)
		}
		requestDuration.WithLabelValues(routeLabel(c, status), c.Method(), strconv.Itoa(status)).
			Observe(time.Since(start).Seconds())
		return err
	}
}

// StatusFromError maps an error returned through a Fiber handler chain to the
// HTTP status the error handler will produce.
func StatusFromError(err error) int {
	var fe *fiber.Error
	if errors.As(err, &fe) {
		return fe.Code
	}
	return apperror.HTTPStatus(apperror.KindOf(err))
}

func routeLabel(c *fiber.Ctx, status int) string {
	if status == http.StatusNotFound && c.Route().Path == "/" && c.Path() != "/" {
		// No route matched: only a catch-all middleware saw the request.
		return "unmatched"
	}
	return c.Route().Path
}

// Handler serves the Prometheus exposition format.
func Handler() http.Handler { return promhttp.Handler() }
