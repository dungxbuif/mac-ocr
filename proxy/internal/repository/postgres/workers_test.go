package postgres

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"macocr/proxy/domain"
	"macocr/proxy/internal/native"
	"macocr/proxy/internal/notifications"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func workerTestDB(t *testing.T) (*pgxpool.Pool, *notifications.SecretCipher) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run isolated PostgreSQL worker integration tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schemaName := fmt.Sprintf("workers_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schemaName
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(); _, _ = admin.Exec(ctx, "DROP SCHEMA "+schemaName+" CASCADE"); admin.Close() })
	repo := &Repository{pool: pool}
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	cipher, err := notifications.NewSecretCipher("integration-only-encryption-key-value")
	if err != nil {
		t.Fatal(err)
	}
	return pool, cipher
}
func TestWorkerEnrollmentExpiryReuseAndRevoke(t *testing.T) {
	pool, cipher := workerTestDB(t)
	r := NewWorkerRepository(pool, cipher)
	ctx := context.Background()
	if err := r.CreateEnrollment(ctx, "expired", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := r.Enroll(ctx, "expired", "expired-node", "Expired", "secret"); err == nil {
		t.Fatal("expired token enrolled")
	}
	if err := r.CreateEnrollment(ctx, "single-use", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if r.Enroll(ctx, "single-use", fmt.Sprintf("node-%d", i), "Mac", "secret") == nil {
				successes.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("single token consumed %d times", successes.Load())
	}
	nodes, err := r.ListWorkers(ctx)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("workers=%d %v", len(nodes), err)
	}
	nodeID := nodes[0].NodeID
	secret, err := r.Secret(ctx, nodeID)
	if err != nil || secret != "secret" {
		t.Fatal("credential roundtrip failed")
	}
	var encrypted []byte
	if err := pool.QueryRow(ctx, `SELECT secret FROM native_workers WHERE node_id=$1`, nodeID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if string(encrypted) == secret {
		t.Fatal("credential stored as plaintext")
	}
	if err := r.Revoke(ctx, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Secret(ctx, nodeID); err == nil {
		t.Fatal("revoked key resolved")
	}
	if err := r.RegisterShared(ctx, nodeID, "new-shared-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Secret(ctx, nodeID); err == nil {
		t.Fatal("shared registration reactivated revoked node")
	}
	if err := r.CreateEnrollment(ctx, "reenroll", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := r.Enroll(ctx, "reenroll", nodeID, "Reenrolled Mac", "replacement-secret"); err != nil {
		t.Fatalf("fresh authorized reenrollment failed: %v", err)
	}
	if secret, err := r.Secret(ctx, nodeID); err != nil || secret != "replacement-secret" {
		t.Fatal("reenrollment did not rotate credential")
	}

}
func TestWorkerClaimFiltersCapabilityAndAssignment(t *testing.T) {
	pool, cipher := workerTestDB(t)
	ctx := context.Background()
	r := NewWorkerRepository(pool, cipher)
	docs := NewDocumentRepository(pool, cipher)
	if err := r.RegisterShared(ctx, "node-mac", "shared"); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users(email)VALUES('worker-test@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `INSERT INTO documents(id,user_id,input_key,input_content_type,options_json,created_at)VALUES('scan-job',$1,'input/scan','image/png','{"processingMode":"scan"}',now()-interval '1 minute'),('ocr-job',$1,'input/ocr','application/pdf',NULL,now())`, userID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := docs.ClaimNextForWorker(ctx, "att-ocr", time.Minute, 3, 4, 1, 4, true, false)
	if err != nil || doc == nil || doc.ID != "ocr-job" {
		t.Fatalf("OCR capability selected wrong row %+v %v", doc, err)
	}
	if err := r.Assign(ctx, native.WorkerRoute{NodeID: "node-mac", DocumentID: doc.ID, AttemptID: "stale", Kind: "ocr"}); err == nil {
		t.Fatal("stale attempt assigned")
	}
	if err := r.Assign(ctx, native.WorkerRoute{NodeID: "node-mac", DocumentID: doc.ID, AttemptID: doc.AttemptID, Kind: "ocr"}); err != nil {
		t.Fatal(err)
	}
	assigned, err := r.AssignedNode(ctx, doc.ID, doc.AttemptID)
	if err != nil || assigned != "node-mac" {
		t.Fatal("assignment did not persist")
	}
	scan, err := docs.ClaimNextForWorker(ctx, "att-scan", time.Minute, 3, 1, 1, 4, false, true)
	if err != nil || scan == nil || scan.ID != "scan-job" {
		t.Fatalf("scan capability selected wrong row %+v %v", scan, err)
	}
	routes, err := r.Routes(ctx)
	if err != nil || len(routes) != 1 || routes[0].Status != string(domain.StatusProcessing) {
		t.Fatalf("routes %v %v", routes, err)
	}
	if err := r.Revoke(ctx, "node-mac"); err != nil {
		t.Fatal(err)
	}
	if err := r.Assign(ctx, native.WorkerRoute{NodeID: "node-mac", DocumentID: scan.ID, AttemptID: scan.AttemptID, Kind: "scan"}); err == nil {
		t.Fatal("revoked node assigned")
	}
}
