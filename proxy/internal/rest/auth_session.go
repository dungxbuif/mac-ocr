package rest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/errs"
	"macocr/proxy/internal/usecase/auth"
	"macocr/proxy/internal/usecase/document"
)

const (
	SessionCookieName  = "macocr_session"
	CSRFCookieName     = "macocr_csrf"
	SessionDuration    = 8 * time.Hour
	maxLoginBodyBytes  = 8 << 10
	loginAttemptLimit  = 5
	loginAttemptWindow = time.Minute
)

type loginAttempt struct {
	count       int
	windowStart time.Time
}

type loginAttemptLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
}

func newLoginAttemptLimiter() *loginAttemptLimiter {
	return &loginAttemptLimiter{attempts: make(map[string]loginAttempt)}
}

func (l *loginAttemptLimiter) allow(keys ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if len(l.attempts) >= 10_000 {
		for key, entry := range l.attempts {
			if now.Sub(entry.windowStart) >= loginAttemptWindow {
				delete(l.attempts, key)
			}
		}
	}
	allowed := true
	for _, key := range keys {
		entry, exists := l.attempts[key]
		if !exists && len(l.attempts) >= 10_000 {
			allowed = false
			continue
		}
		if entry.windowStart.IsZero() || now.Sub(entry.windowStart) >= loginAttemptWindow {
			entry = loginAttempt{windowStart: now}
		}
		entry.count++
		l.attempts[key] = entry
		if entry.count > loginAttemptLimit {
			allowed = false
		}
	}

	return allowed
}

type AdminSession struct {
	UserID    int64
	Email     string
	Role      domain.Role
	CSRFToken string
	ExpiresAt time.Time
}

type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*AdminSession
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*AdminSession),
	}
}

func (sm *SessionManager) Create(user *domain.User) (token string, csrfToken string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	tokenBytes := make([]byte, 32)
	csrfBytes := make([]byte, 16)
	_, _ = rand.Read(tokenBytes)
	_, _ = rand.Read(csrfBytes)

	token = hex.EncodeToString(tokenBytes)
	csrfToken = hex.EncodeToString(csrfBytes)

	sm.sessions[token] = &AdminSession{
		UserID:    user.ID,
		Email:     user.Email,
		Role:      user.Role,
		CSRFToken: csrfToken,
		ExpiresAt: time.Now().Add(SessionDuration),
	}
	return token, csrfToken
}

func (sm *SessionManager) Get(token string) (*AdminSession, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	s, ok := sm.sessions[token]
	if !ok {
		return nil, false
	}
	if time.Now().After(s.ExpiresAt) {
		return nil, false
	}
	return s, true
}

func (sm *SessionManager) Delete(token string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, token)
}

type AdminAuthHandler struct {
	users   domain.UserRepository
	docs    *document.Service
	sm      *SessionManager
	isHTTPS bool
	logins  *loginAttemptLimiter
}

func NewAdminAuthHandler(users domain.UserRepository, docs *document.Service, sm *SessionManager, isHTTPS bool) *AdminAuthHandler {
	return &AdminAuthHandler{
		users:   users,
		docs:    docs,
		sm:      sm,
		isHTTPS: isHTTPS,
		logins:  newLoginAttemptLimiter(),
	}
}

type loginReq struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func (h *AdminAuthHandler) Login(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxLoginBodyBytes)
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			RespondProblem(c, errs.New(errs.CodePayloadTooLarge, http.StatusRequestEntityTooLarge, "Login request is too large").WithLimit("maxRequestBytes", maxLoginBodyBytes))
			return
		}
		RespondProblem(c, errs.InvalidInput(err.Error()))
		return
	}

	emailDigest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(req.Email))))
	if !h.logins.allow("ip:"+remoteIP(c.Request), "email:"+hex.EncodeToString(emailDigest[:])) {
		c.Header("Retry-After", "60")
		RespondProblem(c, errs.New(errs.CodeRateLimited, http.StatusTooManyRequests, "Too many login attempts").WithDetail("try again later"))
		return
	}

	u, err := h.users.GetByEmail(c.Request.Context(), req.Email)
	if err != nil || u.Disabled || u.Role != domain.RoleAdmin {
		RespondProblem(c, errs.Unauthorized("invalid email or password"))
		return
	}

	match, err := auth.VerifyPassword(req.Password, u.PasswordHash)
	if err != nil || !match {
		RespondProblem(c, errs.Unauthorized("invalid email or password"))
		return
	}

	token, csrfToken := h.sm.Create(u)

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(SessionCookieName, token, int(SessionDuration.Seconds()), "/", "", h.isHTTPS, true)
	c.SetCookie(CSRFCookieName, csrfToken, int(SessionDuration.Seconds()), "/", "", h.isHTTPS, false)

	c.JSON(http.StatusOK, gin.H{
		"message":   "login successful",
		"csrfToken": csrfToken,
		"user": gin.H{
			"id":    u.ID,
			"email": u.Email,
			"role":  u.Role,
		},
	})
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (h *AdminAuthHandler) Logout(c *gin.Context) {
	if token, err := c.Cookie(SessionCookieName); err == nil {
		h.sm.Delete(token)
	}

	c.SetCookie(SessionCookieName, "", -1, "/", "", h.isHTTPS, true)
	c.SetCookie(CSRFCookieName, "", -1, "/", "", h.isHTTPS, false)

	c.JSON(http.StatusOK, gin.H{"message": "logged out successfully"})
}

func (h *AdminAuthHandler) Me(c *gin.Context) {
	s, ok := h.sessionFromContext(c)
	if !ok {
		RespondProblem(c, errs.Unauthorized("not authenticated"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"userId": s.UserID,
		"email":  s.Email,
		"role":   s.Role,
	})
}

func (h *AdminAuthHandler) DashboardStats(c *gin.Context) {
	_, ok := h.sessionFromContext(c)
	if !ok {
		RespondProblem(c, errs.Unauthorized("admin authentication required"))
		return
	}

	counts, _ := h.docs.CountStatus(c.Request.Context(), nil)
	users, _ := h.users.List(c.Request.Context(), 1000, 0)

	c.JSON(http.StatusOK, gin.H{
		"queueCounts": counts,
		"totalUsers":  len(users),
		"timestamp":   time.Now().UTC(),
	})
}

func (h *AdminAuthHandler) RequireAdminSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		session, ok := h.sessionFromContext(c)
		if !ok || session.Role != domain.RoleAdmin {
			RespondProblem(c, errs.Unauthorized("session invalid or account deactivated"))
			c.Abort()
			return
		}

		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead && c.Request.Method != http.MethodOptions {
			csrfHeader := c.GetHeader("X-CSRF-Token")
			if csrfHeader == "" || csrfHeader != session.CSRFToken {
				RespondProblem(c, errs.Forbidden("invalid or missing CSRF token"))
				c.Abort()
				return
			}
		}

		c.Set("admin.session", session)
		c.Set(auditActor, "admin")
		c.Set(auditUserID, session.UserID)
		c.Next()
	}
}

func (h *AdminAuthHandler) sessionFromContext(c *gin.Context) (*AdminSession, bool) {
	token, err := c.Cookie(SessionCookieName)
	if err != nil || token == "" {
		return nil, false
	}
	session, ok := h.sm.Get(token)
	if !ok {
		return nil, false
	}

	// Use the authoritative user row for every session-backed request. This
	// makes account deactivation and role changes effective without waiting for
	// the in-memory session TTL.
	user, err := h.users.GetByID(c.Request.Context(), session.UserID)
	if err != nil || user.Disabled || user.Role != domain.RoleAdmin {
		h.sm.Delete(token)
		return nil, false
	}
	session.Email = user.Email
	session.Role = user.Role
	return session, true
}
