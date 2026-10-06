package tests

import (
	"context"
	"errors"
	"fmt"
	"macocr/proxy/domain"
	redisrepo "macocr/proxy/internal/repository/redis"
	"macocr/proxy/internal/usecase/auth"
	"os"
	"testing"
	"time"
)

func TestAuthenticationWithLiveRedisEnforcesKeyAndAggregateAccountLimits(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("set TEST_REDIS_URL for isolated Redis authentication test")
	}
	limiter, err := redisrepo.New(url)
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Close()
	ctx := context.Background()
	users := newMockUsers()
	users.seq = time.Now().UnixNano()
	keys := newMockKeys()
	keys.seq = users.seq
	configs := newMockConfigs()
	service := auth.NewService(users, configs, keys, limiter)
	rpm := 2
	user, err := service.CreateUser(ctx, fmt.Sprintf("rate-%d@example.test", users.seq), domain.RoleUser, "", &rpm, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := service.GenerateKey(ctx, user.ID, "key-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.GenerateKey(ctx, user.ID, "key-b", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, a.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, a.Key); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("key overflow accepted: %v", err)
	}
	if _, err := service.Authenticate(ctx, b.Key); err != nil {
		t.Fatalf("key rejection incorrectly consumed account limit: %v", err)
	}
	if _, err := service.Authenticate(ctx, b.Key); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("aggregate account overflow accepted: %v", err)
	}
	inherited, err := service.GenerateKey(ctx, user.ID, "inherits-account", 0)
	if err != nil || inherited.RateLimitRPM != 2 {
		t.Fatal("create-key zero does not inherit account limit")
	}
	if _, err := service.UpdateKeyRateLimit(ctx, user.ID, inherited.KeyID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, inherited.Key); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatal("disabled per-key limit bypassed aggregate account cap")
	}
	zero := 0
	if _, err := service.UpdateAccountConfig(ctx, user.ID, &zero, nil, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		if _, err := service.Authenticate(ctx, inherited.Key); err != nil {
			t.Fatalf("both limits zero should allow request %d: %v", i, err)
		}
	}
	if _, err := service.Authenticate(ctx, a.Key); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatal("disabling account cap incorrectly disabled per-key cap")
	}
	if _, err := service.UpdateKeyRateLimit(ctx, user.ID, inherited.KeyID, 1000); err != nil {
		t.Fatal(err)
	}
	rpm = 2
	if _, err := service.UpdateAccountConfig(ctx, user.ID, &rpm, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, inherited.Key); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatal("raised key limit bypassed aggregate account cap")
	}
}
