package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

func Logger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		attrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"route", c.FullPath(),
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"response_bytes", c.Writer.Size(),
			"request_id", RequestIDFrom(c),
		}
		for _, field := range []struct {
			contextKey string
			logKey     string
		}{
			{contextKey: "audit.actor", logKey: "actor"},
			{contextKey: "audit.user_id", logKey: "user_id"},
			{contextKey: "audit.api_key_id", logKey: "api_key_id"},
		} {
			if value, ok := c.Get(field.contextKey); ok {
				attrs = append(attrs, field.logKey, value)
			}
		}

		level := slog.LevelInfo
		if c.Writer.Status() >= 500 {
			level = slog.LevelError
		} else if c.Writer.Status() >= 400 {
			level = slog.LevelWarn
		}
		logger.Log(context.Background(), level, "http_request", attrs...)
	}
}
