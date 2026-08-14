package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/rest"
	"macocr/proxy/internal/usecase/auth"
)

func TestAdminSessionAuth_LoginMeLogout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	userRepo := newMockUserRepo()
	cfgRepo := newMockConfigRepo()
	sm := rest.NewSessionManager()

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
