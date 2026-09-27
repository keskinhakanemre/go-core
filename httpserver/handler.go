package httpserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/keskinhakanemre/go-core/apperror"
	"github.com/keskinhakanemre/go-core/validation"
)

// Handler is a transport independent use case. Implementations must not
// import Fiber; they receive a fully parsed and validated request.
type Handler[Req any, Res any] interface {
	Handle(ctx context.Context, req *Req) (*Res, error)
}

// HandlerFunc adapts a plain function to Handler.
type HandlerFunc[Req any, Res any] func(ctx context.Context, req *Req) (*Res, error)

func (f HandlerFunc[Req, Res]) Handle(ctx context.Context, req *Req) (*Res, error) {
	return f(ctx, req)
}

type handleOptions struct {
	status     int
	timeout    time.Duration
	noValidate bool

	// Documentation only; used when the route is registered through an API.
	summary     string
	description string
	tags        []string
	errors      []int
	operationID string
	deprecated  bool
	hidden      bool
}

func newHandleOptions(opts []HandleOption) handleOptions {
	o := handleOptions{status: http.StatusOK}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// HandleOption customises a single route.
type HandleOption func(*handleOptions)

// WithStatus sets the success status code (e.g. 201 Created, 204 No Content).
func WithStatus(code int) HandleOption { return func(o *handleOptions) { o.status = code } }

// WithTimeout overrides the server wide request timeout for this route. A
// negative value disables the timeout.
func WithTimeout(d time.Duration) HandleOption { return func(o *handleOptions) { o.timeout = d } }

// WithoutValidation skips `validate` tag validation for this route.
func WithoutValidation() HandleOption { return func(o *handleOptions) { o.noValidate = true } }

// ErrTimeout is returned when the request context deadline is exceeded.
var ErrTimeout = apperror.Timeout("timeout", "request timed out")

// Handle adapts h to a fiber.Handler.
//
// The request struct is populated from the JSON body (`json` tags), path
// parameters (`params`), query string (`query`) and headers (`reqHeader`),
// then validated with `validate` tags. A nil response (or 204 status) sends
// an empty body.
func Handle[Req any, Res any](h Handler[Req, Res], opts ...HandleOption) fiber.Handler {
	o := newHandleOptions(opts)

	return func(c *fiber.Ctx) error {
		var req Req
		if err := Bind(c, &req); err != nil {
			return err
		}
		if !o.noValidate {
			if err := validation.Struct(&req); err != nil {
				return err
			}
		}

		ctx := c.UserContext()
		timeout := o.timeout
		if timeout == 0 {
			timeout, _ = c.Locals(localsRequestTimeout).(time.Duration)
		}
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		res, err := h.Handle(ctx, &req)
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && apperror.KindOf(err) == apperror.KindInternal {
				return ErrTimeout.Wrap(err)
			}
			return err
		}
		if res == nil || o.status == http.StatusNoContent {
			return c.SendStatus(o.status)
		}
		return c.Status(o.status).JSON(res)
	}
}

// HandleFunc is Handle for plain functions; type parameters are inferred:
//
//	app.Get("/ping", httpserver.HandleFunc(func(ctx context.Context, _ *struct{}) (*Pong, error) { ... }))
func HandleFunc[Req any, Res any](fn func(ctx context.Context, req *Req) (*Res, error), opts ...HandleOption) fiber.Handler {
	return Handle[Req, Res](HandlerFunc[Req, Res](fn), opts...)
}

// Bind populates req from body, path params, query and headers.
func Bind(c *fiber.Ctx, req any) error {
	if len(c.Body()) > 0 {
		if err := c.BodyParser(req); err != nil {
			return apperror.BadRequest("invalid_body", "request body could not be parsed").Wrap(err)
		}
	}
	if err := c.ParamsParser(req); err != nil {
		return apperror.BadRequest("invalid_params", "invalid path parameters").Wrap(err)
	}
	if err := c.QueryParser(req); err != nil {
		return apperror.BadRequest("invalid_query", "invalid query parameters").Wrap(err)
	}
	if err := c.ReqHeaderParser(req); err != nil {
		return apperror.BadRequest("invalid_headers", "invalid headers").Wrap(err)
	}
	return nil
}
