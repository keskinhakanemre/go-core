package httpclient_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hekanemre/go-core/apperror"
	"github.com/hekanemre/go-core/httpclient"
)

func server(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func fastClient(name string, retries int) *httpclient.Client {
	return httpclient.New(httpclient.Config{
		Name: name, Timeout: time.Second, RetryMax: retries,
		RetryWaitMin: time.Millisecond, RetryWaitMax: 2 * time.Millisecond,
		BreakerMinRequests: 2, BreakerTimeout: time.Minute,
	})
}

func get(t *testing.T, c *httpclient.Client, url string) (*http.Response, error) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	return c.Do(req)
}

func TestSuccessAnd4xxArePassedThrough(t *testing.T) {
	srv, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	c := fastClient("pass", 2)

	resp, err := get(t, c, srv.URL+"/ok")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
	_ = resp.Body.Close()

	for range 5 {
		resp, err = get(t, c, srv.URL+"/missing")
		if err != nil || resp.StatusCode != 404 {
			t.Fatalf("resp=%v err=%v", resp, err)
		}
		_ = resp.Body.Close()
	}
	if c.State() != "closed" {
		t.Fatal("4xx responses must not open the breaker")
	}
}

func TestRetriesThen5xxIsError(t *testing.T) {
	srv, calls := server(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	c := fastClient("retry", 2)

	_, err := get(t, c, srv.URL)
	if !errors.Is(err, httpclient.ErrUpstream) {
		t.Fatalf("expected ErrUpstream, got %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("expected 1 attempt + 2 retries, got %d", got)
	}
}

func TestRetryRecovers(t *testing.T) {
	var n atomic.Int32
	srv, calls := server(t, func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	resp, err := get(t, fastClient("recover", 3), srv.URL)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
	_ = resp.Body.Close()
	if calls.Load() != 3 {
		t.Fatalf("expected 3 calls, got %d", calls.Load())
	}
}

func TestPostIsNotRetriedWithoutIdempotencyKey(t *testing.T) {
	srv, calls := server(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	c := fastClient("post", 3)

	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{}`))
	_, _ = c.Do(req)
	if got := calls.Load(); got != 1 {
		t.Fatalf("POST without idempotency key must not be retried, got %d calls", got)
	}

	calls.Store(0)
	req, _ = http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{}`))
	req.Header.Set(httpclient.HeaderIdempotencyKey, "k1")
	_, _ = c.Do(req)
	if got := calls.Load(); got != 4 {
		t.Fatalf("POST with idempotency key should be retried, got %d calls", got)
	}
}

func TestBreakerOpensAndFailsFast(t *testing.T) {
	srv, calls := server(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	c := fastClient("breaker", 0)

	for range 2 {
		_, _ = get(t, c, srv.URL)
	}
	if c.State() != "open" {
		t.Fatalf("expected breaker to be open, got %s", c.State())
	}
	before := calls.Load()
	_, err := get(t, c, srv.URL)
	if !errors.Is(err, httpclient.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	if apperror.From(err).HTTPStatus() != http.StatusServiceUnavailable {
		t.Fatal("open breaker must map to 503")
	}
	if calls.Load() != before {
		t.Fatal("open breaker must not hit the server")
	}
}

func TestCanceledContextDoesNotOpenBreaker(t *testing.T) {
	srv, _ := server(t, func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	c := fastClient("cancel", 0)
	for range 5 {
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(5 * time.Millisecond); cancel() }()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		if _, err := c.Do(req); err == nil {
			t.Fatal("expected error")
		}
	}
	if c.State() != "closed" {
		t.Fatalf("caller cancellations must not open the breaker, state=%s", c.State())
	}
}
