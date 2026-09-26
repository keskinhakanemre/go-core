package apperror_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/hekanemre/go-core/apperror"
)

var errNotFound = apperror.NotFound("thing_not_found", "thing not found")

func TestIsMatchesByCodeAcrossCopies(t *testing.T) {
	cause := errors.New("db: no rows")
	wrapped := fmt.Errorf("repo: %w", errNotFound.Wrap(cause))

	if !errors.Is(wrapped, errNotFound) {
		t.Fatal("expected errors.Is to match sentinel through Wrap and fmt.Errorf")
	}
	if !errors.Is(wrapped, cause) {
		t.Fatal("expected errors.Is to reach the internal cause")
	}
	if errors.Is(wrapped, apperror.NotFound("other", "other")) {
		t.Fatal("different codes must not match")
	}
	if errNotFound.Err != nil {
		t.Fatal("Wrap must not mutate the sentinel")
	}
}

func TestEmptyCodeNeverMatches(t *testing.T) {
	a := apperror.New(apperror.KindConflict, "", "a")
	b := apperror.New(apperror.KindConflict, "", "b")
	if errors.Is(a, b) {
		t.Fatal("errors without code must not be considered equal")
	}
}

func TestFrom(t *testing.T) {
	if apperror.From(nil) != nil {
		t.Fatal("From(nil) must be nil")
	}
	e := apperror.From(errors.New("boom"))
	if e.Kind != apperror.KindInternal || e.Message != "internal server error" {
		t.Fatalf("unexpected internal error: %+v", e)
	}
	if got := apperror.From(fmt.Errorf("x: %w", errNotFound)); got.Code != "thing_not_found" {
		t.Fatalf("expected typed error to be found in chain, got %+v", got)
	}
}

func TestHTTPStatus(t *testing.T) {
	cases := map[apperror.Kind]int{
		apperror.KindInternal:        http.StatusInternalServerError,
		apperror.KindBadRequest:      http.StatusBadRequest,
		apperror.KindValidation:      http.StatusUnprocessableEntity,
		apperror.KindUnauthorized:    http.StatusUnauthorized,
		apperror.KindForbidden:       http.StatusForbidden,
		apperror.KindNotFound:        http.StatusNotFound,
		apperror.KindConflict:        http.StatusConflict,
		apperror.KindTooManyRequests: http.StatusTooManyRequests,
		apperror.KindUnavailable:     http.StatusServiceUnavailable,
		apperror.KindTimeout:         http.StatusGatewayTimeout,
		apperror.Kind(999):           http.StatusInternalServerError,
	}
	for k, want := range cases {
		if got := apperror.HTTPStatus(k); got != want {
			t.Errorf("HTTPStatus(%s) = %d, want %d", k, got, want)
		}
	}
}

func TestErrorString(t *testing.T) {
	if got := errNotFound.Error(); got != "thing not found" {
		t.Fatalf("got %q", got)
	}
	if got := errNotFound.Wrap(errors.New("cause")).Error(); got != "thing not found: cause" {
		t.Fatalf("got %q", got)
	}
}
