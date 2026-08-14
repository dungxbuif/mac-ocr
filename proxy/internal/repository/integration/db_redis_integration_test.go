package integration_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/joho/godotenv"

	"macocr/proxy/domain"
	"macocr/proxy/internal/config"
	"macocr/proxy/internal/notifications"
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
	cipher, err := notifications.NewSecretCipher("integration-document-retention-key")
	if err != nil {
		t.Fatalf("create notification cipher: %v", err)
	}
	docRepo := pgrepo.NewDocumentRepository(pgRepo.Pool(), cipher)

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

	updatedCfg.StorageQuotaBytes = 100
	updatedCfg, err = configRepo.Update(ctx, updatedCfg)
	if err != nil {
		t.Fatalf("set storage quota: %v", err)
	}
	var wg sync.WaitGroup
	reservationErrors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			reservationErrors <- configRepo.ReserveUpload(ctx, domain.UploadReservation{
				ObjectKey: fmt.Sprintf("uploads/%d/race-%d", user.ID, index), UserID: user.ID, SizeBytes: 60, ExpiresAt: time.Now().Add(time.Hour),
			})
		}(i)
	}
	wg.Wait()
	close(reservationErrors)
	succeeded, quotaRejected := 0, 0
	for reservationErr := range reservationErrors {
		if reservationErr == nil {
			succeeded++
		} else if reservationErr == domain.ErrStorageQuotaExceeded {
			quotaRejected++
		} else {
			t.Fatalf("unexpected concurrent reservation error: %v", reservationErr)
		}
	}
	if succeeded != 1 || quotaRejected != 1 {
		t.Fatalf("atomic byte reservation results success=%d rejected=%d", succeeded, quotaRejected)
	}
	for i := 0; i < 2; i++ {
		_, _ = configRepo.ReleaseUpload(ctx, user.ID, fmt.Sprintf("uploads/%d/race-%d", user.ID, i))
	}
	expiredKey := fmt.Sprintf("uploads/%d/expired", user.ID)
	if err := configRepo.ReserveUpload(ctx, domain.UploadReservation{ObjectKey: expiredKey, UserID: user.ID, SizeBytes: 20, ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatalf("reserve expired upload fixture: %v", err)
	}
	expiredReservations, err := configRepo.ListExpiredUploads(ctx, time.Now(), 100)
	if err != nil {
		t.Fatalf("list expired upload reservations: %v", err)
	}
	foundExpired := false
	for _, reservation := range expiredReservations {
		if reservation.ObjectKey == expiredKey {
			foundExpired = true
		}
	}
	if !foundExpired {
		t.Fatal("expired upload reservation was not selected for cleanup")
	}
	if released, err := configRepo.ReleaseUpload(ctx, user.ID, expiredKey); err != nil || !released {
		t.Fatalf("release expired upload reservation: %v", err)
	}

	reservedKey := fmt.Sprintf("uploads/%d/consume", user.ID)
	if err := configRepo.ReserveUpload(ctx, domain.UploadReservation{ObjectKey: reservedKey, UserID: user.ID, SizeBytes: 60, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("reserve upload for consumption: %v", err)
	}
	storageDocID := fmt.Sprintf("doc_storage_%d", suffix)
	createdStorageDoc, err := docRepo.CreateWithQuota(ctx, &domain.Document{
		ID: storageDocID, UserID: user.ID, Status: domain.StatusQueued, InputKey: reservedKey,
		InputSHA256: "abc", InputContentType: "application/pdf", InputSizeBytes: 60,
	})
	if err != nil {
		t.Fatalf("consume upload reservation with document: %v", err)
	}
	if createdStorageDoc.ID != storageDocID {
		t.Fatalf("unexpected storage document: %+v", createdStorageDoc)
	}
	storageCfg, err := configRepo.GetByUserID(ctx, user.ID)
	if err != nil || storageCfg.StorageUsedBytes != 60 || storageCfg.StorageReservedBytes != 0 {
		t.Fatalf("unexpected consumed byte counters cfg=%+v err=%v", storageCfg, err)
	}
	if _, err := pgRepo.Pool().Exec(ctx, `UPDATE documents SET status='completed' WHERE id=$1`, storageDocID); err != nil {
		t.Fatalf("complete storage document: %v", err)
	}
	if err := docRepo.MarkInputExpired(ctx, storageDocID); err != nil {
		t.Fatalf("expire storage document input: %v", err)
	}
	storageCfg, err = configRepo.GetByUserID(ctx, user.ID)
	if err != nil || storageCfg.StorageUsedBytes != 0 || storageCfg.StorageReservedBytes != 0 {
		t.Fatalf("input expiry did not release byte quota cfg=%+v err=%v", storageCfg, err)
	}

	capacityUser, err := userRepo.Create(ctx, &domain.User{
		Email: fmt.Sprintf("capacity-%d@example.test", suffix), PasswordHash: "integration-hash", Role: domain.RoleUser,
	})
	if err != nil {
		t.Fatalf("create weighted-capacity user: %v", err)
	}
	if _, err := pgRepo.Pool().Exec(ctx, `DELETE FROM documents WHERE id LIKE 'doc_capacity_%'`); err != nil {
		t.Fatalf("cleanup stale weighted-capacity fixtures: %v", err)
	}
	pdfQueueID := fmt.Sprintf("doc_capacity_pdf_%d", suffix)
	imageQueueID := fmt.Sprintf("doc_capacity_image_%d", suffix)
	for _, fixture := range []struct {
		id, contentType string
		createdAt       time.Time
	}{
		{id: pdfQueueID, contentType: "application/pdf", createdAt: time.Now().Add(-time.Minute)},
		{id: imageQueueID, contentType: "image/png", createdAt: time.Now()},
	} {
		if _, err := pgRepo.Pool().Exec(ctx, `INSERT INTO documents
			(id, user_id, status, input_key, input_content_type, input_size_bytes, created_at)
			VALUES ($1,$2,'queued',$3,$4,1,$5)`, fixture.id, capacityUser.ID, "inputs/"+fixture.id, fixture.contentType, fixture.createdAt); err != nil {
			t.Fatalf("insert weighted-capacity fixture: %v", err)
		}
	}
	claimed, err := docRepo.ClaimNextWithinCapacity(ctx, fmt.Sprintf("att_capacity_%d", suffix), time.Minute, 3, 1, 1, 3)
	if err != nil || claimed == nil || claimed.ID != imageQueueID {
		t.Fatalf("capacity-aware claim should skip older PDF and select image: doc=%+v err=%v", claimed, err)
	}
	if _, err := pgRepo.Pool().Exec(ctx, `DELETE FROM users WHERE id=$1`, capacityUser.ID); err != nil {
		t.Fatalf("cleanup weighted-capacity user: %v", err)
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

	retentionID := fmt.Sprintf("doc_retention_%d", suffix)
	freshID := fmt.Sprintf("doc_fresh_%d", suffix)
	queuedID := fmt.Sprintf("doc_queued_%d", suffix)
	old := time.Now().Add(-48 * time.Hour)
	for _, row := range []struct {
		id        string
		status    domain.DocumentStatus
		updatedAt time.Time
	}{
		{id: retentionID, status: domain.StatusCompleted, updatedAt: old},
		{id: freshID, status: domain.StatusCompleted, updatedAt: time.Now()},
		{id: queuedID, status: domain.StatusQueued, updatedAt: old},
	} {
		if _, err := pgRepo.Pool().Exec(ctx, `INSERT INTO documents
			(id, user_id, status, input_key, input_content_type, result_key, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			row.id, user.ID, row.status, "inputs/"+row.id, "image/png", "results/"+row.id, row.updatedAt); err != nil {
			t.Fatalf("insert retention fixture %s: %v", row.id, err)
		}
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	expiredDocs, err := docRepo.ListExpiredDocuments(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("list expired documents: %v", err)
	}
	foundRetention := false
	for _, doc := range expiredDocs {
		if doc.ID == retentionID {
			foundRetention = doc.InputKey != "" && doc.ResultKey != ""
		}
		if doc.ID == freshID || doc.ID == queuedID {
			t.Fatalf("non-expired document selected for deletion: %s", doc.ID)
		}
	}
	if !foundRetention {
		t.Fatal("expired terminal document was not selected with its object keys")
	}
	referenced, err := docRepo.IsInputKeyReferenced(ctx, "inputs/"+retentionID)
	if err != nil || !referenced {
		t.Fatalf("input reference lookup: referenced=%v err=%v", referenced, err)
	}
	referenced, err = docRepo.IsInputKeyReferenced(ctx, "uploads/unknown/orphan")
	if err != nil || referenced {
		t.Fatalf("orphan input reference lookup: referenced=%v err=%v", referenced, err)
	}
	if err := docRepo.DeleteExpiredDocument(ctx, retentionID, cutoff); err != nil {
		t.Fatalf("delete expired document: %v", err)
	}
	if _, err := docRepo.GetByID(ctx, retentionID); err != domain.ErrNotFound {
		t.Fatalf("deleted document lookup error = %v, want %v", err, domain.ErrNotFound)
	}
	if _, err := docRepo.GetByID(ctx, freshID); err != nil {
		t.Fatalf("fresh terminal document was deleted: %v", err)
	}
	if _, err := docRepo.GetByID(ctx, queuedID); err != nil {
		t.Fatalf("queued document was deleted: %v", err)
	}

	legacyID := fmt.Sprintf("doc_legacy_%d", suffix)
	if _, err := pgRepo.Pool().Exec(ctx, `INSERT INTO documents
		(id, user_id, status, input_key, input_content_type, input_size_bytes, updated_at)
		VALUES ($1,$2,'failed',$3,NULL,0,now())`, legacyID, user.ID, "inputs/"+legacyID); err != nil {
		t.Fatalf("insert legacy nullable document: %v", err)
	}
	adminDocs, err := docRepo.ListByUser(ctx, 0, "", 100, 0)
	if err != nil {
		t.Fatalf("admin list must tolerate legacy nullable input metadata: %v", err)
	}
	foundLegacy := false
	for _, doc := range adminDocs {
		if doc.ID == legacyID {
			foundLegacy = doc.InputContentType == "" && doc.InputSizeBytes == 0
			break
		}
	}
	if !foundLegacy {
		t.Fatal("admin list did not return normalized legacy document")
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
