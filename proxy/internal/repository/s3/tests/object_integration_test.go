package tests

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joho/godotenv"

	s3repo "macocr/proxy/internal/repository/s3"
)

// TestRoundTrip exercises the S3 repository against a live endpoint (s3rver
// in local dev). It is skipped unless TEST_S3=1 so unit builds stay offline.
func TestRoundTrip(t *testing.T) {
	if os.Getenv("TEST_S3") != "1" {
		t.Skip("set TEST_S3=1 to run against live S3/s3rver")
	}
	loadRepoEnv(t)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	repo, err := s3repo.New(s3repo.Config{
		Endpoint:        os.Getenv("S3_ENDPOINT"),
		Region:          os.Getenv("S3_REGION"),
		Bucket:          os.Getenv("S3_BUCKET"),
		AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		ForcePathStyle:  true,
	}, logger)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}

	ctx := context.Background()
	key := "test/roundtrip-" + time.Now().Format("150405")

	if err := repo.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	exists, err := repo.Exists(ctx, key)
	if err != nil {
		t.Fatalf("exists before: %v", err)
	}
	if exists {
		t.Fatal("expected object to not exist before put")
	}

	payload := []byte("hello mac-ocr s3")
	if err := repo.Put(ctx, key, bytes.NewReader(payload), "text/plain"); err != nil {
		t.Fatalf("put: %v", err)
	}

	exists, err = repo.Exists(ctx, key)
	if err != nil {
		t.Fatalf("exists after: %v", err)
	}
	if !exists {
		t.Fatal("expected object to exist after put")
	}

	rc, err := repo.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("body mismatch: got %q want %q", got, payload)
	}

	u, err := repo.PresignGetURL(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	if u == "" {
		t.Fatal("presigned url empty")
	}
	t.Logf("presigned url: %s", u)
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
