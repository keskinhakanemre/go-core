package httpserver

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/keskinhakanemre/go-core/apperror"
	"github.com/keskinhakanemre/go-core/logger"
)

// ErrorBody is the error object of every error response:
//
//	{"error": {"code": "...", "message": "...", "details": ..., "trace_id": "...", "request_id": "..."}}
type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Details   any    `json:"details,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// ErrorResponse is the envelope of error responses.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorHandler renders err as an ErrorResponse and logs it. 5xx responses only
// expose a generic message; the cause is logged at error level. 4xx are logged
// at debug level (the access log already records them).
func ErrorHandler(c *fiber.Ctx, err error) error {
	ctx := c.UserContext()
	body, status := toBody(err)
	body.RequestID = RequestID(c)

	span := trace.SpanFromContext(ctx)
	if sc := span.SpanContext(); sc.IsValid() {
		body.TraceID = sc.TraceID().String()
	}

	log := logger.FromContext(ctx).With(
		zap.String("method", c.Method()),
		zap.String("path", c.Path()),
		zap.String("route", c.Route().Path),
		zap.Int("status", status),
		zap.String("code", body.Code),
		zap.Error(err),
	)
	if status >= http.StatusInternalServerError {
		span.RecordError(err)
		span.SetStatus(codes.Error, body.Code)
		log.Error("request failed")
	} else {
		log.Debug("request rejected")
	}

	return c.Status(status).JSON(ErrorResponse{Error: body})
}

func toBody(err error) (ErrorBody, int) {
	var fe *fiber.Error
	if errors.As(err, &fe) {
		msg := fe.Message
		if fe.Code >= http.StatusInternalServerError {
			msg = http.StatusText(fe.Code)
		}
		return ErrorBody{Code: statusCode(fe.Code), Message: msg}, fe.Code
	}
	ae := apperror.From(err)
	msg := ae.Message
	if ae.Kind == apperror.KindInternal {
		// Never leak internals, even if someone built an internal error with a custom message.
		msg = "internal server error"
	}
	return ErrorBody{Code: ae.Code, Message: msg, Details: ae.Details}, ae.HTTPStatus()
}

// statusCode turns an HTTP status into a snake_case code, e.g. 405 -> "method_not_allowed".
func statusCode(status int) string {
	text := http.StatusText(status)
	if text == "" {
		return "http_error"
	}
	return strings.ToLower(strings.NewReplacer(" ", "_", "-", "_", "'", "").Replace(text))
}
