package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"log/slog"
	"macocr/proxy/domain"
	"macocr/proxy/internal/native"
	"macocr/proxy/internal/rest"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

type callbackWorkerStore struct{}

func (callbackWorkerStore) CreateEnrollment(context.Context, string, time.Time) error    { return nil }
func (callbackWorkerStore) Enroll(context.Context, string, string, string, string) error { return nil }
func (callbackWorkerStore) Secret(_ context.Context, node string) (string, error) {
	if node == "owner" {
		return "owner-secret", nil
	}
	if node == "other" {
		return "other-secret", nil
	}
	return "", domain.ErrUnauthorized
}
func (callbackWorkerStore) ListWorkers(context.Context) ([]native.WorkerNode, error) { return nil, nil }
func (callbackWorkerStore) Revoke(context.Context, string) error                     { return nil }
func (callbackWorkerStore) Assign(context.Context, native.WorkerRoute) error         { return nil }
func (callbackWorkerStore) AssignedNode(context.Context, string, string) (string, error) {
	return "owner", nil
}
func (callbackWorkerStore) Routes(context.Context) ([]native.WorkerRoute, error) { return nil, nil }
func TestOutboundCallbackRequiresAssignedNodeAndPerNodeSecret(t *testing.T) {
	gin.SetMode(gin.TestMode)
	docs := newMockDocRepoAdapter()
	docs.docs["doc_1"] = &domain.Document{ID: "doc_1", UserID: 1, Status: domain.StatusProcessing, AttemptID: "att_1"}
	handler := rest.NewWebhookHandler(docs, newMockObjectRepoFull(), "legacy-secret", nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, time.Hour)
	handler.SetWorkerHub(native.NewWorkerHub(callbackWorkerStore{}))
	router := gin.New()
	router.POST("/events", handler.HandleNativeEvent)
	send := func(node, secret string) int {
		event := domain.NativeEvent{EventID: "evt_1", Type: "attempt.completed", NodeID: node, DocumentID: "doc_1", AttemptID: "att_1", Result: &domain.OCRResult{Text: "done"}}
		body, _ := json.Marshal(event)
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		req := httptest.NewRequest("POST", "/events", bytes.NewReader(body))
		req.Header.Set("X-Native-Node-Id", node)
		req.Header.Set("X-Native-Timestamp", ts)
		req.Header.Set("X-Native-Event-Id", event.EventID)
		req.Header.Set("X-Native-Signature", native.SignWebhook(secret, node, ts, event.EventID, body))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}
	if code := send("owner", "legacy-secret"); code != http.StatusUnauthorized {
		t.Fatalf("legacy key accepted: %d", code)
	}
	if code := send("other", "other-secret"); code != http.StatusConflict {
		t.Fatalf("wrong owner accepted: %d", code)
	}
	if code := send("owner", "owner-secret"); code != http.StatusOK {
		t.Fatalf("owner completion rejected: %d", code)
	}
	if code := send("other", "other-secret"); code != http.StatusConflict {
		t.Fatalf("wrong owner terminal replay accepted: %d", code)
	}
}

func TestSharedOutboundCallbackUsesBearerWithoutHMAC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	docs := newMockDocRepoAdapter()
	docs.docs["doc_1"] = &domain.Document{ID: "doc_1", UserID: 1, Status: domain.StatusProcessing, AttemptID: "att_1"}
	hub := native.NewWorkerHub(callbackWorkerStore{})
	hub.SetSharedSecret("shared-worker-key-at-least-32-characters")
	handler := rest.NewWebhookHandler(docs, newMockObjectRepoFull(), "legacy-secret", nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, time.Hour)
	handler.SetWorkerHub(hub)
	router := gin.New()
	router.POST("/events", handler.HandleNativeEvent)
	event := domain.NativeEvent{EventID: "evt_1", Type: "attempt.completed", NodeID: "owner", DocumentID: "doc_1", AttemptID: "att_1", Result: &domain.OCRResult{Text: "done"}}
	body, _ := json.Marshal(event)
	send := func(bearer string) int {
		req := httptest.NewRequest("POST", "/events", bytes.NewReader(body))
		req.Header.Set("X-Native-Node-Id", "owner")
		req.Header.Set("X-Native-Event-Id", "evt_1")
		req.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}
	if code := send("legacy-secret"); code != 401 {
		t.Fatalf("bad bearer accepted: %d", code)
	}
	if code := send("shared-worker-key-at-least-32-characters"); code != 200 {
		t.Fatalf("valid bearer rejected without HMAC: %d", code)
	}
}
func TestSharedScanUploadRejectsWrongAssignedWorker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	docs := newMockDocRepoAdapter()
	docs.docs["scan_1"] = &domain.Document{ID: "scan_1", UserID: 1, Status: domain.StatusProcessing, AttemptID: "att_1", Options: &domain.OCROptions{ProcessingMode: "scan"}}
	hub := native.NewWorkerHub(callbackWorkerStore{})
	hub.SetSharedSecret("shared-worker-key-at-least-32-characters")
	handler := rest.NewWebhookHandler(docs, newMockObjectRepoFull(), "legacy", nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, time.Hour)
	handler.SetWorkerHub(hub)
	router := gin.New()
	router.PUT("/scans/:scanId/:attemptId/output", handler.HandleNativeScanOutput)
	req := httptest.NewRequest("PUT", "/scans/scan_1/att_1/output", bytes.NewReader([]byte("jpeg-output")))
	req.Header.Set("Content-Type", "image/jpeg")
	req.Header.Set("X-Native-Node-Id", "other")
	req.Header.Set("X-Native-Event-Id", "out_scan_1_att_1")
	req.Header.Set("Authorization", "Bearer shared-worker-key-at-least-32-characters")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Fatalf("wrong assigned scan worker accepted: %d", w.Code)
	}
}
