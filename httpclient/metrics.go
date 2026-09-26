package httpclient

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sony/gobreaker/v2"
)

var (
	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_client_request_duration_seconds",
		Help:    "Duration of outgoing HTTP requests including retries, in seconds.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"client", "method", "status"})

	breakerState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "http_client_circuit_breaker_state",
		Help: "Circuit breaker state per downstream: 0 closed, 1 half-open, 2 open.",
	}, []string{"client"})
)

func observe(client, method string, resp *http.Response, err error, d time.Duration) {
	status := "error"
	switch {
	case err == nil && resp != nil:
		status = strconv.Itoa(resp.StatusCode)
	case errors.Is(err, ErrUpstream):
		status = "5xx"
	case errors.Is(err, gobreaker.ErrOpenState), errors.Is(err, gobreaker.ErrTooManyRequests):
		status = "circuit_open"
	}
	requestDuration.WithLabelValues(client, method, status).Observe(d.Seconds())
}
