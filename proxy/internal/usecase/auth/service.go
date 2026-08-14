package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"macocr/proxy/domain"
)

type RateLimiter interface {
	Allow(ctx context.Context, id string, limit int) (bool, error)
}

type accountConfigCache interface {
	GetAccountConfig(ctx context.Context, userID int64) (*domain.AccountConfig, error)
	SetAccountConfig(ctx context.Context, cfg *domain.AccountConfig) error
	DeleteAccountConfig(ctx context.Context, userID int64) error
}

type apiKeyCache interface {
	GetAPIKey(ctx context.Context, hash string) (*domain.ApiKey, error)
	SetAPIKey(ctx context.Context, hash string, key *domain.ApiKey) error
	DeleteAPIKeysByUser(ctx context.Context, userID int64) error
}

type Service struct {
	users    domain.UserRepository
	configs  domain.AccountConfigRepository
	keys     domain.ApiKeyRepository
	rl       RateLimiter
	cache    accountConfigCache
	keyCache apiKeyCache
}

func NewService(
	users domain.UserRepository,
	configs domain.AccountConfigRepository,
	keys domain.ApiKeyRepository,
	rl RateLimiter,
) *Service {
	svc := &Service{
		users:   users,
		configs: configs,
		keys:    keys,
		rl:      rl,
	}
	if cache, ok := rl.(accountConfigCache); ok {
		svc.cache = cache
	}
	if cache, ok := rl.(apiKeyCache); ok {
		svc.keyCache = cache
	}
	return svc
}

func (s *Service) CreateUser(
	ctx context.Context,
	email string,
	role domain.Role,
	password string,
	initialRateLimit *int,
	initialDocQuota *int64,
	initialStorageQuotaBytes ...*int64,
) (*domain.User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return nil, domain.ErrBadParamInput
	}
	if role == "" {
		role = domain.RoleUser
	}

	u := &domain.User{
		Email:        email,
		Role:         role,
		PasswordHash: password,
		Disabled:     false,
	}

	created, err := s.users.Create(ctx, u)
	if err != nil {
		return nil, err
	}

	var storageQuota *int64
	if len(initialStorageQuotaBytes) > 0 {
		storageQuota = initialStorageQuotaBytes[0]
	}
	if initialRateLimit != nil || initialDocQuota != nil || storageQuota != nil {
		cfg, err := s.configs.GetByUserID(ctx, created.ID)
		if err == nil && cfg != nil {
			if initialRateLimit != nil && *initialRateLimit >= 0 {
				cfg.RateLimitRPM = *initialRateLimit
			}
			if initialDocQuota != nil && *initialDocQuota >= 0 {
				cfg.DocQuota = *initialDocQuota
			}
			if storageQuota != nil && *storageQuota >= 0 {
				cfg.StorageQuotaBytes = *storageQuota
			}
			updatedCfg, updateErr := s.configs.Update(ctx, cfg)
			if updateErr == nil {
				s.cacheConfig(ctx, updatedCfg)
				created.Config = updatedCfg
				return created, nil
			}
		}
	}

	if cfg, err := s.getConfig(ctx, created.ID); err == nil {
		created.Config = cfg
	}

	return created, nil
}

func (s *Service) GetUser(ctx context.Context, id int64) (*domain.User, error) {
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if cfg, err := s.getConfig(ctx, id); err == nil {
		u.Config = cfg
	}
	return u, nil
}

func (s *Service) ListUsers(ctx context.Context, limit, offset int) ([]domain.User, error) {
	users, err := s.users.List(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range users {
		if cfg, err := s.getConfig(ctx, users[i].ID); err == nil {
			users[i].Config = cfg
		}
	}
	return users, nil
}

func (s *Service) UpdateUser(ctx context.Context, id int64, email *string, role *domain.Role, disabled *bool) (*domain.User, error) {
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if email != nil && strings.TrimSpace(*email) != "" {
		u.Email = strings.TrimSpace(strings.ToLower(*email))
	}
	if role != nil && (*role == domain.RoleAdmin || *role == domain.RoleUser) {
		u.Role = *role
	}
	if disabled != nil {
		u.Disabled = *disabled
	}

	updated, err := s.users.Update(ctx, u)
	if err != nil {
		return nil, err
	}
	// API-key entries only cache credential metadata, but eagerly evict them
	// whenever account state changes so a later authentication cannot reuse
	// stale account-associated data. Authentication still verifies the user row
	// on every request, so a cache outage cannot delay deactivation.
	if disabled != nil && s.keyCache != nil {
		_ = s.keyCache.DeleteAPIKeysByUser(ctx, id)
	}
	if cfg, err := s.getConfig(ctx, id); err == nil {
		updated.Config = cfg
	}
	return updated, nil
}

func (s *Service) GetAccountConfig(ctx context.Context, userID int64) (*domain.AccountConfig, error) {
	return s.getConfig(ctx, userID)
}

func (s *Service) UpdateAccountConfig(
	ctx context.Context,
	userID int64,
	rateLimitRPM *int,
	docQuota *int64,
	adminID *int64,
	storageQuotaBytes ...*int64,
) (*domain.AccountConfig, error) {
	cfg, err := s.getConfig(ctx, userID)
	if err != nil {
		return nil, err
	}

	if rateLimitRPM != nil {
		if *rateLimitRPM < 0 {
			return nil, domain.ErrBadParamInput
		}
		cfg.RateLimitRPM = *rateLimitRPM
	}
	if docQuota != nil {
		if *docQuota < 0 {
			return nil, domain.ErrBadParamInput
		}
		cfg.DocQuota = *docQuota
	}
	if len(storageQuotaBytes) > 0 && storageQuotaBytes[0] != nil {
		if *storageQuotaBytes[0] < 0 {
			return nil, domain.ErrBadParamInput
		}
		if *storageQuotaBytes[0] > 0 && *storageQuotaBytes[0] < cfg.StorageUsedBytes+cfg.StorageReservedBytes {
			return nil, domain.ErrStorageQuotaExceeded
		}
		cfg.StorageQuotaBytes = *storageQuotaBytes[0]
	}
	cfg.UpdatedBy = adminID

	updated, err := s.configs.Update(ctx, cfg)
	if err == nil {
		s.cacheConfig(ctx, updated)
	}
	return updated, err
}

func (s *Service) ResetDocQuota(ctx context.Context, userID int64) error {
	err := s.configs.ResetDocUsed(ctx, userID)
	if err == nil {
		s.invalidateConfig(ctx, userID)
	}
	return err
}

func (s *Service) ReserveDocQuota(ctx context.Context, userID int64, count int64) error {
	err := s.configs.ReserveDocs(ctx, userID, count)
	if err == nil {
		s.invalidateConfig(ctx, userID)
	}
	return err
}

func (s *Service) RefundDocQuota(ctx context.Context, userID int64, count int64) error {
	err := s.configs.RefundDocs(ctx, userID, count)
	if err == nil {
		s.invalidateConfig(ctx, userID)
	}
	return err
}

type GeneratedKey struct {
	Key          string `json:"key"`
	KeyID        int64  `json:"key_id"`
	UserID       int64  `json:"user_id"`
	Name         string `json:"name"`
	Prefix       string `json:"prefix"`
	RateLimitRPM int    `json:"rate_limit_rpm"`
}

func (s *Service) GenerateKey(ctx context.Context, userID int64, name string, rateLimitRPM int) (*GeneratedKey, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.Disabled {
		return nil, domain.ErrUserDisabled
	}

	userCfg, err := s.getConfig(ctx, userID)
	if err != nil {
		return nil, err
	}

	if rateLimitRPM <= 0 || (userCfg.RateLimitRPM > 0 && rateLimitRPM > userCfg.RateLimitRPM) {
		rateLimitRPM = userCfg.RateLimitRPM
	}
	if strings.TrimSpace(name) == "" {
		name = "default"
	}

	raw, err := newRawKey()
	if err != nil {
		return nil, err
	}
	prefix := raw[:15]

	k, err := s.keys.Create(ctx, &domain.ApiKey{
		UserID:       userID,
		Name:         name,
		Prefix:       prefix,
		KeyHash:      hashKey(raw),
		RateLimitRPM: rateLimitRPM,
	})
	if err != nil {
		return nil, err
	}
	_ = s.cacheAPIKey(ctx, hashKey(raw), k)

	return &GeneratedKey{
		Key:          raw,
		KeyID:        k.ID,
		UserID:       k.UserID,
		Name:         k.Name,
		Prefix:       k.Prefix,
		RateLimitRPM: k.RateLimitRPM,
	}, nil
}

func (s *Service) ListKeys(ctx context.Context, userID int64) ([]domain.ApiKey, error) {
	return s.keys.ListByUser(ctx, userID)
}

func (s *Service) RevokeKey(ctx context.Context, userID, keyID int64) error {
	keys, err := s.keys.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if k.ID == keyID {
			if err := s.keys.Revoke(ctx, keyID); err != nil {
				return err
			}
			if s.keyCache != nil {
				// PostgreSQL is authoritative. A cache outage must not make a
				// successful revocation appear to have failed or remain usable.
				_ = s.keyCache.DeleteAPIKeysByUser(ctx, userID)
			}
			return nil
		}
	}
	return domain.ErrNotFound
}

func (s *Service) Authenticate(ctx context.Context, raw string) (*domain.ApiKey, error) {
	k, err := s.ValidateActive(ctx, raw)
	if err != nil {
		return nil, err
	}

	if k.RateLimitRPM > 0 && s.rl != nil {
		allowed, err := s.rl.Allow(ctx, fmt.Sprintf("key:%d", k.ID), k.RateLimitRPM)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, domain.ErrRateLimited
		}
	}

	userCfg, err := s.getConfig(ctx, k.UserID)
	if err != nil {
		// Limits are authorization policy. If they cannot be loaded, fail
		// closed instead of accidentally granting an unlimited request.
		return nil, err
	}
	if userCfg != nil && userCfg.RateLimitRPM > 0 && s.rl != nil {
		allowed, err := s.rl.Allow(ctx, fmt.Sprintf("user:%d", k.UserID), userCfg.RateLimitRPM)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, domain.ErrRateLimited
		}
	}

	return k, nil
}

// ValidateActive performs the authoritative credential and account-state
// checks without consuming a rate-limit unit. Long-lived streams use it to
// make revocation and account deactivation effective after connection setup.
func (s *Service) ValidateActive(ctx context.Context, raw string) (*domain.ApiKey, error) {
	if len(raw) != len("sk_ocr_")+48 || !strings.HasPrefix(raw, "sk_ocr_") {
		return nil, domain.ErrUnauthorized
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(raw, "sk_ocr_")); err != nil {
		return nil, domain.ErrUnauthorized
	}
	hash := hashKey(raw)
	// Always read the authoritative key row. A positive credential cache can
	// otherwise outlive a revoke when Redis invalidation is unavailable.
	k, err := s.keys.GetByHash(ctx, hash)
	if err != nil {
		return nil, domain.ErrUnauthorized
	}
	if k.RevokedAt != nil {
		return nil, domain.ErrUnauthorized
	}

	u, err := s.users.GetByID(ctx, k.UserID)
	if err != nil || u.Disabled {
		return nil, domain.ErrUnauthorized
	}
	_ = s.cacheAPIKey(ctx, hash, k)
	return k, nil
}

func (s *Service) cacheAPIKey(ctx context.Context, hash string, key *domain.ApiKey) error {
	if s.keyCache == nil {
		return nil
	}
	return s.keyCache.SetAPIKey(ctx, hash, key)
}

func (s *Service) getConfig(ctx context.Context, userID int64) (*domain.AccountConfig, error) {
	if s.cache != nil {
		if cfg, err := s.cache.GetAccountConfig(ctx, userID); err == nil {
			return cfg, nil
		}
	}
	cfg, err := s.configs.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	s.cacheConfig(ctx, cfg)
	return cfg, nil
}

func (s *Service) cacheConfig(ctx context.Context, cfg *domain.AccountConfig) {
	if s.cache != nil && cfg != nil {
		_ = s.cache.SetAccountConfig(ctx, cfg)
	}
}

func (s *Service) invalidateConfig(ctx context.Context, userID int64) {
	if s.cache != nil {
		_ = s.cache.DeleteAccountConfig(ctx, userID)
	}
}

// InvalidateAccountConfig refreshes cache-aside quota/config reads after a
// transaction owned by another repository changes account_configs.
func (s *Service) InvalidateAccountConfig(ctx context.Context, userID int64) {
	s.invalidateConfig(ctx, userID)
}

func newRawKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return "sk_ocr_" + hex.EncodeToString(b), nil
}

func hashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
