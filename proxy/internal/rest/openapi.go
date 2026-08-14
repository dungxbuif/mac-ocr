package rest

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

var (
	openAPIOnce sync.Once
	openAPIJSON []byte
)

// OpenAPIHandler serves the contract generated from the same Go schemas used by
// the REST and MCP handlers. It deliberately avoids a second hand-maintained
// YAML contract that can drift from runtime behavior.
func OpenAPIHandler() http.Handler {
	openAPIOnce.Do(func() {
		openAPIJSON, _ = json.MarshalIndent(buildOpenAPISpec(), "", "  ")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(openAPIJSON)
	})
}

func buildOpenAPISpec() gin.H {
	problem := refSchema("Problem")
	document := refSchema("Document")
	return gin.H{
		"openapi": "3.1.0",
		"info": gin.H{
			"title":       "OCR Platform API",
			"version":     "1.0.0",
			"description": "Asynchronous OCR API for applications and AI agents. The native macOS worker maps Apple Vision text observations to pages, blocks, confidence values, and normalized bounding boxes. Submit a public HTTPS URL, app-owned S3 source URL, or strict Base64 content; retrieve each document independently.",
		},
		"externalDocs": gin.H{"description": "OCR result and Apple Vision field mapping", "url": "/api/OCR_RESPONSE"},
		"servers":      []gin.H{{"url": "/", "description": "Current server"}},
		"tags": []gin.H{
			{"name": "Documents", "description": "Submit and retrieve independent OCR documents."},
			{"name": "Uploads", "description": "Create authenticated presigned upload URLs for large inputs."},
			{"name": "Notifications", "description": "Receive durable terminal document events."},
			{"name": "MCP", "description": "Model Context Protocol endpoint for agents."},
			{"name": "System"},
		},
		"paths": gin.H{
			"/healthz": gin.H{"get": operation("Health check", "System", nil, gin.H{"200": jsonResponse("Service is alive", gin.H{"type": "object"})})},
			"/readyz":  gin.H{"get": operation("Readiness check", "System", nil, gin.H{"200": jsonResponse("Dependencies are ready", gin.H{"type": "object"}), "503": problemResponse("A dependency is unavailable", problem)})},
			"/v1/ocr/capabilities": gin.H{"get": operation("Get supported OCR capabilities", "System", nil, gin.H{
				"200": jsonResponse("Limits and supported formats", refSchema("Capabilities")),
			})},
			"/v1/documents": gin.H{
				"post": secured(operationWithBody("Submit one document", "Documents", refSchema("DocumentSubmission"), gin.H{
					"202": jsonResponse("Document accepted", refSchema("SubmissionReceipt")),
					"400": problemResponse("Invalid input, including malformed or oversized Base64", problem),
					"401": problemResponse("Invalid or missing API key", problem),
					"413": problemResponse("HTTP envelope or URL content exceeds its safety limit", problem),
					"415": problemResponse("Only application/json and supported document bytes are accepted", problem),
				})),
			},
			"/v1/documents/{documentId}": gin.H{
				"get": secured(operation("Get document status and result", "Documents", []gin.H{documentIDParameter()}, gin.H{
					"200": jsonResponse("Current state; completed documents include result until expiry", document),
					"304": gin.H{"description": "Not modified"}, "401": problemResponse("Invalid or missing API key", problem),
					"404": problemResponse("Document not found", problem), "410": problemResponse("OCR result has expired", problem),
				})),
			},
			"/v1/batches": gin.H{"post": secured(operationWithBody("Submit 1-100 independent documents", "Documents", gin.H{
				"type": "array", "minItems": 1, "maxItems": 100, "items": refSchema("DocumentSubmission"),
			}, gin.H{
				"202": jsonResponse("Every item accepted as an independent document", refSchema("BatchReceipt")),
				"400": problemResponse("The complete batch is rejected if any item is invalid", problem),
				"401": problemResponse("Invalid or missing API key", problem), "413": problemResponse("Batch JSON envelope exceeds 128 MiB", problem),
				"415": problemResponse("Only application/json is accepted", problem),
			}))},
			"/v1/uploads/presign": gin.H{"post": secured(operationWithBody("Create a presigned upload URL", "Uploads", refSchema("PresignUploadRequest"), gin.H{
				"201": jsonResponse("Presigned upload URL and source URL", refSchema("PresignUploadResponse")),
				"400": problemResponse("Invalid filename or size", problem),
				"401": problemResponse("Invalid or missing API key", problem),
				"413": problemResponse("Requested upload size exceeds the configured file limit", problem),
				"415": problemResponse("Only application/json is accepted", problem),
			}))},
			"/v1/events": gin.H{"get": secured(operation("Stream SSE document events", "Notifications", []gin.H{{
				"name": "Last-Event-ID", "in": "header", "required": false, "schema": gin.H{"type": "string"}, "description": "Resume after a previously received durable event ID.",
			}}, gin.H{"200": eventStreamResponse("Terminal document events and heartbeats"), "401": problemResponse("Invalid or missing API key", problem)}))},
			"/mcp": gin.H{
				"post": secured(operationWithBodyMedia("Send an MCP JSON-RPC message", "MCP", "application/json", refSchema("MCPRequest"), gin.H{
					"200": jsonResponse("JSON-RPC response", refSchema("MCPResponse")), "202": gin.H{"description": "JSON-RPC notification accepted"},
					"400": jsonResponse("Invalid JSON-RPC request", refSchema("MCPResponse")), "401": problemResponse("Invalid or missing API key", problem),
					"413": problemResponse("MCP JSON envelope exceeds 128 MiB", problem), "415": jsonResponse("Only application/json is accepted", refSchema("MCPResponse")),
				})),
				"get": secured(operation("Open the MCP event stream", "MCP", []gin.H{{"name": "Last-Event-ID", "in": "header", "schema": gin.H{"type": "string"}}}, gin.H{
					"200": eventStreamResponse("MCP task-status and resource-updated notifications"), "401": problemResponse("Invalid or missing API key", problem),
				})),
			},
		},
		"components": gin.H{
			"securitySchemes": gin.H{"apiKeyAuth": gin.H{
				"type": "apiKey", "in": "header", "name": "Authorization",
				"description": "API key in the form: Bearer sk_ocr_...",
			}},
			"schemas": openAPISchemas(),
		},
		"x-mcp-protocol-version": mcpProtocolVersion,
		"x-mcp-tools":            mcpTools(),
	}
}

func openAPISchemas() gin.H {
	return gin.H{
		"Input":      inputSchema(),
		"OCROptions": ocrOptionsSchema(),
		"Notification": gin.H{
			"oneOf": []gin.H{
				{"type": "object", "additionalProperties": false, "required": []string{"type", "url", "secret"}, "properties": gin.H{
					"type": gin.H{"const": "webhook"}, "url": gin.H{"type": "string", "format": "uri", "pattern": `^https://`}, "secret": gin.H{"type": "string", "minLength": 16, "maxLength": 256, "writeOnly": true},
				}},
				{"type": "object", "additionalProperties": false, "required": []string{"type"}, "properties": gin.H{"type": gin.H{"const": "sse"}}},
			},
		},
		"DocumentSubmission": gin.H{"type": "object", "additionalProperties": false, "required": []string{"input"}, "properties": gin.H{
			"input": refSchema("Input"), "options": refSchema("OCROptions"), "notification": refSchema("Notification"),
		}},
		"SubmissionReceipt": gin.H{"type": "object", "required": []string{"documentId", "status", "createdAt"}, "properties": gin.H{
			"documentId": gin.H{"type": "string", "examples": []string{"doc_..."}}, "status": gin.H{"const": "queued"}, "createdAt": gin.H{"type": "string", "format": "date-time"}, "links": linksSchema(),
		}},
		"BatchReceipt": gin.H{"type": "object", "required": []string{"status", "summary", "items"}, "properties": gin.H{
			"status": gin.H{"const": "accepted"}, "summary": gin.H{"type": "object", "properties": gin.H{"total": integerSchema(), "accepted": integerSchema(), "rejected": integerSchema()}},
			"items": gin.H{"type": "array", "items": gin.H{"type": "object", "required": []string{"index", "documentId", "status"}, "properties": gin.H{"index": integerSchema(), "documentId": gin.H{"type": "string"}, "status": gin.H{"const": "queued"}, "links": linksSchema()}}},
		}},
		"PresignUploadRequest": gin.H{"type": "object", "additionalProperties": false, "required": []string{"filename", "sizeBytes"}, "properties": gin.H{
			"filename":    gin.H{"type": "string", "minLength": 1, "maxLength": 255},
			"sizeBytes":   gin.H{"type": "integer", "format": "int64", "minimum": 1, "description": "Declared upload size. The object is checked again with storage metadata at OCR submission time."},
			"contentType": gin.H{"type": "string", "description": "Optional upload Content-Type header to bind into the presigned PUT request."},
		}},
		"PresignUploadResponse": gin.H{"type": "object", "required": []string{"uploadUrl", "sourceUrl", "method", "expiresAt", "maxUploadBytes", "sizeBytes", "headers"}, "properties": gin.H{
			"uploadUrl":      gin.H{"type": "string", "format": "uri", "description": "Temporary URL for one PUT upload to object storage."},
			"sourceUrl":      gin.H{"type": "string", "description": "App-owned S3 source URL to pass as input.url in /v1/documents, /v1/batches, or MCP tools."},
			"method":         gin.H{"const": "PUT"},
			"expiresAt":      gin.H{"type": "string", "format": "date-time"},
			"maxUploadBytes": gin.H{"type": "integer", "format": "int64", "description": "Maximum accepted object size for this deployment."},
			"sizeBytes":      gin.H{"type": "integer", "format": "int64", "description": "Exact Content-Length bound into this presigned request."},
			"headers":        gin.H{"type": "object", "additionalProperties": gin.H{"type": "string"}, "description": "Headers the client must copy to the PUT request, including the signed Content-Length and optional Content-Type."},
		}},
		"Document": gin.H{"type": "object", "required": []string{"documentId", "status", "createdAt", "updatedAt"}, "properties": gin.H{
			"documentId": gin.H{"type": "string"}, "status": statusSchema(), "inputContentType": gin.H{"type": "string", "description": "Media type detected from file bytes, not trusted from the filename or request header."}, "inputSizeBytes": gin.H{"type": "integer", "format": "int64"},
			"createdAt": gin.H{"type": "string", "format": "date-time"}, "updatedAt": gin.H{"type": "string", "format": "date-time"},
			"result": refSchema("OCRResult"), "resultExpiresAt": gin.H{"type": []string{"string", "null"}, "format": "date-time", "description": "Redis result expiration. Persist required output before this time."},
			"resultExpired": gin.H{"type": "boolean"}, "errorDetail": gin.H{"type": "string"}, "links": linksSchema(),
		}},
		"Capabilities": gin.H{
			"type": "object",
			"required": []string{
				"engine", "capabilityVersion", "defaultProfile", "supportedLevels",
				"supportedRevisions", "supportedLanguages", "limits",
			},
			"properties": gin.H{
				"engine":            gin.H{"const": "OCR"},
				"capabilityVersion": gin.H{"type": "string", "examples": []string{"ocr-v1.0"}},
				"defaultProfile":    refSchema("OCROptions"),
				"supportedLevels": gin.H{
					"type": "array", "items": gin.H{"type": "string", "enum": []string{"accurate", "fast"}},
				},
				"supportedRevisions": gin.H{"type": "array", "items": gin.H{"type": "integer", "minimum": 1}},
				"supportedLanguages": gin.H{"type": "array", "items": gin.H{"type": "string"}},
				"limits": gin.H{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"maxBase64Bytes", "maxBatchItems", "maxLanguagesPerDoc", "maxCustomWords"},
					"properties": gin.H{
						"maxBase64Bytes":     gin.H{"const": 26214400},
						"maxBatchItems":      gin.H{"const": 100},
						"maxLanguagesPerDoc": gin.H{"const": 10},
						"maxCustomWords":     gin.H{"const": 100},
					},
				},
			},
		},
		"OCRBlock": gin.H{"type": "object", "required": []string{"text", "confidence"}, "description": "One Apple Vision recognized-text observation using only its highest-ranked candidate.", "properties": gin.H{
			"text":       gin.H{"type": "string", "description": "String from topCandidates(1).first."},
			"confidence": gin.H{"type": "number", "minimum": 0, "maximum": 1, "description": "Apple Vision normalized confidence for the selected candidate; 1 is highest. Do not assume it is a calibrated probability."},
			"bbox":       gin.H{"type": "array", "minItems": 4, "maxItems": 4, "items": gin.H{"type": "number"}, "description": "Normalized [x, y, width, height] in Apple Vision coordinates, whose origin is the lower-left of the page/image."},
		}},
		"OCRPage": gin.H{"type": "object", "required": []string{"pageNumber", "text"}, "properties": gin.H{
			"pageNumber": gin.H{"type": "integer", "minimum": 1, "description": "One-based source page number."},
			"text":       gin.H{"type": "string", "description": "Top candidate strings joined with newline characters in Apple Vision observation order."},
			"blocks":     gin.H{"type": "array", "items": refSchema("OCRBlock"), "description": "Recognized observations; blocks are not guaranteed to be words, paragraphs, table cells, or semantic lines."},
		}},
		"OCRResult": gin.H{"type": "object", "required": []string{"text", "pageCount"}, "properties": gin.H{
			"text":      gin.H{"type": "string", "description": "Whole-document plain text. Non-empty PDF page texts are joined with two newlines, --- PAGE BREAK ---, and two newlines."},
			"pageCount": gin.H{"type": "integer", "minimum": 0, "description": "Source PDF page count; supported standalone images have one page."},
			"pages":     gin.H{"type": "array", "items": refSchema("OCRPage"), "description": "Page-level output in source-page order. Consumers should tolerate omission for stored results without page details."},
		}},
		"Problem": gin.H{"type": "object", "required": []string{"type", "title", "status", "code"}, "properties": gin.H{
			"type": gin.H{"type": "string", "format": "uri-reference"}, "title": gin.H{"type": "string"}, "status": gin.H{"type": "integer"}, "code": gin.H{"type": "string"}, "detail": gin.H{"type": "string"}, "requestId": gin.H{"type": "string"}, "limits": gin.H{"type": "object"}, "links": linksSchema(),
		}},
		"MCPRequest":  gin.H{"type": "object", "required": []string{"jsonrpc", "method"}, "properties": gin.H{"jsonrpc": gin.H{"const": "2.0"}, "id": gin.H{}, "method": gin.H{"type": "string"}, "params": gin.H{"type": "object"}}},
		"MCPResponse": gin.H{"type": "object", "required": []string{"jsonrpc"}, "properties": gin.H{"jsonrpc": gin.H{"const": "2.0"}, "id": gin.H{}, "result": gin.H{}, "error": gin.H{"type": "object"}}},
	}
}

func operation(summary, tag string, parameters []gin.H, responses gin.H) gin.H {
	op := gin.H{"summary": summary, "tags": []string{tag}, "responses": responses}
	if len(parameters) > 0 {
		op["parameters"] = parameters
	}
	return op
}

func operationWithBody(summary, tag string, schema gin.H, responses gin.H) gin.H {
	return operationWithBodyMedia(summary, tag, "application/json", schema, responses)
}

func operationWithBodyMedia(summary, tag, mediaType string, schema gin.H, responses gin.H) gin.H {
	op := operation(summary, tag, nil, responses)
	op["requestBody"] = gin.H{"required": true, "content": gin.H{mediaType: gin.H{"schema": schema}}}
	return op
}

func secured(op gin.H) gin.H {
	op["security"] = []gin.H{{"apiKeyAuth": []string{}}}
	return op
}

func jsonResponse(description string, schema gin.H) gin.H {
	return gin.H{"description": description, "content": gin.H{"application/json": gin.H{"schema": schema}}}
}

func problemResponse(description string, schema gin.H) gin.H {
	return gin.H{"description": description, "content": gin.H{"application/problem+json": gin.H{"schema": schema}}}
}

func eventStreamResponse(description string) gin.H {
	return gin.H{"description": description, "content": gin.H{"text/event-stream": gin.H{"schema": gin.H{"type": "string"}}}}
}

func refSchema(name string) gin.H { return gin.H{"$ref": "#/components/schemas/" + name} }
func integerSchema() gin.H        { return gin.H{"type": "integer", "minimum": 0} }
func statusSchema() gin.H {
	return gin.H{"type": "string", "enum": []string{"queued", "processing", "completed", "failed", "cancelled"}}
}
func linksSchema() gin.H {
	return gin.H{
		"type": "array",
		"items": gin.H{
			"type":     "object",
			"required": []string{"rel", "href"},
			"properties": gin.H{
				"rel":    gin.H{"type": "string"},
				"href":   gin.H{"type": "string", "format": "uri-reference"},
				"method": gin.H{"type": "string"},
			},
		},
	}
}
func documentIDParameter() gin.H {
	return gin.H{"name": "documentId", "in": "path", "required": true, "schema": gin.H{"type": "string"}}
}
