package rest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/notifications"
	"macocr/proxy/internal/usecase/document"
)

const mcpProtocolVersion = "2025-11-25"
const maxMCPRequestBytes = maxBatchJSONRequestBytes

type MCPHandler struct {
	docs          *document.Service
	notifications *notifications.Service
	allowedOrigin string
}

func NewMCPHandler(docs *document.Service, notifications *notifications.Service, publicAPIBaseURL string) *MCPHandler {
	u, _ := url.Parse(publicAPIBaseURL)
	return &MCPHandler{docs: docs, notifications: notifications, allowedOrigin: u.Scheme + "://" + u.Host}
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (h *MCPHandler) Post(c *gin.Context) {
	if !h.validOrigin(c) {
		c.Status(http.StatusForbidden)
		return
	}
	if c.ContentType() != "application/json" {
		c.JSON(http.StatusUnsupportedMediaType, mcpError(nil, -32600, "content type must be application/json"))
		return
	}
	if version := c.GetHeader("MCP-Protocol-Version"); version != "" && version != mcpProtocolVersion {
		c.JSON(http.StatusBadRequest, mcpError(nil, -32602, "unsupported MCP protocol version"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxMCPRequestBytes)
	var req mcpRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decodeOneJSON(decoder, &req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.JSON(http.StatusRequestEntityTooLarge, mcpError(req.ID, -32000, "MCP request exceeds 128 MiB"))
			return
		}
		c.JSON(http.StatusBadRequest, mcpError(req.ID, -32600, "invalid JSON-RPC request"))
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		c.JSON(http.StatusBadRequest, mcpError(req.ID, -32600, "invalid JSON-RPC request"))
		return
	}
	if req.ID == nil {
		c.Status(http.StatusAccepted)
		return
	}
	key, ok := apiKeyFrom(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, mcpError(req.ID, -32001, "authentication required"))
		return
	}
	result, rpcErr := h.dispatch(c, key.UserID, req)
	if rpcErr != nil {
		c.JSON(http.StatusOK, mcpError(req.ID, rpcErr.Code, rpcErr.Message))
		return
	}
	c.Header("MCP-Protocol-Version", mcpProtocolVersion)
	c.JSON(http.StatusOK, gin.H{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

func (h *MCPHandler) Get(c *gin.Context) {
	if !h.validOrigin(c) {
		c.Status(http.StatusForbidden)
		return
	}
	key, ok := apiKeyFrom(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache, no-store")
	c.Header("X-Accel-Buffering", "no")
	_, _ = c.Writer.WriteString("retry: 3000\n\n")
	flusher.Flush()
	cursor := c.GetHeader("Last-Event-ID")
	poll, heartbeat := time.NewTicker(time.Second), time.NewTicker(15*time.Second)
	defer poll.Stop()
	defer heartbeat.Stop()
	for {
		events, err := h.notifications.ListSSE(c.Request.Context(), key.UserID, cursor)
		if err != nil {
			return
		}
		for _, event := range events {
			var payload map[string]any
			_ = json.Unmarshal(event.Payload, &payload)
			status := mcpTaskStatus(domain.DocumentStatus(fmt.Sprint(payload["status"])))
			msg, _ := json.Marshal(gin.H{"jsonrpc": "2.0", "method": "notifications/tasks/status", "params": gin.H{"taskId": event.DocumentID, "status": status}})
			_, _ = fmt.Fprintf(c.Writer, "id: %s\ndata: %s\n\n", event.ID, msg)
			resource, _ := json.Marshal(gin.H{"jsonrpc": "2.0", "method": "notifications/resources/updated", "params": gin.H{"uri": "ocr://documents/" + event.DocumentID}})
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", resource)
			cursor = event.ID
		}
		if len(events) > 0 {
			flusher.Flush()
			continue
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = c.Writer.WriteString(": heartbeat\n\n")
			flusher.Flush()
		case <-poll.C:
		}
	}
}

type rpcFailure struct {
	Code    int
	Message string
}

func (h *MCPHandler) dispatch(c *gin.Context, userID int64, req mcpRequest) (any, *rpcFailure) {
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string          `json:"protocolVersion"`
			Capabilities    json.RawMessage `json:"capabilities"`
			ClientInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
			Meta json.RawMessage `json:"_meta,omitempty"`
		}
		if err := strictJSON(req.Params, &params); err != nil || params.ProtocolVersion == "" || params.ClientInfo.Name == "" || params.ClientInfo.Version == "" || !isJSONObject(params.Capabilities) {
			if err == nil {
				err = errors.New("initialize requires protocolVersion, object capabilities, and clientInfo name/version")
			}
			return nil, invalidParams(err)
		}
		return gin.H{"protocolVersion": mcpProtocolVersion, "capabilities": gin.H{
			"tools": gin.H{}, "resources": gin.H{"subscribe": true},
			"tasks": gin.H{"requests": gin.H{"tools": gin.H{"call": gin.H{}}}},
		}, "serverInfo": gin.H{"name": "ocr-platform", "version": "1.0.0"},
			"instructions": "Submit OCR documents as durable tasks. Poll tasks/get or keep GET /mcp open for task and resource notifications."}, nil
	case "ping":
		return gin.H{}, nil
	case "tools/list":
		return gin.H{"tools": mcpTools()}, nil
	case "tools/call":
		return h.callTool(c, userID, req.Params)
	case "resources/templates/list":
		return gin.H{"resourceTemplates": []gin.H{{"uriTemplate": "ocr://documents/{documentId}", "name": "ocr_document", "description": "OCR document status and completed result", "mimeType": "application/json"}}}, nil
	case "resources/read":
		return h.readResource(c, userID, req.Params)
	case "resources/subscribe", "resources/unsubscribe":
		return gin.H{}, nil
	case "tasks/get":
		return h.getTask(c, userID, req.Params)
	case "tasks/result":
		return h.taskResult(c, userID, req.Params)
	default:
		return nil, &rpcFailure{-32601, "method not found"}
	}
}

func mcpTools() []gin.H {
	input := inputSchema()
	item := gin.H{"type": "object", "additionalProperties": false, "required": []string{"input"}, "properties": gin.H{"input": input, "options": ocrOptionsSchema()}}
	return []gin.H{
		{"name": "submit_ocr_document", "description": "Submit one OCR document and return a durable document task.", "inputSchema": item, "execution": gin.H{"taskSupport": "optional"}},
		{"name": "submit_ocr_batch", "description": "Submit multiple independent OCR documents and return their document task IDs.", "inputSchema": gin.H{"type": "object", "additionalProperties": false, "required": []string{"items"}, "properties": gin.H{"items": gin.H{"type": "array", "minItems": 1, "maxItems": 100, "items": item}}}, "execution": gin.H{"taskSupport": "forbidden"}},
		{"name": "get_ocr_document", "description": "Read one OCR task status and completed result by document ID.", "inputSchema": gin.H{"type": "object", "additionalProperties": false, "required": []string{"documentId"}, "properties": gin.H{"documentId": gin.H{"type": "string"}}}},
	}
}

func inputSchema() gin.H {
	return gin.H{
		"type":                 "object",
		"additionalProperties": false,
		"properties": gin.H{
			"url":    gin.H{"type": "string", "format": "uri", "description": "Public HTTPS URL, or an app-owned s3:// sourceUrl returned by /v1/uploads/presign for large uploads."},
			"base64": gin.H{"type": "string", "contentEncoding": "base64", "description": "Strict standard Base64; maximum decoded size is 25 MiB."},
		},
		"oneOf": []gin.H{{"required": []string{"url"}}, {"required": []string{"base64"}}},
	}
}

func ocrOptionsSchema() gin.H {
	return gin.H{"type": "object", "additionalProperties": false, "properties": gin.H{"recognitionLevel": gin.H{"type": "string", "enum": []string{"fast", "accurate"}, "default": "accurate"}, "languages": gin.H{"type": "array", "items": gin.H{"type": "string"}, "default": []string{"vi-VN", "en-US"}}, "automaticallyDetectsLanguage": gin.H{"type": "boolean", "default": true}, "usesLanguageCorrection": gin.H{"type": "boolean", "default": true}, "customWords": gin.H{"type": "array", "items": gin.H{"type": "string"}, "default": []string{}}, "minimumTextHeight": gin.H{"type": "number", "minimum": 0, "maximum": 1, "default": 0}}}
}

type mcpToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Task      *struct {
		TTL int64 `json:"ttl,omitempty"`
	} `json:"task,omitempty"`
}
type mcpSubmit struct {
	Input   inputReq           `json:"input"`
	Options *domain.OCROptions `json:"options,omitempty"`
}

func (h *MCPHandler) callTool(c *gin.Context, userID int64, raw json.RawMessage) (any, *rpcFailure) {
	var call mcpToolCall
	if err := strictJSON(raw, &call); err != nil {
		return nil, invalidParams(err)
	}
	switch call.Name {
	case "submit_ocr_document":
		var args mcpSubmit
		if err := strictJSON(call.Arguments, &args); err != nil {
			return nil, invalidParams(err)
		}
		source, err := args.Input.source()
		if err != nil {
			return nil, invalidParams(err)
		}
		doc, err := h.docs.SubmitSingle(c.Request.Context(), userID, source, args.Options, &domain.NotificationConfig{Type: "sse"})
		if err != nil {
			return nil, toolFailure(err)
		}
		if call.Task != nil {
			return gin.H{"task": mcpTask(doc), "_meta": gin.H{"io.modelcontextprotocol/related-task": gin.H{"taskId": doc.ID}}}, nil
		}
		return toolJSON(publicMCPDocument(doc)), nil
	case "submit_ocr_batch":
		var args struct {
			Items []mcpSubmit `json:"items"`
		}
		if err := strictJSON(call.Arguments, &args); err != nil {
			return nil, invalidParams(err)
		}
		if len(args.Items) == 0 || len(args.Items) > 100 {
			return nil, invalidParams(errors.New("items must contain 1-100 documents"))
		}
		items := make([]document.BatchItemInput, len(args.Items))
		for i := range args.Items {
			source, err := args.Items[i].Input.source()
			if err != nil {
				return nil, invalidParams(fmt.Errorf("item %d: %w", i, err))
			}
			items[i] = document.BatchItemInput{Input: source, Options: args.Items[i].Options, Notification: &domain.NotificationConfig{Type: "sse"}}
		}
		docs, err := h.docs.SubmitBatch(c.Request.Context(), userID, items)
		if err != nil {
			return nil, toolFailure(err)
		}
		out := make([]gin.H, len(docs))
		for i := range docs {
			out[i] = gin.H{"index": i, "documentId": docs[i].ID, "task": mcpTask(&docs[i])}
		}
		return toolJSON(gin.H{"documents": out}), nil
	case "get_ocr_document":
		id, fail := documentID(call.Arguments)
		if fail != nil {
			return nil, fail
		}
		doc, err := h.docs.GetDocument(c.Request.Context(), userID, id)
		if err != nil {
			return nil, toolFailure(err)
		}
		return toolJSON(publicMCPDocument(doc)), nil
	default:
		return nil, &rpcFailure{-32602, "unknown tool"}
	}
}

func (h *MCPHandler) getTask(c *gin.Context, userID int64, raw json.RawMessage) (any, *rpcFailure) {
	id, f := taskID(raw)
	if f != nil {
		return nil, f
	}
	doc, err := h.docs.GetDocumentStatus(c.Request.Context(), userID, id)
	if err != nil {
		return nil, toolFailure(err)
	}
	return mcpTask(doc), nil
}
func (h *MCPHandler) taskResult(c *gin.Context, userID int64, raw json.RawMessage) (any, *rpcFailure) {
	id, f := taskID(raw)
	if f != nil {
		return nil, f
	}
	doc, err := h.docs.GetDocument(c.Request.Context(), userID, id)
	if err != nil {
		return nil, toolFailure(err)
	}
	if doc.Status != domain.StatusCompleted {
		return nil, &rpcFailure{-32602, "task result is not available"}
	}
	result := toolJSON(publicMCPDocument(doc))
	result["_meta"] = gin.H{"io.modelcontextprotocol/related-task": gin.H{"taskId": id}}
	return result, nil
}
func (h *MCPHandler) readResource(c *gin.Context, userID int64, raw json.RawMessage) (any, *rpcFailure) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := strictJSON(raw, &p); err != nil {
		return nil, invalidParams(err)
	}
	const prefix = "ocr://documents/"
	if !strings.HasPrefix(p.URI, prefix) {
		return nil, invalidParams(errors.New("unsupported resource URI"))
	}
	documentID := strings.TrimPrefix(p.URI, prefix)
	if !documentIDPattern.MatchString(documentID) {
		return nil, invalidParams(errors.New("resource document ID format is invalid"))
	}
	doc, err := h.docs.GetDocument(c.Request.Context(), userID, documentID)
	if err != nil {
		return nil, toolFailure(err)
	}
	b, _ := json.Marshal(publicMCPDocument(doc))
	return gin.H{"contents": []gin.H{{"uri": p.URI, "mimeType": "application/json", "text": string(b)}}}, nil
}

func publicMCPDocument(doc *domain.Document) gin.H {
	out := gin.H{"documentId": doc.ID, "status": doc.Status, "createdAt": doc.CreatedAt, "updatedAt": doc.UpdatedAt}
	if doc.Status == domain.StatusCompleted && doc.Result != nil {
		out["result"] = doc.Result
		out["resultExpiresAt"] = doc.ResultExpiresAt
	}
	if doc.ErrorDetail != "" {
		out["errorDetail"] = doc.ErrorDetail
	}
	return out
}
func mcpTask(doc *domain.Document) gin.H {
	return gin.H{"taskId": doc.ID, "status": mcpTaskStatus(doc.Status), "statusMessage": doc.ErrorDetail, "createdAt": doc.CreatedAt, "lastUpdatedAt": doc.UpdatedAt, "ttl": nil, "pollInterval": 3000}
}
func mcpTaskStatus(s domain.DocumentStatus) string {
	switch s {
	case domain.StatusCompleted:
		return "completed"
	case domain.StatusFailed:
		return "failed"
	case domain.StatusCancelled:
		return "cancelled"
	default:
		return "working"
	}
}
func toolJSON(v any) gin.H {
	b, _ := json.Marshal(v)
	return gin.H{"content": []gin.H{{"type": "text", "text": string(b)}}, "structuredContent": v, "isError": false}
}
func taskID(raw json.RawMessage) (string, *rpcFailure) {
	var p struct {
		TaskID string `json:"taskId"`
	}
	if err := strictJSON(raw, &p); err != nil || !documentIDPattern.MatchString(p.TaskID) {
		if err == nil {
			err = errors.New("taskId format is invalid")
		}
		return "", invalidParams(err)
	}
	return p.TaskID, nil
}
func documentID(raw json.RawMessage) (string, *rpcFailure) {
	var p struct {
		DocumentID string `json:"documentId"`
	}
	if err := strictJSON(raw, &p); err != nil || !documentIDPattern.MatchString(p.DocumentID) {
		if err == nil {
			err = errors.New("documentId format is invalid")
		}
		return "", invalidParams(err)
	}
	return p.DocumentID, nil
}
func strictJSON(raw []byte, v any) error {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	return decodeOneJSON(d, v)
}

func isJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value map[string]any
	return json.Unmarshal(raw, &value) == nil && value != nil
}
func decodeOneJSON(d *json.Decoder, v any) error {
	if err := d.Decode(v); err != nil {
		return err
	}
	var trailing any
	if err := d.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request must contain exactly one JSON value")
		}
		return err
	}
	return nil
}
func invalidParams(err error) *rpcFailure { return &rpcFailure{-32602, err.Error()} }
func toolFailure(err error) *rpcFailure {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return &rpcFailure{-32004, "document not found"}
	case errors.Is(err, domain.ErrResultExpired):
		return &rpcFailure{-32010, "OCR result has expired"}
	case errors.Is(err, domain.ErrRateLimited):
		return &rpcFailure{-32029, "rate limit exceeded"}
	case errors.Is(err, domain.ErrQuotaExceeded):
		return &rpcFailure{-32029, "document quota exceeded"}
	case errors.Is(err, domain.ErrConflict):
		return &rpcFailure{-32009, "document state conflict"}
	case errors.Is(err, domain.ErrInvalidSource), errors.Is(err, domain.ErrInvalidBase64),
		errors.Is(err, domain.ErrBase64TooLarge), errors.Is(err, domain.ErrInvalidURL),
		errors.Is(err, domain.ErrSSRFBlocked), errors.Is(err, domain.ErrUnsupportedMediaType),
		errors.Is(err, domain.ErrFileValidation), errors.Is(err, domain.ErrBadParamInput):
		return &rpcFailure{-32602, err.Error()}
	case errors.Is(err, domain.ErrStorageUnavailable):
		return &rpcFailure{-32003, "service temporarily unavailable"}
	default:
		return &rpcFailure{-32603, "internal error"}
	}
}
func mcpError(id any, code int, message string) gin.H {
	return gin.H{"jsonrpc": "2.0", "id": id, "error": gin.H{"code": code, "message": message}}
}
func (h *MCPHandler) validOrigin(c *gin.Context) bool {
	origin := strings.TrimRight(c.GetHeader("Origin"), "/")
	return origin == "" || origin == h.allowedOrigin
}
