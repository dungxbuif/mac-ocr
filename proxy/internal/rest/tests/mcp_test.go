package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/notifications"
	"macocr/proxy/internal/rest"
	"macocr/proxy/internal/usecase/auth"
	"macocr/proxy/internal/usecase/document"
)

type mockNotificationRepo struct{ events []domain.NotificationEvent }

func (m *mockNotificationRepo) Create(_ context.Context, event *domain.NotificationEvent) error {
	m.events = append(m.events, *event)
	return nil
}
func (m *mockNotificationRepo) ClaimWebhook(context.Context) (*domain.NotificationEvent, error) {
	return nil, nil
}
func (m *mockNotificationRepo) MarkDelivered(context.Context, string) error { return nil }
func (m *mockNotificationRepo) MarkFailed(context.Context, string, string, time.Time) error {
	return nil
}
func (m *mockNotificationRepo) ListSSE(context.Context, int64, string, int) ([]domain.NotificationEvent, error) {
	return m.events, nil
}
func (m *mockNotificationRepo) DeleteBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func TestMCPInitializeAndToolsList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	users, configs, keys := newMockUserRepo(), newMockConfigRepo(), newMockKeyRepoFull()
	users.users[1] = &domain.User{ID: 1, Email: "agent@example.com"}
	configs.configs[1] = &domain.AccountConfig{UserID: 1, RateLimitRPM: 100, DocQuota: 10}
	authSvc := auth.NewService(users, configs, keys, nil)
	generated, err := authSvc.GenerateKey(context.Background(), 1, "agent", 100)
	if err != nil {
		t.Fatal(err)
	}
	notifySvc := notifications.NewService(&mockNotificationRepo{}, "https://ocr.example.com", slog.New(slog.NewTextHandler(io.Discard, nil)))
	docSvc := document.NewService(newMockDocRepoAdapter(), newMockObjectRepoFull(), authSvc, notifySvc, nil)
	handler := rest.NewMCPHandler(docSvc, notifySvc, "https://ocr.example.com")
	authHandler := rest.NewAuthHandler(authSvc)
	router := gin.New()
	router.POST("/mcp", authHandler.RequireAPIKey(), handler.Post)

	for _, method := range []string{"initialize", "tools/list"} {
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}}})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+generated.Key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"jsonrpc":"2.0"`) {
			t.Fatalf("%s failed: %d %s", method, w.Code, w.Body.String())
		}
	}
}

func TestMCPRejectsNonJSONAndMalformedResourceIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	users, configs, keys := newMockUserRepo(), newMockConfigRepo(), newMockKeyRepoFull()
	users.users[1] = &domain.User{ID: 1, Email: "agent@example.com"}
	configs.configs[1] = &domain.AccountConfig{UserID: 1, RateLimitRPM: 100, DocQuota: 10}
	authSvc := auth.NewService(users, configs, keys, nil)
	generated, err := authSvc.GenerateKey(context.Background(), 1, "agent", 100)
	if err != nil {
		t.Fatal(err)
	}
	notifySvc := notifications.NewService(&mockNotificationRepo{}, "https://ocr.example.com", slog.New(slog.NewTextHandler(io.Discard, nil)))
	handler := rest.NewMCPHandler(document.NewService(newMockDocRepoAdapter(), newMockObjectRepoFull(), authSvc, notifySvc, nil), notifySvc, "https://ocr.example.com")
	authHandler := rest.NewAuthHandler(authSvc)
	router := gin.New()
	router.POST("/mcp", authHandler.RequireAPIKey(), handler.Post)

	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+generated.Key)
	req.Header.Set("Content-Type", "text/plain")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected MCP non-JSON request to return 415, got %d", w.Code)
	}

	payload = []byte(`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"ocr://documents/not-a-document"}}`)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+generated.Key)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":-32602`) {
		t.Fatalf("expected invalid resource ID error, got %d %s", w.Code, w.Body.String())
	}
}

func TestMCPInitializeValidatesRequiredParametersAndNegotiatesVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	users, configs, keys := newMockUserRepo(), newMockConfigRepo(), newMockKeyRepoFull()
	users.users[1] = &domain.User{ID: 1, Email: "agent@example.com"}
	configs.configs[1] = &domain.AccountConfig{UserID: 1, RateLimitRPM: 100, DocQuota: 10}
	authSvc := auth.NewService(users, configs, keys, nil)
	generated, err := authSvc.GenerateKey(context.Background(), 1, "agent", 100)
	if err != nil {
		t.Fatal(err)
	}
	notifySvc := notifications.NewService(&mockNotificationRepo{}, "https://ocr.example.com", slog.New(slog.NewTextHandler(io.Discard, nil)))
	handler := rest.NewMCPHandler(document.NewService(newMockDocRepoAdapter(), newMockObjectRepoFull(), authSvc, notifySvc, nil), notifySvc, "https://ocr.example.com")
	router := gin.New()
	router.POST("/mcp", rest.NewAuthHandler(authSvc).RequireAPIKey(), handler.Post)

	for _, tc := range []struct {
		name, body, contains string
	}{
		{"missing parameters", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, `"code":-32602`},
		{"version negotiation", `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`, `"protocolVersion":"2025-11-25"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+generated.Key)
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.contains) {
				t.Fatalf("expected %q in response, got %d %s", tc.contains, w.Code, w.Body.String())
			}
		})
	}
}
