package docs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSwaggerHandler(t *testing.T) {
	tests := []struct {
		name        string
		handler     http.Handler
		contentType string
		contains    string
	}{
		{name: "swagger", handler: SwaggerHandler("https://api.test"), contentType: "text/html", contains: "/api/v1/openapi.json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
			if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), tc.contentType) || !strings.Contains(w.Body.String(), tc.contains) {
				t.Fatalf("unexpected response: status=%d content-type=%q", w.Code, w.Header().Get("Content-Type"))
			}
		})
	}
}
