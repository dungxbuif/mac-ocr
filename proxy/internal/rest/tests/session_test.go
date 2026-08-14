package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/rest"
	"macocr/proxy/internal/usecase/auth"
)

type mockRedisSessionRepo struct {
	data map[string][]byte
}

func newMockRedisSessionRepo() *mockRedisSessionRepo {
	return &mockRedisSessionRepo{data: make(map[string][]byte)}
}

func (m *mockRedisSessionRepo) SetSession(_ context.Context, token string, data []byte, _ time.Duration) error {
	m.data[token] = data
	return nil
}

func (m *mockRedisSessionRepo) GetSession(_ context.Context, token string) ([]byte, error) {
	d, ok := m.data[token]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return d, nil
}

func (m *mockRedisSessionRepo) DeleteSession(_ context.Context, token string) error {
	delete(m.data, token)
	return nil
}

func TestAdminSessionAuth_LoginMeLogout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	userRepo := newMockUserRepo()
	cfgRepo := newMockConfigRepo()
	redisRepo := newMockRedisSessionRepo()
	sm := rest.NewSessionManager(redisRepo)

	hash, _ := auth.HashPassword("AdminPass123!")
	adminUser := &domain.User{
		ID:           10,
		Email:        "admin@test.local",
		Role:         domain.RoleAdmin,
		PasswordHash: hash,
		Disabled:     false,
	}
	userRepo.users[10] = adminUser

	adminAuth := rest.NewAdminAuthHandler(userRepo, nil, sm, false)

	api := r.Group("/v1")
	api.POST("/auth/login", adminAuth.Login)
	api.POST("/auth/logout", adminAuth.Logout)
	api.GET("/auth/me", adminAuth.Me)

	adminGroup := api.Group("/admin", adminAuth.RequireAdminSession())
	adminGroup.POST("/action", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	loginBody, _ := json.Marshal(map[string]string{
		"email":    "admin@test.local",
		"password": "AdminPass123!",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login failed, expected 200 got %d: %s", w.Code, w.Body.String())
	}

	var loginResp struct {
		CSRFToken string `json:"csrfToken"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &loginResp)
	if loginResp.CSRFToken == "" {
		t.Fatalf("expected csrfToken in login response")
	}

	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == rest.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("expected session cookie set")
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, "/v1/auth/me", nil)
	req.AddCookie(sessionCookie)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on /v1/auth/me, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/v1/admin/action", nil)
	req.AddCookie(sessionCookie)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on mutation without CSRF token, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/v1/admin/action", nil)
	req.AddCookie(sessionCookie)
	req.Header.Set("X-CSRF-Token", loginResp.CSRFToken)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on mutation with valid CSRF token, got %d", w.Code)
	}

	adminUser.Disabled = true
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/v1/admin/action", nil)
	req.AddCookie(sessionCookie)
	req.Header.Set("X-CSRF-Token", loginResp.CSRFToken)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected deactivated admin session to be rejected, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	req.AddCookie(sessionCookie)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on logout, got %d", w.Code)
	}

	_ = cfgRepo
}

func TestAdminLoginRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := rest.NewAdminAuthHandler(newMockUserRepo(), nil, rest.NewSessionManager(nil), false)
	r.POST("/v1/auth/login", h.Login)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(`{"email":"admin@test.local","password":"`+strings.Repeat("x", 9<<10)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"code":"PAYLOAD_TOO_LARGE"`) {
		t.Fatalf("expected machine-readable payload limit response: %s", w.Body.String())
	}
}

func TestAdminLoginRateLimitsBeforePasswordVerification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	users := newMockUserRepo()
	hash, err := auth.HashPassword("AdminPass123!")
	if err != nil {
		t.Fatal(err)
	}
	users.users[10] = &domain.User{ID: 10, Email: "admin@test.local", Role: domain.RoleAdmin, PasswordHash: hash}
	h := rest.NewAdminAuthHandler(users, nil, rest.NewSessionManager(nil), false)
	r.POST("/v1/auth/login", h.Login)

	body := `{"email":"admin@test.local","password":"wrong password"}`
	for attempt := 1; attempt <= 6; attempt++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.10:4321"
		r.ServeHTTP(w, req)

		want := http.StatusUnauthorized
		if attempt == 6 {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Fatalf("attempt %d: expected %d, got %d: %s", attempt, want, w.Code, w.Body.String())
		}
		if attempt == 6 && w.Header().Get("Retry-After") != "60" {
			t.Fatalf("expected Retry-After on rate limit")
		}
	}
}
