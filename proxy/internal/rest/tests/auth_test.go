package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/rest"
	"macocr/proxy/internal/usecase/auth"
)

type fakeAuthService struct {
	users   map[int64]*domain.User
	configs map[int64]*domain.AccountConfig
	keys    map[int64][]domain.ApiKey
	seq     int64
}

func newFakeAuthService() *fakeAuthService {
	return &fakeAuthService{
		users:   make(map[int64]*domain.User),
		configs: make(map[int64]*domain.AccountConfig),
		keys:    make(map[int64][]domain.ApiKey),
	}
}

func (f *fakeAuthService) CreateUser(_ context.Context, email string, role domain.Role, _ string, rpm *int, quota *int64) (*domain.User, error) {
	f.seq++
	r := role
	if r == "" {
		r = domain.RoleUser
	}
	u := &domain.User{
		ID:        f.seq,
		Email:     email,
		Role:      r,
		Disabled:  false,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	c := &domain.AccountConfig{
		UserID:       f.seq,
		RateLimitRPM: 60,
		DocQuota:     0,
		DocUsed:      0,
		UpdatedAt:    time.Now(),
	}
	if rpm != nil {
		c.RateLimitRPM = *rpm
	}
	if quota != nil {
		c.DocQuota = *quota
	}
	u.Config = c
	f.users[f.seq] = u
	f.configs[f.seq] = c
	return u, nil
}

func (f *fakeAuthService) GetUser(_ context.Context, id int64) (*domain.User, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	u.Config = f.configs[id]
	return u, nil
}

func (f *fakeAuthService) ListUsers(_ context.Context, _, _ int) ([]domain.User, error) {
	var out []domain.User
	for _, u := range f.users {
		u.Config = f.configs[u.ID]
		out = append(out, *u)
	}
	return out, nil
}

func (f *fakeAuthService) UpdateUser(_ context.Context, id int64, email *string, role *domain.Role, disabled *bool) (*domain.User, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if email != nil {
		u.Email = *email
	}
	if role != nil {
		u.Role = *role
	}
	if disabled != nil {
		u.Disabled = *disabled
	}
	u.Config = f.configs[id]
	return u, nil
}

func (f *fakeAuthService) GetAccountConfig(_ context.Context, userID int64) (*domain.AccountConfig, error) {
	c, ok := f.configs[userID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return c, nil
}

func (f *fakeAuthService) UpdateAccountConfig(_ context.Context, userID int64, rpm *int, quota *int64, _ *int64) (*domain.AccountConfig, error) {
	c, ok := f.configs[userID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if rpm != nil {
		c.RateLimitRPM = *rpm
	}
	if quota != nil {
		c.DocQuota = *quota
	}
	return c, nil
}

func (f *fakeAuthService) ResetDocQuota(_ context.Context, userID int64) error {
	c, ok := f.configs[userID]
	if !ok {
		return domain.ErrNotFound
	}
	c.DocUsed = 0
	return nil
}

func (f *fakeAuthService) GenerateKey(_ context.Context, userID int64, name string, rpm int) (*auth.GeneratedKey, error) {
	if _, ok := f.users[userID]; !ok {
		return nil, domain.ErrNotFound
	}
	k := domain.ApiKey{
		ID:           int64(len(f.keys[userID]) + 1),
		UserID:       userID,
		Name:         name,
		Prefix:       "sk_ocr_test",
		RateLimitRPM: rpm,
		CreatedAt:    time.Now(),
	}
	f.keys[userID] = append(f.keys[userID], k)
	return &auth.GeneratedKey{
		Key:          "sk_ocr_test_secret_plaintext",
		KeyID:        k.ID,
		UserID:       userID,
		Name:         name,
		Prefix:       k.Prefix,
		RateLimitRPM: rpm,
	}, nil
}

func (f *fakeAuthService) ListKeys(_ context.Context, userID int64) ([]domain.ApiKey, error) {
	return f.keys[userID], nil
}

func (f *fakeAuthService) RevokeKey(_ context.Context, userID, keyID int64) error {
	for i, k := range f.keys[userID] {
		if k.ID == keyID {
			now := time.Now()
			f.keys[userID][i].RevokedAt = &now
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeAuthService) Authenticate(_ context.Context, raw string) (*domain.ApiKey, error) {
	if raw == "valid_key" {
		return &domain.ApiKey{ID: 1, UserID: 1}, nil
	}
	return nil, domain.ErrUnauthorized
}

func (f *fakeAuthService) ValidateActive(ctx context.Context, raw string) (*domain.ApiKey, error) {
	return f.Authenticate(ctx, raw)
}

func newAuthTestRouter(svc rest.AuthService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/v1")

	h := rest.NewAuthHandler(svc)
	api.POST("/users", h.CreateUser)
	api.GET("/users", h.ListUsers)
	api.GET("/users/:id", h.GetUser)
	api.PATCH("/users/:id", h.UpdateUser)
	api.POST("/users/:id/deactivate", h.DeactivateUser)

	api.GET("/users/:id/config", h.GetAccountConfig)
	api.PATCH("/users/:id/config", h.UpdateAccountConfig)
	api.POST("/users/:id/config/reset-quota", h.ResetDocQuota)

	api.POST("/users/:id/apikeys", h.CreateAPIKey)
	api.GET("/users/:id/apikeys", h.ListAPIKeys)
	api.DELETE("/users/:id/apikeys/:kid", h.RevokeAPIKey)

	return engine
}

func TestUserAndConfigRESTEndpoints(t *testing.T) {
	svc := newFakeAuthService()
	r := newAuthTestRouter(svc)

	// 1. POST /v1/users
	body, _ := json.Marshal(map[string]any{
		"email":          "alice@example.com",
		"role":           "user",
		"rate_limit_rpm": 120,
		"doc_quota":      500,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/users", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("POST /v1/users want 201, got %d body=%s", w.Code, w.Body.String())
	}

	// 2. GET /v1/users/1
	req = httptest.NewRequest(http.MethodGet, "/v1/users/1", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/users/1 want 200, got %d", w.Code)
	}

	// 3. POST /v1/users/1/deactivate
	req = httptest.NewRequest(http.MethodPost, "/v1/users/1/deactivate", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !svc.users[1].Disabled {
		t.Fatalf("POST /v1/users/1/deactivate want disabled account, got %d body=%s", w.Code, w.Body.String())
	}

	// 4. PATCH /v1/users/1 can reactivate or update the account.
	patchBody, _ := json.Marshal(map[string]any{
		"disabled": false,
	})
	req = httptest.NewRequest(http.MethodPatch, "/v1/users/1", bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH /v1/users/1 want 200, got %d", w.Code)
	}

	// 5. GET /v1/users/1/config
	req = httptest.NewRequest(http.MethodGet, "/v1/users/1/config", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/users/1/config want 200, got %d", w.Code)
	}

	// 6. PATCH /v1/users/1/config
	cfgBody, _ := json.Marshal(map[string]any{
		"rate_limit_rpm": 300,
		"doc_quota":      2000,
	})
	req = httptest.NewRequest(http.MethodPatch, "/v1/users/1/config", bytes.NewReader(cfgBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH /v1/users/1/config want 200, got %d", w.Code)
	}

	// 6. POST /v1/users/1/config/reset-quota
	req = httptest.NewRequest(http.MethodPost, "/v1/users/1/config/reset-quota", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /v1/users/1/config/reset-quota want 200, got %d", w.Code)
	}

	// 7. POST /v1/users/1/apikeys
	keyBody, _ := json.Marshal(map[string]any{
		"name":           "ci-key",
		"rate_limit_rpm": 60,
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/users/1/apikeys", bytes.NewReader(keyBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /v1/users/1/apikeys want 201, got %d", w.Code)
	}

	// 8. GET /v1/users/1/apikeys
	req = httptest.NewRequest(http.MethodGet, "/v1/users/1/apikeys", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/users/1/apikeys want 200, got %d", w.Code)
	}

	// 9. DELETE /v1/users/1/apikeys/1
	req = httptest.NewRequest(http.MethodDelete, "/v1/users/1/apikeys/1", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE /v1/users/1/apikeys/1 want 204, got %d", w.Code)
	}
}

func TestAccountAdminValidation(t *testing.T) {
	svc := newFakeAuthService()
	r := newAuthTestRouter(svc)

	for _, target := range []string{"/v1/users?limit=0", "/v1/users?limit=101", "/v1/users?offset=-1", "/v1/users?limit=invalid"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected %s to return 400, got %d", target, w.Code)
		}
	}

	for _, target := range []string{"/v1/users/1", "/v1/users/1/config"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, target, bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected empty update %s to return 400, got %d", target, w.Code)
		}
	}
}
