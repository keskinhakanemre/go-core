// Package apperror provides typed application errors that are transport
// agnostic. Domain and application code only decide the Kind of an error;
// transports (HTTP, gRPC, queue consumers) map the Kind to their own status.
//
// Typical usage is to declare sentinel errors in the domain layer:
//
//	var ErrProductNotFound = apperror.NotFound("product_not_found", "product not found")
//
// and compare them with errors.Is, which matches on Code:
//
//	if errors.Is(err, domain.ErrProductNotFound) { ... }
package apperror

import (
	"errors"
	"net/http"
)

// Kind classifies an error. The zero value is KindInternal.
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

var kindNames = [...]string{
	KindInternal:        "internal",
	KindBadRequest:      "bad_request",
	KindValidation:      "validation",
	KindUnauthorized:    "unauthorized",
	KindForbidden:       "forbidden",
	KindNotFound:        "not_found",
	KindConflict:        "conflict",
	KindTooManyRequests: "too_many_requests",
	KindUnavailable:     "unavailable",
	KindTimeout:         "timeout",
}

func (k Kind) String() string {
	if k >= 0 && int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "unknown"
}

// Error is the application wide error type.
type Error struct {
	Kind    Kind
	Code    string // machine readable, e.g. "product_not_found"
	Message string // safe to show to clients
	Details any    // optional structured details, e.g. validation field errors
	Err     error  // internal cause; logged, never sent to clients
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// Is reports whether target is an *Error with the same non-empty Code. This
// makes errors.Is work with sentinel errors even after Wrap/WithDetails copies.
func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return e.Code != "" && e.Code == t.Code
}

// Wrap returns a copy of e with err attached as the internal cause.
// The receiver (usually a package level sentinel) is never mutated.
func (e *Error) Wrap(err error) *Error {
	c := *e
	c.Err = err
	return &c
}

// WithDetails returns a copy of e with the given client visible details.
func (e *Error) WithDetails(d any) *Error {
	c := *e
	c.Details = d
	return &c
}

// WithMessage returns a copy of e with a different client visible message.
func (e *Error) WithMessage(msg string) *Error {
	c := *e
	c.Message = msg
	return &c
}

// HTTPStatus is a shortcut for HTTPStatus(e.Kind).
func (e *Error) HTTPStatus() int { return HTTPStatus(e.Kind) }

func New(kind Kind, code, msg string) *Error { return &Error{Kind: kind, Code: code, Message: msg} }

func BadRequest(code, msg string) *Error      { return New(KindBadRequest, code, msg) }
func Validation(code, msg string) *Error      { return New(KindValidation, code, msg) }
func Unauthorized(code, msg string) *Error    { return New(KindUnauthorized, code, msg) }
func Forbidden(code, msg string) *Error       { return New(KindForbidden, code, msg) }
func NotFound(code, msg string) *Error        { return New(KindNotFound, code, msg) }
func Conflict(code, msg string) *Error        { return New(KindConflict, code, msg) }
func TooManyRequests(code, msg string) *Error { return New(KindTooManyRequests, code, msg) }
func Unavailable(code, msg string) *Error     { return New(KindUnavailable, code, msg) }
func Timeout(code, msg string) *Error         { return New(KindTimeout, code, msg) }

// Internal wraps an unexpected error. Clients only see a generic message.
func Internal(err error) *Error {
	return &Error{Kind: KindInternal, Code: "internal_error", Message: "internal server error", Err: err}
}

// From converts any error into an *Error. Unknown errors become Internal.
// From(nil) returns nil.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Internal(err)
}

// KindOf returns the Kind of err (KindInternal for unknown errors).
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindInternal
}

// HTTPStatus maps a Kind to an HTTP status code.
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
