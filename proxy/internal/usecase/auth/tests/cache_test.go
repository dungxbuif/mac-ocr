package tests

import (
	"context"
	"errors"
	"testing"

	"macocr/proxy/domain"
	"macocr/proxy/internal/usecase/auth"
)

type runtimeCache struct {
	configs          map[int64]domain.AccountConfig
	keys             map[string]domain.ApiKey
	deletes          int
	keyInvalidations int
	keyDeleteErr     error
}

func (c *runtimeCache) GetAPIKey(_ context.Context, hash string) (*domain.ApiKey, error) {
	key, ok := c.keys[hash]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &key, nil
}
func (c *runtimeCache) SetAPIKey(_ context.Context, hash string, key *domain.ApiKey) error {
	c.keys[hash] = *key
	return nil
}
func (c *runtimeCache) DeleteAPIKeysByUser(_ context.Context, userID int64) error {
	if c.keyDeleteErr != nil {
		return c.keyDeleteErr
	}
	for hash, key := range c.keys {
		if key.UserID == userID {
			delete(c.keys, hash)
		}
	}
	c.keyInvalidations++
	return nil
}

func TestAPIKeyRevocationIsAuthoritativeWhenCacheInvalidationFails(t *testing.T) {
	ctx := context.Background()
	users, configs, keys := newMockUsers(), newMockConfigs(), newMockKeys()
	users.byID[9] = &domain.User{ID: 9, Email: "revoke@example.com"}
	configs.byUserID[9] = &domain.AccountConfig{UserID: 9, RateLimitRPM: 70}
	cache := &runtimeCache{
		configs:      make(map[int64]domain.AccountConfig),
		keys:         make(map[string]domain.ApiKey),
		keyDeleteErr: errors.New("redis unavailable"),
	}
	svc := auth.NewService(users, configs, keys, cache)

	generated, err := svc.GenerateKey(ctx, 9, "cached", 50)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if err := svc.RevokeKey(ctx, 9, generated.KeyID); err != nil {
		t.Fatalf("database revocation must succeed despite cache outage: %v", err)
	}
	if len(cache.keys) != 1 {
		t.Fatalf("test requires a stale positive cache entry")
	}
	if _, err := svc.Authenticate(ctx, generated.Key); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("stale cached key authenticated after authoritative revoke: %v", err)
	}
}

func (c *runtimeCache) Allow(context.Context, string, int) (bool, error) { return true, nil }
func (c *runtimeCache) GetAccountConfig(_ context.Context, userID int64) (*domain.AccountConfig, error) {
	cfg, ok := c.configs[userID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &cfg, nil
}
func (c *runtimeCache) SetAccountConfig(_ context.Context, cfg *domain.AccountConfig) error {
	c.configs[cfg.UserID] = *cfg
	return nil
}
func (c *runtimeCache) DeleteAccountConfig(_ context.Context, userID int64) error {
	delete(c.configs, userID)
	c.deletes++
	return nil
}

func TestAccountLimitsUseCacheAndQuotaWritesInvalidateIt(t *testing.T) {
	ctx := context.Background()
	users, configs := newMockUsers(), newMockConfigs()
	users.byID[9] = &domain.User{ID: 9, Email: "cache@example.com"}
	configs.byUserID[9] = &domain.AccountConfig{UserID: 9, RateLimitRPM: 70, DocQuota: 4}
	cache := &runtimeCache{configs: make(map[int64]domain.AccountConfig), keys: make(map[string]domain.ApiKey)}
	svc := auth.NewService(users, configs, newMockKeys(), cache)

	first, err := svc.GetAccountConfig(ctx, 9)
	if err != nil || first.RateLimitRPM != 70 {
		t.Fatalf("cache-aside read failed: cfg=%+v err=%v", first, err)
	}
	configs.byUserID[9].RateLimitRPM = 999
	second, err := svc.GetAccountConfig(ctx, 9)
	if err != nil || second.RateLimitRPM != 70 {
		t.Fatalf("expected cached limit, cfg=%+v err=%v", second, err)
	}
	if err := svc.ReserveDocQuota(ctx, 9, 1); err != nil {
		t.Fatalf("reserve quota: %v", err)
	}
	if cache.deletes != 1 {
		t.Fatalf("expected quota mutation to invalidate cached limits, deletes=%d", cache.deletes)
	}
}

func TestAPIKeyAuthenticationUsesCacheAndRevokeInvalidatesIt(t *testing.T) {
	ctx := context.Background()
	users, configs, keys := newMockUsers(), newMockConfigs(), newMockKeys()
	users.byID[9] = &domain.User{ID: 9, Email: "cache@example.com"}
	configs.byUserID[9] = &domain.AccountConfig{UserID: 9, RateLimitRPM: 70, DocQuota: 4}
	cache := &runtimeCache{configs: make(map[int64]domain.AccountConfig), keys: make(map[string]domain.ApiKey)}
	svc := auth.NewService(users, configs, keys, cache)

	generated, err := svc.GenerateKey(ctx, 9, "cached", 50)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if len(cache.keys) != 1 {
		t.Fatalf("generated API key was not cached")
	}
	if _, err := svc.Authenticate(ctx, generated.Key); err != nil {
		t.Fatalf("cached API key did not authenticate: %v", err)
	}
	if err := svc.RevokeKey(ctx, 9, generated.KeyID); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
	if cache.keyInvalidations != 1 || len(cache.keys) != 0 {
		t.Fatalf("revoke did not invalidate user key cache: invalidations=%d keys=%d", cache.keyInvalidations, len(cache.keys))
	}
	if _, err := svc.Authenticate(ctx, generated.Key); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revoked API key authenticated: %v", err)
	}
}

func TestAccountDeactivationInvalidatesCachedKeysAndBlocksAuthentication(t *testing.T) {
	ctx := context.Background()
	users, configs, keys := newMockUsers(), newMockConfigs(), newMockKeys()
	users.byID[9] = &domain.User{ID: 9, Email: "deactivate@example.com", Role: domain.RoleUser}
	configs.byUserID[9] = &domain.AccountConfig{UserID: 9, RateLimitRPM: 70, DocQuota: 4}
	cache := &runtimeCache{configs: make(map[int64]domain.AccountConfig), keys: make(map[string]domain.ApiKey)}
	svc := auth.NewService(users, configs, keys, cache)

	generated, err := svc.GenerateKey(ctx, 9, "deactivate", 50)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	disabled := true
	updated, err := svc.UpdateUser(ctx, 9, nil, nil, &disabled)
	if err != nil || !updated.Disabled {
		t.Fatalf("UpdateUser disabled: user=%+v err=%v", updated, err)
	}
	if cache.keyInvalidations != 1 || len(cache.keys) != 0 {
		t.Fatalf("deactivation did not invalidate key cache: invalidations=%d keys=%d", cache.keyInvalidations, len(cache.keys))
	}
	if _, err := svc.Authenticate(ctx, generated.Key); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("deactivated account authenticated: %v", err)
	}
}
