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
			Schemas         map[string]json.RawMessage `json:"schemas"`
			SecuritySchemes map[string]json.RawMessage `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatalf("invalid OpenAPI JSON: %v", err)
	}
	if spec.OpenAPI != "3.1.0" || len(spec.Tools) != 3 {
		t.Fatalf("runtime schemas are missing: %+v", spec)
	}
	for _, path := range []string{"/v1/documents", "/v1/documents/{documentId}", "/v1/batches", "/v1/uploads/presign", "/v1/events", "/mcp"} {
		if _, ok := spec.Paths[path]; !ok {
			t.Fatalf("missing path %s", path)
		}
	}
	if _, exists := spec.Paths["/v1/batches/{batchId}"]; exists {
		t.Fatal("public batch lookup must not be advertised")
	}
	var documentsPath map[string]json.RawMessage
	if err := json.Unmarshal(spec.Paths["/v1/documents"], &documentsPath); err != nil {
		t.Fatalf("invalid documents path: %v", err)
	}
	if _, exists := documentsPath["get"]; exists {
		t.Fatal("public document list must not be advertised")
	}
	var documentPath map[string]json.RawMessage
	if err := json.Unmarshal(spec.Paths["/v1/documents/{documentId}"], &documentPath); err != nil {
		t.Fatalf("invalid document path: %v", err)
	}
	if _, exists := documentPath["delete"]; exists {
		t.Fatal("public document deletion must not be advertised")
	}
	if _, ok := spec.Components.SecuritySchemes["apiKeyAuth"]; !ok {
		t.Fatal("apiKeyAuth security scheme is missing")
	}
	if _, stale := spec.Components.SecuritySchemes["bearerAuth"]; stale {
		t.Fatal("stale bearerAuth security scheme is still advertised")
	}
	for _, schemaName := range []string{"SubmissionReceipt", "Document", "Problem"} {
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(spec.Components.Schemas[schemaName], &schema); err != nil {
			t.Fatalf("invalid %s schema: %v", schemaName, err)
		}
		var links struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(schema.Properties["links"], &links); err != nil || links.Type != "array" {
			t.Fatalf("%s links must use the HATEOAS array contract", schemaName)
		}
		if _, stale := schema.Properties["_links"]; stale {
			t.Fatalf("%s still exposes legacy _links", schemaName)
		}
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
	for _, schema := range []string{"OCRResult", "OCRPage", "OCRBlock"} {
		if _, ok := spec.Components.Schemas[schema]; !ok {
			t.Fatalf("OCR response schema %s is missing", schema)
		}
	}
	var block struct {
		Properties map[string]struct {
			Description string `json:"description"`
			MinItems    int    `json:"minItems"`
			MaxItems    int    `json:"maxItems"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(spec.Components.Schemas["OCRBlock"], &block); err != nil {
		t.Fatalf("invalid OCRBlock schema: %v", err)
	}
	if block.Properties["bbox"].MinItems != 4 || block.Properties["bbox"].MaxItems != 4 {
		t.Fatal("OCR bbox must contain exactly [x, y, width, height]")
	}
	if block.Properties["confidence"].Description == "" || block.Properties["bbox"].Description == "" {
		t.Fatal("OCR confidence and bbox integration semantics must be documented")
	}
}
