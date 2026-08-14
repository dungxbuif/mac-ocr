package tests

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"macocr/proxy/domain"
	"macocr/proxy/internal/usecase/auth"
)

type mockUsers struct {
	byID    map[int64]*domain.User
	byEmail map[string]*domain.User
	seq     int64
}

func newMockUsers() *mockUsers {
	return &mockUsers{
		byID:    make(map[int64]*domain.User),
		byEmail: make(map[string]*domain.User),
	}
}

func (m *mockUsers) Create(_ context.Context, u *domain.User) (*domain.User, error) {
	if _, exists := m.byEmail[u.Email]; exists {
		return nil, domain.ErrConflict
	}
	m.seq++
	saved := &domain.User{
		ID:           m.seq,
		Email:        u.Email,
		Role:         u.Role,
		PasswordHash: u.PasswordHash,
		Disabled:     u.Disabled,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	m.byID[saved.ID] = saved
	m.byEmail[saved.Email] = saved
	return saved, nil
}

func (m *mockUsers) GetByID(_ context.Context, id int64) (*domain.User, error) {
	if u, ok := m.byID[id]; ok {
		cpy := *u
		return &cpy, nil
	}
	return nil, domain.ErrNotFound
}

func (m *mockUsers) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	if u, ok := m.byEmail[email]; ok {
		cpy := *u
		return &cpy, nil
	}
	return nil, domain.ErrNotFound
}

func (m *mockUsers) List(_ context.Context, limit, offset int) ([]domain.User, error) {
	var list []domain.User
	var count int
	for _, u := range m.byID {
		if count >= offset && len(list) < limit {
			list = append(list, *u)
		}
		count++
	}
	return list, nil
}

func (m *mockUsers) Update(_ context.Context, u *domain.User) (*domain.User, error) {
	existing, ok := m.byID[u.ID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	existing.Email = u.Email
	existing.Role = u.Role
	existing.Disabled = u.Disabled
	existing.UpdatedAt = time.Now()
	cpy := *existing
	return &cpy, nil
}

type mockConfigs struct {
	byUserID map[int64]*domain.AccountConfig
}

func newMockConfigs() *mockConfigs {
	return &mockConfigs{
		byUserID: make(map[int64]*domain.AccountConfig),
	}
}

func (m *mockConfigs) GetByUserID(_ context.Context, userID int64) (*domain.AccountConfig, error) {
	if cfg, ok := m.byUserID[userID]; ok {
		cpy := *cfg
		return &cpy, nil
	}
	// default config mock
	defaultCfg := &domain.AccountConfig{
		UserID:       userID,
		RateLimitRPM: 60,
		DocQuota:     0,
		DocUsed:      0,
		UpdatedAt:    time.Now(),
	}
	m.byUserID[userID] = defaultCfg
	cpy := *defaultCfg
	return &cpy, nil
}

func (m *mockConfigs) Update(_ context.Context, cfg *domain.AccountConfig) (*domain.AccountConfig, error) {
	m.byUserID[cfg.UserID] = cfg
	cpy := *cfg
	return &cpy, nil
}

func (m *mockConfigs) ReserveDocs(_ context.Context, userID int64, count int64) error {
	cfg, ok := m.byUserID[userID]
	if !ok {
		return domain.ErrNotFound
	}
	if cfg.DocQuota > 0 && cfg.DocUsed+count > cfg.DocQuota {
		return domain.ErrQuotaExceeded
	}
	cfg.DocUsed += count
	return nil
}

func (m *mockConfigs) RefundDocs(_ context.Context, userID int64, count int64) error {
	cfg, ok := m.byUserID[userID]
	if !ok {
		return domain.ErrNotFound
	}
	cfg.DocUsed -= count
	if cfg.DocUsed < 0 {
		cfg.DocUsed = 0
	}
	return nil
}

func (m *mockConfigs) ResetDocUsed(_ context.Context, userID int64) error {
	cfg, ok := m.byUserID[userID]
	if !ok {
		return domain.ErrNotFound
	}
	cfg.DocUsed = 0
	return nil
}

type mockKeys struct {
	byHash map[string]*domain.ApiKey
	seq    int64
}

func newMockKeys() *mockKeys {
	return &mockKeys{byHash: make(map[string]*domain.ApiKey)}
}

func (m *mockKeys) Create(_ context.Context, k *domain.ApiKey) (*domain.ApiKey, error) {
	m.seq++
	saved := &domain.ApiKey{
		ID:           m.seq,
		UserID:       k.UserID,
		Name:         k.Name,
		Prefix:       k.Prefix,
		KeyHash:      k.KeyHash,
		RateLimitRPM: k.RateLimitRPM,
		CreatedAt:    time.Now(),
	}
	m.byHash[k.KeyHash] = saved
	return saved, nil
}

func (m *mockKeys) ListByUser(_ context.Context, uid int64) ([]domain.ApiKey, error) {
	out := []domain.ApiKey{}
	for _, k := range m.byHash {
		if k.UserID == uid {
			out = append(out, *k)
		}
	}
	return out, nil
}

func (m *mockKeys) GetByHash(_ context.Context, h string) (*domain.ApiKey, error) {
	if k, ok := m.byHash[h]; ok {
		return k, nil
	}
	return nil, domain.ErrNotFound
}

func (m *mockKeys) Revoke(_ context.Context, id int64) error {
	for _, k := range m.byHash {
		if k.ID == id {
			now := time.Now()
			k.RevokedAt = &now
			return nil
		}
	}
	return domain.ErrNotFound
}

type mockRL struct {
	allowedIDs map[string]bool
}

func (m *mockRL) Allow(_ context.Context, id string, _ int) (bool, error) {
	if m.allowedIDs != nil {
		if allow, exists := m.allowedIDs[id]; exists {
			return allow, nil
		}
	}
	return true, nil
}

func setupFixture() (*auth.Service, *mockUsers, *mockConfigs, *mockKeys, *mockRL) {
	users := newMockUsers()
	configs := newMockConfigs()
	keys := newMockKeys()
	rl := &mockRL{}
	svc := auth.NewService(users, configs, keys, rl)
	return svc, users, configs, keys, rl
}

func TestUserManagementAndConfig(t *testing.T) {
	svc, _, _, _, _ := setupFixture()
	ctx := context.Background()

	// 1. Create user with custom limits
	initialRPM := 120
	initialQuota := int64(500)
	u, err := svc.CreateUser(ctx, "test@example.com", domain.RoleUser, "", &initialRPM, &initialQuota)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	if u.Email != "test@example.com" || u.Role != domain.RoleUser {
		t.Fatalf("unexpected user: %+v", u)
	}
	if u.Config == nil || u.Config.RateLimitRPM != 120 || u.Config.DocQuota != 500 {
		t.Fatalf("unexpected config on created user: %+v", u.Config)
	}

	// 2. Get User
	gotUser, err := svc.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUser failed: %v", err)
	}
	if gotUser.ID != u.ID || gotUser.Config == nil || gotUser.Config.RateLimitRPM != 120 {
		t.Fatalf("unexpected got user: %+v", gotUser)
	}

	// 3. Update User status (disable)
	disabled := true
	updatedUser, err := svc.UpdateUser(ctx, u.ID, nil, nil, &disabled)
	if err != nil {
		t.Fatalf("UpdateUser failed: %v", err)
	}
	if !updatedUser.Disabled {
		t.Fatalf("user should be disabled")
	}

	// Cannot generate keys when disabled
	_, err = svc.GenerateKey(ctx, u.ID, "key1", 60)
	if err != domain.ErrUserDisabled {
		t.Fatalf("expected ErrUserDisabled, got %v", err)
	}

	// Enable user back
	disabled = false
	_, err = svc.UpdateUser(ctx, u.ID, nil, nil, &disabled)
	if err != nil {
		t.Fatalf("UpdateUser enable failed: %v", err)
	}

	// 4. Update Account Config
	newRPM := 200
	newQuota := int64(1000)
	updatedCfg, err := svc.UpdateAccountConfig(ctx, u.ID, &newRPM, &newQuota, nil)
	if err != nil {
		t.Fatalf("UpdateAccountConfig failed: %v", err)
	}
	if updatedCfg.RateLimitRPM != 200 || updatedCfg.DocQuota != 1000 {
		t.Fatalf("unexpected updated config: %+v", updatedCfg)
	}

	// 5. Doc Quota Reservation & Refund & Reset
	err = svc.ReserveDocQuota(ctx, u.ID, 600)
	if err != nil {
		t.Fatalf("ReserveDocQuota failed: %v", err)
	}
	// Exceed quota: 600 + 500 > 1000
	err = svc.ReserveDocQuota(ctx, u.ID, 500)
	if err != domain.ErrQuotaExceeded {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
	// Refund 200 -> used = 400
	err = svc.RefundDocQuota(ctx, u.ID, 200)
	if err != nil {
		t.Fatalf("RefundDocQuota failed: %v", err)
	}
	// Now reserve 500 -> 400 + 500 = 900 <= 1000 -> succeeds
	err = svc.ReserveDocQuota(ctx, u.ID, 500)
	if err != nil {
		t.Fatalf("ReserveDocQuota after refund failed: %v", err)
	}
	// Reset quota -> used = 0
	err = svc.ResetDocQuota(ctx, u.ID)
	if err != nil {
		t.Fatalf("ResetDocQuota failed: %v", err)
	}
	cfgAfterReset, _ := svc.GetAccountConfig(ctx, u.ID)
	if cfgAfterReset.DocUsed != 0 {
		t.Fatalf("expected DocUsed=0, got %d", cfgAfterReset.DocUsed)
	}
}

func TestGenerateAndAuthenticateKeys(t *testing.T) {
	svc, _, _, keys, rl := setupFixture()
	ctx := context.Background()

	rpm := 100
	u, err := svc.CreateUser(ctx, "dev@example.com", domain.RoleUser, "", &rpm, nil)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// 1. Generate Key clamped to user rate limit
	genKey, err := svc.GenerateKey(ctx, u.ID, "dev-key", 200) // requests 200, but user max is 100
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	if genKey.RateLimitRPM != 100 {
		t.Fatalf("expected rate limit clamped to 100, got %d", genKey.RateLimitRPM)
	}
	if !strings.HasPrefix(genKey.Key, "sk_ocr_") || !strings.HasPrefix(genKey.Prefix, "sk_ocr_") {
		t.Fatalf("unexpected API key format: key=%q prefix=%q", genKey.Key, genKey.Prefix)
	}

	// 2. Authenticate
	authKey, err := svc.Authenticate(ctx, genKey.Key)
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if authKey.ID != genKey.KeyID {
		t.Fatalf("key mismatch: %d vs %d", authKey.ID, genKey.KeyID)
	}

	// 3. Key rate limited
	rl.allowedIDs = map[string]bool{fmt.Sprintf("key:%d", genKey.KeyID): false}
	_, err = svc.Authenticate(ctx, genKey.Key)
	if err != domain.ErrRateLimited {
		t.Fatalf("expected ErrRateLimited on key limit, got %v", err)
	}

	// 4. User rate limited
	rl.allowedIDs = map[string]bool{fmt.Sprintf("user:%d", u.ID): false}
	_, err = svc.Authenticate(ctx, genKey.Key)
	if err != domain.ErrRateLimited {
		t.Fatalf("expected ErrRateLimited on user limit, got %v", err)
	}

	// 5. Revoke key
	rl.allowedIDs = nil
	_ = keys.Revoke(ctx, genKey.KeyID)
	_, err = svc.Authenticate(ctx, genKey.Key)
	if err != domain.ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized after revocation, got %v", err)
	}
}
