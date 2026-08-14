package integration_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joho/godotenv"

	"macocr/proxy/domain"
	"macocr/proxy/internal/config"
	pgrepo "macocr/proxy/internal/repository/postgres"
	redisrepo "macocr/proxy/internal/repository/redis"
)

func TestPostgresAndRedisIntegration(t *testing.T) {
	if os.Getenv("TEST_DB_REDIS") != "1" {
		t.Skip("set TEST_DB_REDIS=1 to run against live PostgreSQL and Redis")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	loadRepoEnv(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	pgRepo, err := pgrepo.New(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	defer pgRepo.Close()
	if err := pgRepo.Ping(ctx); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}
	if err := pgRepo.Migrate(ctx); err != nil {
		t.Fatalf("migrate postgres: %v", err)
	}

	redisRepo, err := redisrepo.New(cfg.RedisURL)
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	defer redisRepo.Close()
	if err := redisRepo.Ping(ctx); err != nil {
		t.Fatalf("ping redis: %v", err)
	}

	suffix := time.Now().UTC().UnixNano()
	email := fmt.Sprintf("integration-%d@example.test", suffix)
	userRepo := pgrepo.NewUserRepository(pgRepo.Pool())
	configRepo := pgrepo.NewAccountConfigRepository(pgRepo.Pool())
	apiKeyRepo := pgrepo.NewAPIKeyRepository(pgRepo.Pool())

	user, err := userRepo.Create(ctx, &domain.User{
		Email:        email,
		PasswordHash: "integration-hash",
		Role:         domain.RoleUser,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = pgRepo.Pool().Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, user.ID)
	})

	defaultCfg, err := configRepo.GetByUserID(ctx, user.ID)
	if err != nil {
		t.Fatalf("get default account config: %v", err)
	}
	if defaultCfg.RateLimitRPM != 60 || defaultCfg.DocQuota != 0 {
		t.Fatalf("unexpected default account config: %+v", defaultCfg)
	}

	defaultCfg.RateLimitRPM = 7
	defaultCfg.DocQuota = 2
	updatedCfg, err := configRepo.Update(ctx, defaultCfg)
	if err != nil {
		t.Fatalf("update account config: %v", err)
	}
	if updatedCfg.RateLimitRPM != 7 || updatedCfg.DocQuota != 2 {
		t.Fatalf("unexpected updated account config: %+v", updatedCfg)
	}
	if err := configRepo.ReserveDocs(ctx, user.ID, 2); err != nil {
		t.Fatalf("reserve docs: %v", err)
	}
	if err := configRepo.ReserveDocs(ctx, user.ID, 1); err != domain.ErrQuotaExceeded {
		t.Fatalf("reserve over quota error = %v, want %v", err, domain.ErrQuotaExceeded)
	}
	if err := configRepo.RefundDocs(ctx, user.ID, 1); err != nil {
		t.Fatalf("refund docs: %v", err)
	}

	keyHash := fmt.Sprintf("integration-key-hash-%d", suffix)
	apiKey, err := apiKeyRepo.Create(ctx, &domain.ApiKey{
		UserID:       user.ID,
		Name:         "integration",
		Prefix:       "moc_test",
		KeyHash:      keyHash,
		RateLimitRPM: 3,
	})
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	fetchedKey, err := apiKeyRepo.GetByHash(ctx, keyHash)
	if err != nil {
		t.Fatalf("get api key by hash: %v", err)
	}
	if fetchedKey.ID != apiKey.ID || fetchedKey.UserID != user.ID {
		t.Fatalf("unexpected fetched api key: %+v", fetchedKey)
	}

	if err := redisRepo.SetAccountConfig(ctx, updatedCfg); err != nil {
		t.Fatalf("cache account config: %v", err)
	}
	cachedCfg, err := redisRepo.GetAccountConfig(ctx, user.ID)
	if err != nil {
		t.Fatalf("read cached account config: %v", err)
	}
	if cachedCfg.RateLimitRPM != updatedCfg.RateLimitRPM {
		t.Fatalf("cached account config mismatch: %+v", cachedCfg)
	}
	if err := redisRepo.DeleteAccountConfig(ctx, user.ID); err != nil {
		t.Fatalf("delete cached account config: %v", err)
	}

	if err := redisRepo.SetAPIKey(ctx, keyHash, apiKey); err != nil {
		t.Fatalf("cache api key: %v", err)
	}
	cachedKey, err := redisRepo.GetAPIKey(ctx, keyHash)
	if err != nil {
		t.Fatalf("read cached api key: %v", err)
	}
	if cachedKey.ID != apiKey.ID || cachedKey.UserID != user.ID {
		t.Fatalf("cached api key mismatch: %+v", cachedKey)
	}
	if err := redisRepo.DeleteAPIKeysByUser(ctx, user.ID); err != nil {
		t.Fatalf("delete cached api keys by user: %v", err)
	}

	resultID := fmt.Sprintf("integration-result-%d", suffix)
	result := &domain.OCRResult{Text: "integration ok", PageCount: 1}
	if err := redisRepo.SetResult(ctx, resultID, result, time.Minute); err != nil {
		t.Fatalf("cache OCR result: %v", err)
	}
	cachedResult, err := redisRepo.GetResult(ctx, resultID)
	if err != nil {
		t.Fatalf("read cached OCR result: %v", err)
	}
	if cachedResult.Text != result.Text || cachedResult.PageCount != result.PageCount {
		t.Fatalf("cached OCR result mismatch: %+v", cachedResult)
	}
	if err := redisRepo.DeleteResult(ctx, resultID); err != nil {
		t.Fatalf("delete cached OCR result: %v", err)
	}

	rateLimitID := fmt.Sprintf("integration-rate-%d", suffix)
	for i := 0; i < 3; i++ {
		allowed, err := redisRepo.Allow(ctx, rateLimitID, 2)
		if err != nil {
			t.Fatalf("rate limit allow call %d: %v", i+1, err)
		}
		if i < 2 && !allowed {
			t.Fatalf("rate limit call %d denied unexpectedly", i+1)
		}
		if i == 2 && allowed {
			t.Fatalf("rate limit call %d allowed unexpectedly", i+1)
		}
	}
}

func loadRepoEnv(t *testing.T) {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		envPath := filepath.Join(dir, ".env")
		if _, err := os.Stat(envPath); err == nil {
			if err := godotenv.Load(envPath); err != nil {
				t.Fatalf("load %s: %v", envPath, err)
			}
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find proxy .env from %s", dir)
		}
		dir = parent
	}
}
