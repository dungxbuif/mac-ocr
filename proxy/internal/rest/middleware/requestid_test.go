package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestIDRejectsUnsafeClientValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID())
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "unsafe request id with spaces")
	router.ServeHTTP(w, req)

	got := w.Header().Get("X-Request-Id")
	if got == "" || got == "unsafe request id with spaces" || !safeRequestID.MatchString(got) {
		t.Fatalf("unsafe request ID was not replaced: %q", got)
	}
}

func TestRequestIDPreservesSafeClientValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID())
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "trace_1234-safe")
	router.ServeHTTP(w, req)

	if got := w.Header().Get("X-Request-Id"); got != "trace_1234-safe" {
		t.Fatalf("safe request ID changed: %q", got)
	}
}
