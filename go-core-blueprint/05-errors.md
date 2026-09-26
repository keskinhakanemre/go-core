# 05 — Hata Yönetimi (apperror)

## Kaynak projede

- Generic handler, handler'dan dönen **her hatayı 500** olarak döndürüyor.
- Couchbase repository "document not found" durumunda `errors.New("product not found")` dönüyor → client 404 yerine **500** alıyor.
- Hata mesajı (`err.Error()`) doğrudan client'a gidiyor → iç detaylar (DB hataları, host adresleri) sızabilir.

## Core tasarımı

Hataların bir **türü (Kind)** olur; HTTP katmanı türe bakarak status belirler. Domain ve app katmanı HTTP bilmez, sadece tür bilir.

## `apperror/apperror.go`

```go
package apperror

import (
	"errors"
	"net/http"
)

type Kind int

const (
	KindInternal Kind = iota
	KindBadRequest
	KindValidation
	KindUnauthorized
	KindForbidden
	KindNotFound
	KindConflict
	KindTooManyRequests
	KindUnavailable
	KindTimeout
)

// Error uygulama genelinde kullanılan tipli hata.
type Error struct {
	Kind    Kind
	Code    string // makine tarafından okunabilir: "product_not_found"
	Message string // client'a gösterilecek güvenli mesaj
	Details any    // ör. validation alan hataları
	Err     error  // iç sebep (client'a gösterilmez, loglanır)
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// Is: aynı Code'a sahip hatalar eşit sayılır → errors.Is(err, domain.ErrProductNotFound) çalışır.
func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return e.Code != "" && e.Code == t.Code
}

// Wrap: sentinel hatayı iç sebep ile zenginleştirip yeni kopya döner.
func (e *Error) Wrap(err error) *Error {
	c := *e
	c.Err = err
	return &c
}

func (e *Error) WithDetails(d any) *Error {
	c := *e
	c.Details = d
	return &c
}

func New(kind Kind, code, msg string) *Error { return &Error{Kind: kind, Code: code, Message: msg} }

func NotFound(code, msg string) *Error    { return New(KindNotFound, code, msg) }
func BadRequest(code, msg string) *Error  { return New(KindBadRequest, code, msg) }
func Conflict(code, msg string) *Error    { return New(KindConflict, code, msg) }
func Unauthorized(code, msg string) *Error { return New(KindUnauthorized, code, msg) }
func Forbidden(code, msg string) *Error   { return New(KindForbidden, code, msg) }
func Unavailable(code, msg string) *Error { return New(KindUnavailable, code, msg) }
func Internal(err error) *Error {
	return &Error{Kind: KindInternal, Code: "internal_error", Message: "internal server error", Err: err}
}

// From herhangi bir hatayı *Error'a çevirir (tanınmayanlar Internal olur).
func From(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Internal(err)
}

func HTTPStatus(k Kind) int {
	switch k {
	case KindBadRequest:
		return http.StatusBadRequest
	case KindValidation:
		return http.StatusUnprocessableEntity
	case KindUnauthorized:
		return http.StatusUnauthorized
	case KindForbidden:
		return http.StatusForbidden
	case KindNotFound:
		return http.StatusNotFound
	case KindConflict:
		return http.StatusConflict
	case KindTooManyRequests:
		return http.StatusTooManyRequests
	case KindUnavailable:
		return http.StatusServiceUnavailable
	case KindTimeout:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}
```

## Kullanım

**Domain'de sentinel hatalar:**

```go
// internal/domain/errors.go
package domain

import "github.com/keskinhakanemre/go-core/apperror"

var (
	ErrProductNotFound      = apperror.NotFound("product_not_found", "product not found")
	ErrProductAlreadyExists = apperror.Conflict("product_already_exists", "product already exists")
)
```

**Infra'da DB hatasını domain hatasına çevir:**

```go
if errors.Is(err, gocb.ErrDocumentNotFound) {
	return nil, domain.ErrProductNotFound
}
if err != nil {
	return nil, fmt.Errorf("couchbase get product %s: %w", id, err) // → 500, detay sadece logda
}
```

**App katmanında karar ver:**

```go
p, err := h.repo.GetProduct(ctx, req.ID)
if errors.Is(err, domain.ErrProductNotFound) { /* gerekirse özel davranış */ }
```

## Standart hata response formatı

Tüm servislerde aynı JSON:

```json
{
  "error": {
    "code": "product_not_found",
    "message": "product not found",
    "details": null,
    "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736"
  }
}
```

Bu formatı üreten error handler [06-http-server.md](06-http-server.md)'de.

## Kurallar

- 5xx'lerde client'a sadece `"internal server error"` gider, gerçek sebep loglanır.
- 4xx'lerde `Message` client'a gider → güvenli, kullanıcı dostu mesaj yaz.
- Hata sarmalarken `fmt.Errorf("...: %w", err)` kullan (`%v` değil), zincir bozulmasın.
