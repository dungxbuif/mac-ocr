package tests

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	api.GET("/documents/:id", authHandler.RequireAPIKey(), docHandler.Get)

	api.POST("/batches", authHandler.RequireAPIKey(), batchHandler.Submit)

	return r
}

func setupUploadTestRouter(docSvc *document.Service, authSvc *auth.Service, objRepo *mockObjectRepoFull, maxUploadBytes int64) *gin.Engine {
	r := setupDocTestRouter(docSvc, authSvc)
	authHandler := rest.NewAuthHandler(authSvc)
	uploadHandler := rest.NewUploadHandler(objRepo, maxUploadBytes)
	r.POST("/v1/uploads/presign", authHandler.RequireAPIKey(), uploadHandler.Presign)
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
	docRepo.configs = cfgRepo

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
		Links      []struct {
			Rel  string `json:"rel"`
			Href string `json:"href"`
		} `json:"links"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &submitResp)
	if submitResp.DocumentID == "" {
		t.Fatalf("expected documentId in response")
	}
	foundSelf := false
	for _, link := range submitResp.Links {
		if link.Rel == "self" && link.Href != "" {
			foundSelf = true
		}
		if link.Rel == "cancel" {
			t.Fatal("submission response must not advertise user cancellation")
		}
	}
	if !foundSelf {
		t.Fatal("submission response must advertise its self link")
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

	for _, unavailable := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/v1/documents"},
		{method: http.MethodDelete, path: "/v1/documents/" + submitResp.DocumentID},
	} {
		w = httptest.NewRecorder()
		req = httptest.NewRequest(unavailable.method, unavailable.path, nil)
		req.Header.Set("Authorization", "Bearer "+genKey.Key)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected removed endpoint %s %s to return 404, got %d", unavailable.method, unavailable.path, w.Code)
		}
	}
}

func TestPresignedUploadFlowAndLimits(t *testing.T) {
	validPNGBytes, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")
	userRepo := newMockUserRepo()
	cfgRepo := newMockConfigRepo()
	keyRepo := newMockKeyRepoFull()
	objRepo := newMockObjectRepoFull()
	docRepo := newMockDocRepoAdapter()

	userRepo.users[1] = &domain.User{ID: 1, Email: "upload@user.com"}
	userRepo.users[2] = &domain.User{ID: 2, Email: "other@user.com"}
	cfgRepo.configs[1] = &domain.AccountConfig{UserID: 1, RateLimitRPM: 100, DocQuota: 1, DocUsed: 0}
	cfgRepo.configs[2] = &domain.AccountConfig{UserID: 2, RateLimitRPM: 100, DocQuota: 10, DocUsed: 0}
	docRepo.configs = cfgRepo

	authSvc := auth.NewService(userRepo, cfgRepo, keyRepo, nil)
	docSvc := document.NewServiceWithMaxUploadBytes(docRepo, objRepo, authSvc, nil, nil, 1024)
	genKey, err := authSvc.GenerateKey(nil, 1, "upload-key", 100)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	otherKey, err := authSvc.GenerateKey(nil, 2, "other-key", 100)
	if err != nil {
		t.Fatalf("GenerateKey other failed: %v", err)
	}

	router := setupUploadTestRouter(docSvc, authSvc, objRepo, 1024)

	w := httptest.NewRecorder()
	oversizedBody := `{"filename":"` + strings.Repeat("x", 9<<10) + `","sizeBytes":1}`
	req := httptest.NewRequest(http.MethodPost, "/v1/uploads/presign", strings.NewReader(oversizedBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+genKey.Key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), `"code":"PAYLOAD_TOO_LARGE"`) {
		t.Fatalf("expected oversized presign envelope rejection, got %d: %s", w.Code, w.Body.String())
	}

	for _, size := range []int64{1025, 1 << 30} {
		w := httptest.NewRecorder()
		body := fmt.Sprintf(`{"filename":"sample.png","sizeBytes":%d}`, size)
		req := httptest.NewRequest(http.MethodPost, "/v1/uploads/presign", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+genKey.Key)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), `"code":"URL_CONTENT_TOO_LARGE"`) {
			t.Fatalf("expected size %d presign rejection, got %d: %s", size, w.Code, w.Body.String())
		}
	}
	if cfgRepo.configs[1].DocUsed != 0 {
		t.Fatalf("rejected presign consumed document quota: %d", cfgRepo.configs[1].DocUsed)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/uploads/presign", strings.NewReader(`{"filename":"boundary.bin","sizeBytes":1024}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+genKey.Key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected upload at exact byte limit to be accepted, got %d: %s", w.Code, w.Body.String())
	}
	if cfgRepo.configs[1].DocUsed != 0 {
		t.Fatalf("successful presign consumed document quota: %d", cfgRepo.configs[1].DocUsed)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/uploads/presign", strings.NewReader(`{"filename":"sample.png","sizeBytes":70,"contentType":"image/png"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+genKey.Key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected presign 201, got %d: %s", w.Code, w.Body.String())
	}
	var presign struct {
		SourceURL string `json:"sourceUrl"`
		UploadURL string `json:"uploadUrl"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &presign); err != nil || presign.SourceURL == "" || presign.UploadURL == "" {
		t.Fatalf("invalid presign response: %+v err=%v", presign, err)
	}

	sourceKey := strings.TrimPrefix(presign.SourceURL, "s3://macocr-inputs/")
	objRepo.stored[sourceKey] = validPNGBytes
	payload, _ := json.Marshal(map[string]any{"input": map[string]string{"url": presign.SourceURL}})
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/documents", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+genKey.Key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected own-S3 submit accepted, got %d: %s", w.Code, w.Body.String())
	}
	if cfgRepo.configs[1].DocUsed != 1 {
		t.Fatalf("accepted document should consume one quota unit, got %d", cfgRepo.configs[1].DocUsed)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/documents", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+genKey.Key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), `"code":"QUOTA_EXCEEDED"`) {
		t.Fatalf("expected document quota rejection, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/documents", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+otherKey.Key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected cross-user source URL to be hidden as not found, got %d: %s", w.Code, w.Body.String())
	}

	oversizedSourceURL := "s3://macocr-inputs/uploads/1/oversized.png"
	objRepo.stored["uploads/1/oversized.png"] = append(validPNGBytes, bytes.Repeat([]byte{1}, 2048)...)
	payload, _ = json.Marshal(map[string]any{"input": map[string]string{"url": oversizedSourceURL}})
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/documents", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+genKey.Key)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), `"code":"URL_CONTENT_TOO_LARGE"`) {
		t.Fatalf("expected submit-time oversized uploaded object rejection, got %d: %s", w.Code, w.Body.String())
	}
}
