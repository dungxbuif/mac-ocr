package middleware

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

func Recovery(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("panic recovered", "value", rec, "request_id", RequestIDFrom(c))
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"code":   "INTERNAL_ERROR",
					"status": http.StatusInternalServerError,
					"title":  "Internal error",
				})
			}
		}()
		c.Next()
	}
}
