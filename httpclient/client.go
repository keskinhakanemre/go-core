// Package httpclient provides an instrumented, resilient HTTP client for
// calling downstream services.
//
// Layers (outermost first):
//
//	circuit breaker -> retry with jittered backoff -> per attempt timeout -> otelhttp transport -> pooled http.Transport
//
// Create one Client per downstream service so that the circuit breaker state is
// shared by every caller of that service. Wrap it in a small typed adapter in
// the service's infra layer instead of using it directly from use cases.
package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/sony/gobreaker/v2"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"

	"github.com/hekanemre/go-core/apperror"
	"github.com/hekanemre/go-core/logger"
)

// HeaderIdempotencyKey marks non-idempotent requests (POST, PATCH) as safe to retry.
const HeaderIdempotencyKey = "Idempotency-Key"

type Config struct {
	Name         string        // downstream name used for breaker, metrics and logs, e.g. "inventory-service"
	Timeout      time.Duration // per attempt timeout (default 5s)
	RetryMax     int           // retries after the first attempt; 0 disables retries
	RetryWaitMin time.Duration // default 100ms
	RetryWaitMax time.Duration // default 2s

	BreakerMaxRequests  uint32        // requests allowed in half-open state (default 3)
	BreakerInterval     time.Duration // counter reset window in closed state (default 10s)
	BreakerTimeout      time.Duration // open -> half-open delay (default 10s)
	BreakerMinRequests  uint32        // minimum requests in window before tripping (default 5)
	BreakerFailureRatio float64       // failure ratio that trips the breaker (default 0.6)

	MaxIdleConnsPerHost int               // default 20
	Transport           http.RoundTripper // optional base transport (tests, custom TLS)
}

// ErrUpstream is returned (wrapped) when the downstream answers with a 5xx status.
var ErrUpstream = errors.New("upstream server error")

// ErrUnavailable is returned when the circuit breaker rejects the call.
var ErrUnavailable = apperror.Unavailable("upstream_unavailable", "upstream service is unavailable")

type Client struct {
	name    string
	retry   *retryablehttp.Client
	noRetry *retryablehttp.Client
	cb      *gobreaker.CircuitBreaker[*http.Response]
}

// New builds a client. Zero config values fall back to sane defaults.
func New(cfg Config) *Client {
	withDefaults(&cfg)

	base := cfg.Transport
	if base == nil {
		base = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: cfg.Timeout,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
			IdleConnTimeout:       90 * time.Second,
		}
	}
	hc := &http.Client{Transport: otelhttp.NewTransport(base), Timeout: cfg.Timeout}

	newRetry := func(max int) *retryablehttp.Client {
		rc := retryablehttp.NewClient()
		rc.HTTPClient = hc
		rc.RetryMax = max
		rc.RetryWaitMin = cfg.RetryWaitMin
		rc.RetryWaitMax = cfg.RetryWaitMax
		rc.Backoff = retryablehttp.LinearJitterBackoff
		rc.Logger = nil
		rc.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return retryablehttp.DefaultRetryPolicy(ctx, resp, err)
		}
		rc.RequestLogHook = func(_ retryablehttp.Logger, req *http.Request, attempt int) {
			if attempt > 0 {
				logger.FromContext(req.Context()).Debug("retrying downstream request",
					zap.String("client", cfg.Name), zap.String("method", req.Method), zap.Int("attempt", attempt))
			}
		}
		// After the last attempt return the response instead of an opaque error;
		// Do decides what counts as a failure.
		rc.ErrorHandler = retryablehttp.PassthroughErrorHandler
		return rc
	}

	cb := gobreaker.NewCircuitBreaker[*http.Response](gobreaker.Settings{ //nolint:bodyclose // type argument, bodies are closed by callers
		Name:        cfg.Name,
		MaxRequests: cfg.BreakerMaxRequests,
		Interval:    cfg.BreakerInterval,
		Timeout:     cfg.BreakerTimeout,
		ReadyToTrip: func(c gobreaker.Counts) bool {
			if c.Requests < cfg.BreakerMinRequests {
				return false
			}
			return float64(c.TotalFailures)/float64(c.Requests) >= cfg.BreakerFailureRatio
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			breakerState.WithLabelValues(name).Set(float64(to))
			zap.L().Warn("circuit breaker state changed",
				zap.String("client", name), zap.String("from", from.String()), zap.String("to", to.String()))
		},
		// Callers giving up must not open the breaker.
		IsSuccessful: func(err error) bool {
			return err == nil || errors.Is(err, context.Canceled)
		},
	})
	breakerState.WithLabelValues(cfg.Name).Set(float64(gobreaker.StateClosed))

	return &Client{name: cfg.Name, retry: newRetry(cfg.RetryMax), noRetry: newRetry(0), cb: cb}
}

// Do sends req through the circuit breaker and retry policy.
//
//   - 2xx-4xx responses are returned; the caller must close the body.
//   - 5xx responses (after retries) are returned as an error wrapping ErrUpstream,
//     with the body already drained and closed.
//   - When the breaker is open, ErrUnavailable (503) is returned immediately.
//
// POST and PATCH requests are only retried when they carry an Idempotency-Key header.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	rreq, err := retryablehttp.FromRequest(req)
	if err != nil {
		return nil, fmt.Errorf("httpclient %s: %w", c.name, err)
	}
	rc := c.retry
	if !isRetryable(req) {
		rc = c.noRetry
	}

	start := time.Now()
	resp, err := c.cb.Execute(func() (*http.Response, error) {
		resp, err := rc.Do(rreq)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= http.StatusInternalServerError {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
			return nil, fmt.Errorf("%w: %s responded %d", ErrUpstream, c.name, resp.StatusCode)
		}
		return resp, nil
	})
	observe(c.name, req.Method, resp, err, time.Since(start))

	if err != nil {
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return nil, ErrUnavailable.WithMessage(c.name + " is unavailable").Wrap(err)
		}
		return nil, fmt.Errorf("httpclient %s: %w", c.name, err)
	}
	return resp, nil
}

// State returns the circuit breaker state: "closed", "half-open" or "open".
func (c *Client) State() string { return c.cb.State().String() }

// Name returns the downstream name.
func (c *Client) Name() string { return c.name }

func isRetryable(req *http.Request) bool {
	switch req.Method {
	case http.MethodPost, http.MethodPatch:
		return req.Header.Get(HeaderIdempotencyKey) != ""
	default:
		return true
	}
}

func withDefaults(c *Config) {
	if c.Name == "" {
		c.Name = "default"
	}
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Second
	}
	if c.RetryWaitMin == 0 {
		c.RetryWaitMin = 100 * time.Millisecond
	}
	if c.RetryWaitMax == 0 {
		c.RetryWaitMax = 2 * time.Second
	}
	if c.BreakerMaxRequests == 0 {
		c.BreakerMaxRequests = 3
	}
	if c.BreakerInterval == 0 {
		c.BreakerInterval = 10 * time.Second
	}
	if c.BreakerTimeout == 0 {
		c.BreakerTimeout = 10 * time.Second
	}
	if c.BreakerMinRequests == 0 {
		c.BreakerMinRequests = 5
	}
	if c.BreakerFailureRatio == 0 {
		c.BreakerFailureRatio = 0.6
	}
	if c.MaxIdleConnsPerHost == 0 {
		c.MaxIdleConnsPerHost = 20
	}
}
