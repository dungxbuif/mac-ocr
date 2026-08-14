package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/native"
	"macocr/proxy/internal/rest"
)

type webhookResultCache struct {
	results map[string]*domain.OCRResult
}

func (m *webhookResultCache) SetResult(_ context.Context, id string, result *domain.OCRResult, _ time.Duration) error {
	m.results[id] = result
	return nil
}
func (m *webhookResultCache) GetResult(_ context.Context, id string) (*domain.OCRResult, error) {
	return m.results[id], nil
}
func (m *webhookResultCache) DeleteResult(_ context.Context, id string) error {
	delete(m.results, id)
	return nil
}

func TestWebhookRequiresActiveAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	docs := newMockDocRepoAdapter()
	docs.docs["doc_1"] = &domain.Document{ID: "doc_1", UserID: 1, Status: domain.StatusProcessing, AttemptID: "att_active"}
	results := &webhookResultCache{results: make(map[string]*domain.OCRResult)}
	handler := rest.NewWebhookHandler(docs, newMockObjectRepoFull(), "test-secret", nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, results, 24*time.Hour)
	router := gin.New()
	router.POST("/webhooks/native/events", handler.HandleNativeEvent)

	event := domain.NativeEvent{
		EventID: "evt_1", Type: "attempt.completed", NodeID: "node_1", AttemptID: "att_stale",
		DocumentID: "doc_1", Result: &domain.OCRResult{Text: "done", PageCount: 1}, OccurredAt: time.Now().UTC(),
	}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/native/events", bytes.NewReader(body))
	req.Header.Set("X-Native-Node-Id", event.NodeID)
	req.Header.Set("X-Native-Timestamp", ts)
	req.Header.Set("X-Native-Event-Id", event.EventID)
	req.Header.Set("X-Native-Signature", native.SignWebhook("test-secret", event.NodeID, ts, event.EventID, body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict || docs.docs["doc_1"].Status != domain.StatusProcessing {
		t.Fatalf("stale callback was not rejected: status=%d body=%s", w.Code, w.Body.String())
	}

	event.EventID, event.AttemptID = "evt_2", "att_active"
	body, _ = json.Marshal(event)
	ts = strconv.FormatInt(time.Now().Unix(), 10)
	req = httptest.NewRequest(http.MethodPost, "/webhooks/native/events", bytes.NewReader(body))
	req.Header.Set("X-Native-Node-Id", event.NodeID)
	req.Header.Set("X-Native-Timestamp", ts)
	req.Header.Set("X-Native-Event-Id", event.EventID)
	req.Header.Set("X-Native-Signature", native.SignWebhook("test-secret", event.NodeID, ts, event.EventID, body))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || docs.docs["doc_1"].Status != domain.StatusCompleted || docs.docs["doc_1"].ResultExpiresAt == nil {
		t.Fatalf("valid callback failed: status=%d body=%s document=%+v", w.Code, w.Body.String(), docs.docs["doc_1"])
	}
	if results.results["doc_1"] == nil || results.results["doc_1"].Text != "done" {
		t.Fatalf("completed result was not cached: %+v", results.results["doc_1"])
	}
}

func TestNativeConnectionVerification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := rest.NewWebhookHandler(newMockDocRepoAdapter(), newMockObjectRepoFull(), "test-secret", nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, &webhookResultCache{results: make(map[string]*domain.OCRResult)}, 24*time.Hour)
	router := gin.New()
	router.POST("/webhooks/native/verify", handler.HandleNativeVerify)

	body := []byte(`{"nodeId":"node_1","nonce":"0123456789abcdef"}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	eventID := "verify_0123456789abcdef"
	request := func(secret string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/native/verify", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Native-Node-Id", "node_1")
		req.Header.Set("X-Native-Timestamp", ts)
		req.Header.Set("X-Native-Event-Id", eventID)
		req.Header.Set("X-Native-Signature", native.SignWebhook(secret, "node_1", ts, eventID, body))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	if w := request("test-secret"); w.Code != http.StatusOK {
		t.Fatalf("valid native verification failed: status=%d body=%s", w.Code, w.Body.String())
	}
	if w := request("wrong-secret"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong shared key was not rejected: status=%d body=%s", w.Code, w.Body.String())
	}
}
