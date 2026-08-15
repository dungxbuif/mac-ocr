package rest

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/errs"
	"macocr/proxy/internal/usecase/auth"
)

const (
	ctxKey               = "auth.apiKey"
	ctxAPIKeyRevalidator = "auth.apiKeyRevalidator"
	auditUserID          = "audit.user_id"
	auditAPIKeyID        = "audit.api_key_id"
	auditActor           = "audit.actor"
)

type AuthService interface {
	CreateUser(ctx context.Context, email string, role domain.Role, password string, rateLimit *int, docQuota *int64, storageQuotaBytes ...*int64) (*domain.User, error)
	GetUser(ctx context.Context, id int64) (*domain.User, error)
	ListUsers(ctx context.Context, limit, offset int) ([]domain.User, error)
	UpdateUser(ctx context.Context, id int64, email *string, role *domain.Role, disabled *bool) (*domain.User, error)
	ResetPassword(ctx context.Context, id int64, newPassword string) error

	GetAccountConfig(ctx context.Context, userID int64) (*domain.AccountConfig, error)
	UpdateAccountConfig(ctx context.Context, userID int64, rateLimitRPM *int, docQuota *int64, adminID *int64, storageQuotaBytes ...*int64) (*domain.AccountConfig, error)
	ResetDocQuota(ctx context.Context, userID int64) error

	GenerateKey(ctx context.Context, userID int64, name string, rateLimitRPM int) (*auth.GeneratedKey, error)
	ListKeys(ctx context.Context, userID int64) ([]domain.ApiKey, error)
	RevokeKey(ctx context.Context, userID, keyID int64) error
	UpdateKeyRateLimit(ctx context.Context, userID, keyID int64, rateLimitRPM int) (*domain.ApiKey, error)
	Authenticate(ctx context.Context, raw string) (*domain.ApiKey, error)
	ValidateActive(ctx context.Context, raw string) (*domain.ApiKey, error)
}

type AuthHandler struct {
	svc AuthService
}

func NewAuthHandler(svc AuthService) *AuthHandler {
	return &AuthHandler{svc: svc}
}

type createUserReq struct {
	Email             string      `json:"email" binding:"required,email"`
	Password          string      `json:"password,omitempty"`
	Role              domain.Role `json:"role,omitempty" binding:"omitempty,oneof=admin user"`
	RateLimitRPM      *int        `json:"rate_limit_rpm,omitempty" binding:"omitempty,gte=0"`
	DocQuota          *int64      `json:"doc_quota,omitempty" binding:"omitempty,gte=0"`
	StorageQuotaBytes *int64      `json:"storage_quota_bytes,omitempty" binding:"omitempty,gte=0"`
}

func (h *AuthHandler) CreateUser(c *gin.Context) {
	var req createUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondProblem(c, errs.InvalidInput(err.Error()))
		return
	}

	role := domain.RoleUser
	if req.Role != "" {
		role = req.Role
	}

	u, err := h.svc.CreateUser(c.Request.Context(), req.Email, role, req.Password, req.RateLimitRPM, req.DocQuota, req.StorageQuotaBytes)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, u)
}

func (h *AuthHandler) ListUsers(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limit < 1 || limit > 100 {
		RespondProblem(c, errs.InvalidInput("limit must be an integer from 1 through 100"))
		return
	}
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		RespondProblem(c, errs.InvalidInput("offset must be a non-negative integer"))
		return
	}

	users, err := h.svc.ListUsers(c.Request.Context(), limit, offset)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"users":  users,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *AuthHandler) GetUser(c *gin.Context) {
	userID, ok := parseID(c, "id")
	if !ok {
		return
	}
	u, err := h.svc.GetUser(c.Request.Context(), userID)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, u)
}

type updateUserReq struct {
	Email    *string      `json:"email,omitempty" binding:"omitempty,email"`
	Role     *domain.Role `json:"role,omitempty" binding:"omitempty,oneof=admin user"`
	Disabled *bool        `json:"disabled,omitempty"`
}

func (h *AuthHandler) UpdateUser(c *gin.Context) {
	userID, ok := parseID(c, "id")
	if !ok {
		return
	}
	var req updateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondProblem(c, errs.InvalidInput(err.Error()))
		return
	}
	if req.Email == nil && req.Role == nil && req.Disabled == nil {
		RespondProblem(c, errs.InvalidInput("at least one of email, role, or disabled is required"))
		return
	}
	u, err := h.svc.UpdateUser(c.Request.Context(), userID, req.Email, req.Role, req.Disabled)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, u)
}

// DeactivateUser disables an account without deleting its documents, audit
// metadata, or API-key records. Existing queued work continues normally; all
// subsequent API-key authentication attempts are rejected.
func (h *AuthHandler) DeactivateUser(c *gin.Context) {
	userID, ok := parseID(c, "id")
	if !ok {
		return
	}
	if currentUserID, exists := c.Get(auditUserID); exists {
		if id, ok := currentUserID.(int64); ok && id == userID {
			RespondProblem(c, errs.InvalidInput("administrators cannot deactivate their own account"))
			return
		}
	}
	disabled := true
	u, err := h.svc.UpdateUser(c.Request.Context(), userID, nil, nil, &disabled)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, u)
}

func (h *AuthHandler) ReactivateUser(c *gin.Context) {
	userID, ok := parseID(c, "id")
	if !ok {
		return
	}
	disabled := false
	u, err := h.svc.UpdateUser(c.Request.Context(), userID, nil, nil, &disabled)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, u)
}

type resetPasswordReq struct {
	Password string `json:"password" binding:"required,min=8"`
}

func (h *AuthHandler) ResetPassword(c *gin.Context) {
	userID, ok := parseID(c, "id")
	if !ok {
		return
	}
	if sessionObj, exists := c.Get("admin.session"); exists {
		if s, ok := sessionObj.(*AdminSession); ok && s.Role != domain.RoleAdmin && s.UserID != userID {
			RespondProblem(c, errs.Forbidden("cannot reset password of another account"))
			return
		}
	}
	var req resetPasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondProblem(c, errs.InvalidInput(err.Error()))
		return
	}
	if err := h.svc.ResetPassword(c.Request.Context(), userID, req.Password); err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "password updated successfully"})
}

func (h *AuthHandler) GetAccountConfig(c *gin.Context) {
	userID, ok := parseID(c, "id")
	if !ok {
		return
	}
	cfg, err := h.svc.GetAccountConfig(c.Request.Context(), userID)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, cfg)
}

type updateConfigReq struct {
	RateLimitRPM      *int   `json:"rate_limit_rpm,omitempty" binding:"omitempty,gte=0"`
	DocQuota          *int64 `json:"doc_quota,omitempty" binding:"omitempty,gte=0"`
	StorageQuotaBytes *int64 `json:"storage_quota_bytes,omitempty" binding:"omitempty,gte=0"`
}

func (h *AuthHandler) UpdateAccountConfig(c *gin.Context) {
	userID, ok := parseID(c, "id")
	if !ok {
		return
	}
	var req updateConfigReq
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondProblem(c, errs.InvalidInput(err.Error()))
		return
	}
	if req.RateLimitRPM == nil && req.DocQuota == nil && req.StorageQuotaBytes == nil {
		RespondProblem(c, errs.InvalidInput("at least one of rate_limit_rpm, doc_quota, or storage_quota_bytes is required"))
		return
	}
	cfg, err := h.svc.UpdateAccountConfig(c.Request.Context(), userID, req.RateLimitRPM, req.DocQuota, nil, req.StorageQuotaBytes)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, cfg)
}

func (h *AuthHandler) ResetDocQuota(c *gin.Context) {
	userID, ok := parseID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.ResetDocQuota(c.Request.Context(), userID); err != nil {
		RespondError(c, err)
		return
	}
	cfg, err := h.svc.GetAccountConfig(c.Request.Context(), userID)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "quota reset successful",
		"config":  cfg,
	})
}

type createKeyReq struct {
	Name         string `json:"name" binding:"omitempty,max=100"`
	RateLimitRPM int    `json:"rate_limit_rpm" binding:"gte=0"`
}

func (h *AuthHandler) CreateAPIKey(c *gin.Context) {
	userID, ok := parseID(c, "id")
	if !ok {
		return
	}
	if sessionObj, exists := c.Get("admin.session"); exists {
		if s, ok := sessionObj.(*AdminSession); ok && s.Role != domain.RoleAdmin && s.UserID != userID {
			RespondProblem(c, errs.Forbidden("cannot create keys for another account"))
			return
		}
	}
	var req createKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondProblem(c, errs.InvalidInput(err.Error()))
		return
	}
	k, err := h.svc.GenerateKey(c.Request.Context(), userID, req.Name, req.RateLimitRPM)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, k)
}

func (h *AuthHandler) ListAPIKeys(c *gin.Context) {
	userID, ok := parseID(c, "id")
	if !ok {
		return
	}
	if sessionObj, exists := c.Get("admin.session"); exists {
		if s, ok := sessionObj.(*AdminSession); ok && s.Role != domain.RoleAdmin && s.UserID != userID {
			RespondProblem(c, errs.Forbidden("cannot view keys of another account"))
			return
		}
	}
	keys, err := h.svc.ListKeys(c.Request.Context(), userID)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user_id": userID, "api_keys": keys})
}

func (h *AuthHandler) RevokeAPIKey(c *gin.Context) {
	userID, ok := parseID(c, "id")
	if !ok {
		return
	}
	keyID, ok := parseID(c, "kid")
	if !ok {
		return
	}
	if sessionObj, exists := c.Get("admin.session"); exists {
		if s, ok := sessionObj.(*AdminSession); ok && s.Role != domain.RoleAdmin && s.UserID != userID {
			RespondProblem(c, errs.Forbidden("cannot revoke keys of another account"))
			return
		}
	}
	if err := h.svc.RevokeKey(c.Request.Context(), userID, keyID); err != nil {
		RespondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type updateKeyReq struct {
	RateLimitRPM *int `json:"rate_limit_rpm" binding:"required,gte=0"`
}

func (h *AuthHandler) UpdateAPIKey(c *gin.Context) {
	userID, ok := parseID(c, "id")
	if !ok {
		return
	}
	keyID, ok := parseID(c, "kid")
	if !ok {
		return
	}
	if sessionObj, exists := c.Get("admin.session"); exists {
		if s, ok := sessionObj.(*AdminSession); ok && s.Role != domain.RoleAdmin && s.UserID != userID {
			RespondProblem(c, errs.Forbidden("cannot update keys of another account"))
			return
		}
	}
	var req updateKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondProblem(c, errs.InvalidInput(err.Error()))
		return
	}
	k, err := h.svc.UpdateKeyRateLimit(c.Request.Context(), userID, keyID, *req.RateLimitRPM)
	if err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, k)
}

func (h *AuthHandler) RequireAPIKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := bearerToken(c)
		if err != nil {
			RespondProblem(c, errs.Unauthorized("missing or malformed Authorization header"))
			c.Abort()
			return
		}
		k, err := h.svc.Authenticate(c.Request.Context(), raw)
		if err != nil {
			RespondError(c, err)
			c.Abort()
			return
		}
		c.Set(ctxKey, k)
		c.Set(ctxAPIKeyRevalidator, func(ctx context.Context) error {
			_, err := h.svc.ValidateActive(ctx, raw)
			return err
		})
		c.Set(auditActor, "api_key")
		c.Set(auditUserID, k.UserID)
		c.Set(auditAPIKeyID, k.ID)
		c.Next()
	}
}

func revalidateAPIKey(c *gin.Context) error {
	v, ok := c.Get(ctxAPIKeyRevalidator)
	if !ok {
		return domain.ErrUnauthorized
	}
	revalidate, ok := v.(func(context.Context) error)
	if !ok {
		return domain.ErrUnauthorized
	}
	return revalidate(c.Request.Context())
}

func apiKeyFrom(c *gin.Context) (*domain.ApiKey, bool) {
	v, ok := c.Get(ctxKey)
	if !ok {
		return nil, false
	}
	k, ok := v.(*domain.ApiKey)
	return k, ok
}

func bearerToken(c *gin.Context) (string, error) {
	hdr := c.GetHeader("Authorization")
	if hdr == "" {
		return "", errors.New("missing Authorization header")
	}
	parts := strings.SplitN(hdr, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", errors.New("malformed Authorization header")
	}
	return strings.TrimSpace(parts[1]), nil
}

func parseID(c *gin.Context, param string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(param), 10, 64)
	if err != nil || id <= 0 {
		RespondProblem(c, errs.InvalidInput("invalid "+param+" parameter"))
		return 0, false
	}
	return id, true
}
