# 08 — HTTP Client ve Resilience (Retry + Circuit Breaker)

## Kaynak projede

Dış servis çağrısı (`GetProductHandler` → `http_server/random-error`) üç katmanla korunuyor:

```
handler → gobreaker.Execute → retryablehttp.Client.Do → otelhttp.Transport → http.Transport
```

1. **`http.Transport`** (main.go `httpc()`): dial 30s, TLS handshake 10s, response header 10s timeout.
2. **`otelhttp.NewTransport`**: giden isteklere trace header'ı ekler, client span açar.
3. **`retryablehttp`**: `LinearJitterBackoff`, 100ms–10s bekleme, context iptal edildiyse retry etmeyen `CheckRetry`. **Ancak `RetryMax = 0`** → pratikte retry kapalı.
4. **`gobreaker`**: 5s pencerede ≥3 istek ve ≥%60 hata → OPEN; 10s sonra HALF-OPEN, 3 deneme izni. State değişimleri loglanıyor.

**Eksikler:**
- Circuit breaker handler constructor'ında oluşturuluyor → her handler kendi breaker'ına sahip; aynı downstream'e giden iki handler durumu paylaşmıyor.
- Breaker sadece network hatalarını sayıyor; **5xx yanıtlar başarılı sayılıyor** (retryablehttp retry'ları tüketince son 5xx yanıtı hata döndürmeden verebilir).
- `http.Client` üzerinde toplam `Timeout` yok.
- retryablehttp varsayılan olarak kendi logger'ı ile stderr'e yazıyor (zap formatı dışında).
- `MaxIdleConnsPerHost` ayarlanmamış (varsayılan 2 → yüksek trafikte bağlantı churn'ü).

## Core tasarımı

Her **downstream servis başına bir `httpclient.Client`** oluşturulur (breaker da ona ait). Handler'lar bu client'ı interface üzerinden alır.

## `httpclient/client.go`

```go
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
	"github.com/sony/gobreaker"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"

	"github.com/keskinhakanemre/go-core/apperror"
)

type Config struct {
	Name         string        // breaker ve log adı, ör. "inventory-service"
	Timeout      time.Duration // tek denemenin toplam süresi (varsayılan 5s)
	RetryMax     int           // 0 = retry yok
	RetryWaitMin time.Duration
	RetryWaitMax time.Duration

	// Circuit breaker
	BreakerMaxRequests  uint32        // half-open'da izin verilen istek (varsayılan 3)
	BreakerInterval     time.Duration // closed'da sayaç sıfırlama penceresi (varsayılan 5s)
	BreakerTimeout      time.Duration // open → half-open bekleme (varsayılan 10s)
	BreakerMinRequests  uint32        // trip için min istek (varsayılan 3)
	BreakerFailureRatio float64       // trip için hata oranı (varsayılan 0.6)
}

type Client struct {
	name  string
	retry *retryablehttp.Client
	cb    *gobreaker.CircuitBreaker
}

var ErrUpstream = errors.New("upstream server error")

func New(cfg Config) *Client {
	withDefaults(&cfg)

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: cfg.Timeout,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
	}

	rc := retryablehttp.NewClient()
	rc.HTTPClient = &http.Client{Transport: otelhttp.NewTransport(transport), Timeout: cfg.Timeout}
	rc.RetryMax = cfg.RetryMax
	rc.RetryWaitMin = cfg.RetryWaitMin
	rc.RetryWaitMax = cfg.RetryWaitMax
	rc.Backoff = retryablehttp.LinearJitterBackoff
	rc.Logger = nil // kendi loglamamızı yapıyoruz
	rc.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return retryablehttp.DefaultRetryPolicy(ctx, resp, err)
	}
	// Retry'lar bitince son yanıtı hata yerine yanıt olarak dön (breaker'da biz karar verelim)
	rc.ErrorHandler = retryablehttp.PassthroughErrorHandler

	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
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
			zap.L().Warn("circuit breaker state changed",
				zap.String("name", name), zap.String("from", from.String()), zap.String("to", to.String()))
		},
		// Client hatası (4xx) ve context iptali breaker'ı tetiklememeli
		IsSuccessful: func(err error) bool {
			return err == nil || errors.Is(err, context.Canceled)
		},
	})

	return &Client{name: cfg.Name, retry: rc, cb: cb}
}

// Do isteği breaker + retry ile gönderir. 5xx yanıtlar hata olarak döner (body kapatılmış olarak).
// 2xx-4xx yanıtlar çağırana döner; body'yi kapatmak çağıranın sorumluluğundadır.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	rreq, err := retryablehttp.FromRequest(req)
	if err != nil {
		return nil, err
	}

	out, err := c.cb.Execute(func() (any, error) {
		resp, err := c.retry.Do(rreq)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 500 {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("%w: %s status %d", ErrUpstream, c.name, resp.StatusCode)
		}
		return resp, nil
	})
	if err != nil {
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return nil, apperror.Unavailable("upstream_unavailable", c.name+" is unavailable").Wrap(err)
		}
		return nil, err
	}
	return out.(*http.Response), nil
}

func withDefaults(c *Config) {
	if c.Timeout == 0 { c.Timeout = 5 * time.Second }
	if c.RetryWaitMin == 0 { c.RetryWaitMin = 100 * time.Millisecond }
	if c.RetryWaitMax == 0 { c.RetryWaitMax = 2 * time.Second }
	if c.BreakerMaxRequests == 0 { c.BreakerMaxRequests = 3 }
	if c.BreakerInterval == 0 { c.BreakerInterval = 5 * time.Second }
	if c.BreakerTimeout == 0 { c.BreakerTimeout = 10 * time.Second }
	if c.BreakerMinRequests == 0 { c.BreakerMinRequests = 3 }
	if c.BreakerFailureRatio == 0 { c.BreakerFailureRatio = 0.6 }
}
```

> `gobreaker.Settings.IsSuccessful` alanı gobreaker v1'de mevcuttur. `gobreaker/v2` generic API sunar (`CircuitBreaker[T]`); yeni kurulumda v2 tercih edilebilir, o zaman `any` cast'ı da ortadan kalkar.

## Downstream client'ı tipli sarmalamak (servis tarafında)

Handler'ın ham HTTP ile uğraşmaması için her downstream için küçük bir adapter yaz:

```go
// internal/infra/inventory/client.go
type Client struct {
	baseURL string
	http    *httpclient.Client
}

func (c *Client) GetStock(ctx context.Context, productID string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/stock/"+url.PathEscape(productID), nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, domain.ErrProductNotFound
	}
	var body struct{ Stock int `json:"stock"` }
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("inventory decode: %w", err)
	}
	return body.Stock, nil
}
```

`app/product` ise sadece interface görür:

```go
type StockProvider interface {
	GetStock(ctx context.Context, productID string) (int, error)
}
```

## Resilience kuralları

- **Retry sadece idempotent isteklerde** (GET, PUT, DELETE veya idempotency-key'li POST). Non-idempotent POST için `RetryMax: 0`.
- Retry toplam süresi, gelen isteğin timeout'undan (`http.request_timeout`) kısa olmalı. Context deadline zaten retry döngüsünü keser.
- Sıralama: **timeout < retry < circuit breaker**. Breaker en dışta, böylece retry'lar tükendikten sonraki gerçek başarısızlık sayılır.
- Breaker OPEN → `503 upstream_unavailable` döner (hızlı fail, cascade failure önlenir).
- Mümkünse fallback (cache'ten eski veri, varsayılan değer) değerlendir.
