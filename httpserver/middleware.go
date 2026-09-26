package httpserver

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"

	"github.com/hekanemre/go-core/logger"
	"github.com/hekanemre/go-core/metrics"
)

// RequestID returns the request id of the current request.
func RequestID(c *fiber.Ctx) string {
	return c.GetRespHeader(HeaderRequestID)
}

// requestLogger attaches request_id to the logger carried in the user context,
// so logger.FromContext(ctx) in handlers includes it.
func requestLogger(c *fiber.Ctx) error {
	if id := RequestID(c); id != "" {
		c.SetUserContext(logger.WithFields(c.UserContext(), zap.String("request_id", id)))
	}
	return c.Next()
}

// renderErrors converts errors returned by inner handlers into responses so
// outer middlewares observe the final status code.
func renderErrors(c *fiber.Ctx) error {
	err := c.Next()
	if err == nil {
		return nil
	}
	return ErrorHandler(c, err)
}

func accessLog(skip func(*fiber.Ctx) bool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if skip(c) {
			return c.Next()
		}
		start := time.Now()
		err := c.Next()

		status := c.Response().StatusCode()
		if err != nil {
			status = metrics.StatusFromError(err)
		}
		logger.FromContext(c.UserContext()).Info("http request",
			zap.String("method", c.Method()),
			zap.String("path", c.Path()),
			zap.String("route", c.Route().Path),
			zap.Int("status", status),
			zap.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
			zap.Int("bytes", len(c.Response().Body())),
			zap.String("ip", c.IP()),
			zap.String("user_agent", c.Get(fiber.HeaderUserAgent)),
		)
		return err
	}
}

func logPanic(c *fiber.Ctx, e any) {
	logger.FromContext(c.UserContext()).Error("panic recovered",
		zap.String("panic", fmt.Sprint(e)),
		zap.String("method", c.Method()),
		zap.String("path", c.Path()),
	)
}
