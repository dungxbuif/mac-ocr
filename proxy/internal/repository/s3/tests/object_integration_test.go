package tests

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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

	putKey := "test/presigned-put-" + time.Now().Format("150405")
	putPayload := []byte("hello presigned upload")
	putURL, headers, err := repo.PresignPutURL(ctx, putKey, "text/plain", int64(len(putPayload)), time.Minute)
	if err != nil {
		t.Fatalf("presign put: %v", err)
	}
	if got := headers.Get("Content-Length"); got != fmt.Sprint(len(putPayload)) {
		t.Fatalf("presigned PUT must bind exact Content-Length: got %q want %d", got, len(putPayload))
	}
	putReq, err := http.NewRequest(http.MethodPut, putURL, bytes.NewReader(putPayload))
	if err != nil {
		t.Fatalf("build presigned put request: %v", err)
	}
	for name, values := range headers {
		for _, value := range values {
			putReq.Header.Add(name, value)
		}
	}
	putReq.ContentLength = int64(len(putPayload))
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("execute presigned put: %v", err)
	}
	_, _ = io.Copy(io.Discard, putResp.Body)
	_ = putResp.Body.Close()
	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		t.Fatalf("presigned put status = %d", putResp.StatusCode)
	}
	info, err := repo.Stat(ctx, putKey)
	if err != nil {
		t.Fatalf("stat presigned upload: %v", err)
	}
	if info.SizeBytes != int64(len(putPayload)) {
		t.Fatalf("presigned upload size = %d, want %d", info.SizeBytes, len(putPayload))
	}

	janitorKey := "uploads/999/janitor-" + time.Now().Format("150405.000")
	if err := repo.Put(ctx, janitorKey, bytes.NewReader(payload), "text/plain"); err != nil {
		t.Fatalf("put orphan-upload fixture: %v", err)
	}
	expiredUploads, err := repo.ListExpiredUploads(ctx, time.Now().Add(time.Minute), 100)
	if err != nil {
		t.Fatalf("list expired uploads: %v", err)
	}
	foundJanitor := false
	for _, object := range expiredUploads {
		if object.Key == janitorKey {
			foundJanitor = !object.LastModified.IsZero() && object.SizeBytes == int64(len(payload))
			break
		}
	}
	if !foundJanitor {
		t.Fatalf("expired upload listing did not return fixture %q", janitorKey)
	}
	if err := repo.Delete(ctx, janitorKey); err != nil {
		t.Fatalf("delete orphan-upload fixture: %v", err)
	}

	mismatchKey := "test/presigned-put-size-mismatch-" + time.Now().Format("150405.000")
	mismatchURL, mismatchHeaders, err := repo.PresignPutURL(ctx, mismatchKey, "text/plain", int64(len(putPayload)), time.Minute)
	if err != nil {
		t.Fatalf("presign size mismatch PUT: %v", err)
	}
	mismatchPayload := append(append([]byte(nil), putPayload...), '!')
	mismatchReq, err := http.NewRequest(http.MethodPut, mismatchURL, bytes.NewReader(mismatchPayload))
	if err != nil {
		t.Fatalf("build mismatch PUT request: %v", err)
	}
	for name, values := range mismatchHeaders {
		for _, value := range values {
			mismatchReq.Header.Add(name, value)
		}
	}
	mismatchReq.ContentLength = int64(len(mismatchPayload))
	mismatchResp, err := http.DefaultClient.Do(mismatchReq)
	if err != nil {
		t.Fatalf("execute mismatch PUT: %v", err)
	}
	_, _ = io.Copy(io.Discard, mismatchResp.Body)
	_ = mismatchResp.Body.Close()
	if mismatchResp.StatusCode >= 200 && mismatchResp.StatusCode < 300 {
		if os.Getenv("EXPECT_S3_SIGNED_CONTENT_LENGTH") == "1" {
			t.Fatalf("S3 accepted body with Content-Length different from presigned value: status=%d", mismatchResp.StatusCode)
		}
		t.Log("S3 emulator did not enforce signed Content-Length; production-equivalent storage must run this test with EXPECT_S3_SIGNED_CONTENT_LENGTH=1")
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
