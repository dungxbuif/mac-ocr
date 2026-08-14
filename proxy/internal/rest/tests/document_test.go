package tests

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/rest"
	"macocr/proxy/internal/usecase/auth"
	"macocr/proxy/internal/usecase/document"
)

func setupDocTestRouter(docSvc *document.Service, authSvc *auth.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	docHandler := rest.NewDocumentHandler(docSvc, "http://api.local", "http://docs.local")
	batchHandler := rest.NewBatchHandler(docSvc, "http://api.local", "http://docs.local")
	capHandler := rest.NewCapabilitiesHandler()
	authHandler := rest.NewAuthHandler(authSvc)

	api := r.Group("/v1")
	api.GET("/ocr/capabilities", capHandler.Get)

	api.POST("/documents", authHandler.RequireAPIKey(), docHandler.Submit)
	api.GET("/documents", authHandler.RequireAPIKey(), docHandler.List)
	api.GET("/documents/:id", authHandler.RequireAPIKey(), docHandler.Get)
	api.DELETE("/documents/:id", authHandler.RequireAPIKey(), docHandler.Cancel)

	api.POST("/batches", authHandler.RequireAPIKey(), batchHandler.Submit)

	return r
}

func TestDocumentREST_EndToEnd(t *testing.T) {
	validPNGBytes, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")
	userRepo := newMockUserRepo()
	cfgRepo := newMockConfigRepo()
	keyRepo := newMockKeyRepoFull()
	objRepo := newMockObjectRepoFull()
	docRepo := newMockDocRepoAdapter()

	userRepo.users[1] = &domain.User{ID: 1, Email: "test@user.com"}
	cfgRepo.configs[1] = &domain.AccountConfig{UserID: 1, RateLimitRPM: 100, DocQuota: 10, DocUsed: 0}

	authSvc := auth.NewService(userRepo, cfgRepo, keyRepo, nil)
	docSvc := document.NewService(docRepo, objRepo, authSvc, nil, nil)

	genKey, err := authSvc.GenerateKey(nil, 1, "test-key", 100)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	router := setupDocTestRouter(docSvc, authSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/v1/ocr/capabilities", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on /v1/ocr/capabilities, got %d", w.Code)
	}

	body, _ := json.Marshal(map[string]any{
		"input": map[string]string{"base64": base64.StdEncoding.EncodeToString(validPNGBytes)},
	})

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/v1/documents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+genKey.Key)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 on submit, got %d: %s", w.Code, w.Body.String())
	}

	var submitResp struct {
		DocumentID string `json:"documentId"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &submitResp)
	if submitResp.DocumentID == "" {
		t.Fatalf("expected documentId in response")
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/documents", strings.NewReader("not a supported upload"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	req.Header.Set("Authorization", "Bearer "+genKey.Key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnsupportedMediaType || !strings.Contains(w.Body.String(), `"code":"UNSUPPORTED_CONTENT_TYPE"`) {
		t.Fatalf("expected multipart to be rejected, got %d: %s", w.Code, w.Body.String())
	}

	notifyPayload, _ := json.Marshal(map[string]any{
		"input":        map[string]string{"base64": base64.StdEncoding.EncodeToString(validPNGBytes)},
		"notification": map[string]string{"type": "sse"},
	})
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/documents", bytes.NewReader(notifyPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+genKey.Key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected SSE notification submission to be accepted, got %d: %s", w.Code, w.Body.String())
	}
	foundNotification := false
	for _, stored := range docRepo.docs {
		if stored.Notification != nil && stored.Notification.Type == "sse" {
			foundNotification = true
			break
		}
	}
	if !foundNotification {
		t.Fatal("SSE notification configuration was not persisted")
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, "/v1/documents/"+submitResp.DocumentID, nil)
	req.Header.Set("Authorization", "Bearer "+genKey.Key)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on get status, got %d", w.Code)
	}

	batchPayload, _ := json.Marshal([]map[string]any{
		{"input": map[string]string{"base64": base64.StdEncoding.EncodeToString(validPNGBytes)}},
		{"input": map[string]string{"base64": base64.StdEncoding.EncodeToString(validPNGBytes)}},
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/v1/batches", bytes.NewReader(batchPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+genKey.Key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 on direct-array batch, got %d: %s", w.Code, w.Body.String())
	}
	var batchResp struct {
		Items []struct {
			Index      int    `json:"index"`
			DocumentID string `json:"documentId"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &batchResp); err != nil || len(batchResp.Items) != 2 {
		t.Fatalf("unexpected batch response: %s", w.Body.String())
	}
	for i, item := range batchResp.Items {
		if item.Index != i || item.DocumentID == "" {
			t.Fatalf("unexpected batch item %d: %+v", i, item)
		}
	}

	usedBeforeInvalidBatch := cfgRepo.configs[1].DocUsed
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/v1/batches", strings.NewReader(`[{"input":{"base64":"not-valid***"}}]`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+genKey.Key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"INVALID_BASE64"`) || !strings.Contains(w.Body.String(), "batch item 0") {
		t.Fatalf("unexpected invalid batch response: %d %s", w.Code, w.Body.String())
	}
	if cfgRepo.configs[1].DocUsed != usedBeforeInvalidBatch {
		t.Fatalf("invalid batch consumed quota: before=%d after=%d", usedBeforeInvalidBatch, cfgRepo.configs[1].DocUsed)
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodDelete, "/v1/documents/"+submitResp.DocumentID, nil)
	req.Header.Set("Authorization", "Bearer "+genKey.Key)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 on cancel, got %d", w.Code)
	}
}
