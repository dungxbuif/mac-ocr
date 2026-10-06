package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesSPAShellForStableRoutes(t *testing.T) {
	for _, path := range []string{"/", "/users", "/workers", "/api-keys", "/portal"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s: status=%d", path, response.Code)
		}
		if !strings.Contains(response.Body.String(), `<div id="root"></div>`) {
			t.Fatalf("GET %s did not serve the SPA shell", path)
		}
	}
}
