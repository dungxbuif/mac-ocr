package rest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAPIHandlerBuildsRuntimeContract(t *testing.T) {
	w := httptest.NewRecorder()
	OpenAPIHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("unexpected response: status=%d content-type=%q", w.Code, w.Header().Get("Content-Type"))
	}
	var spec struct {
		OpenAPI    string                     `json:"openapi"`
		Paths      map[string]json.RawMessage `json:"paths"`
		Tools      []json.RawMessage          `json:"x-mcp-tools"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatalf("invalid OpenAPI JSON: %v", err)
	}
	if spec.OpenAPI != "3.1.0" || len(spec.Tools) != 4 {
		t.Fatalf("runtime schemas are missing: %+v", spec)
	}
	for _, path := range []string{"/v1/documents", "/v1/documents/{documentId}", "/v1/batches", "/v1/events", "/mcp"} {
		if _, ok := spec.Paths[path]; !ok {
			t.Fatalf("missing path %s", path)
		}
	}
	if _, exists := spec.Paths["/v1/batches/{batchId}"]; exists {
		t.Fatal("public batch lookup must not be advertised")
	}
	var capabilities struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(spec.Components.Schemas["Capabilities"], &capabilities); err != nil {
		t.Fatalf("invalid capabilities schema: %v", err)
	}
	for _, property := range []string{"engine", "capabilityVersion", "defaultProfile", "supportedLevels", "supportedRevisions", "supportedLanguages", "limits"} {
		if _, ok := capabilities.Properties[property]; !ok {
			t.Fatalf("capabilities schema is missing runtime property %s", property)
		}
	}
	if _, stale := capabilities.Properties["inputMethods"]; stale {
		t.Fatal("capabilities schema contains a property not returned by runtime")
	}
}
