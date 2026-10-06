package tests

import (
	"testing"
	"time"

	"macocr/proxy/internal/config"
)

func setEnv(t *testing.T, vals map[string]string) {
	t.Helper()
	for k, v := range vals {
		t.Setenv(k, v)
	}
}

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":         "postgres://user:pass@localhost:5432/macocr",
		"REDIS_URL":            "redis://localhost:6379/0",
		"S3_ENDPOINT":          "http://localhost:9000",
		"S3_BUCKET":            "macocr-inputs",
		"S3_ACCESS_KEY_ID":     "S3RVER",
		"S3_SECRET_ACCESS_KEY": "S3RVER",
		"PUBLIC_API_BASE_URL":  "http://localhost:8080",
		"PUBLIC_DOCS_BASE_URL": "http://localhost:3000",
	}
}

func TestLoadValid(t *testing.T) {
	setEnv(t, validEnv())
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.S3Bucket != "macocr-inputs" {
		t.Fatalf("unexpected bucket %q", cfg.S3Bucket)
	}
	if !cfg.S3ForcePathStyle {
		t.Fatal("expected path style default true")
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("unexpected shutdown timeout %v", cfg.ShutdownTimeout)
	}
	if cfg.MaxUploadBytes != 104857600 {
		t.Fatalf("unexpected max upload bytes %d", cfg.MaxUploadBytes)
	}
	if cfg.DocumentTTL != 2160*time.Hour {
		t.Fatalf("unexpected document TTL %v", cfg.DocumentTTL)
	}
	if cfg.NotificationTTL != 2160*time.Hour {
		t.Fatalf("unexpected notification TTL %v", cfg.NotificationTTL)
	}
	if cfg.UploadTTL != 24*time.Hour {
		t.Fatalf("unexpected orphan upload TTL %v", cfg.UploadTTL)
	}
}

func TestLoadDefaults(t *testing.T) {
	setEnv(t, validEnv())
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("unexpected addr %q", cfg.HTTPAddr)
	}
	if cfg.Env != "development" {
		t.Fatalf("unexpected env %q", cfg.Env)
	}
	if cfg.S3Region != "us-east-1" {
		t.Fatalf("unexpected region %q", cfg.S3Region)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	env := validEnv()
	env["S3_BUCKET"] = ""
	setEnv(t, env)
	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for missing required config")
	}
}

func TestLoadInvalidURL(t *testing.T) {
	env := validEnv()
	env["PUBLIC_API_BASE_URL"] = "://bad"
	setEnv(t, env)
	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestLoadRejectsDocumentTTLAtOrBelowResultTTL(t *testing.T) {
	env := validEnv()
	env["RESULT_TTL"] = "168h"
	env["DOCUMENT_TTL"] = "168h"
	setEnv(t, env)
	_, err := config.Load()
	if err == nil {
		t.Fatal("expected metadata retention to exceed result retention")
	}
}

func TestSharedOutboundProductionDoesNotRequireLocalNativeURL(t *testing.T) {
	setEnv(t, validEnv())
	t.Setenv("APP_ENV", "production")
	t.Setenv("NATIVE_TRANSPORT", "websocket")
	t.Setenv("WORKER_AUTH_MODE", "shared")
	t.Setenv("NATIVE_BASE_URL", "")
	t.Setenv("NATIVE_AUTH_SECRET", "shared-worker-key-at-least-32-characters")
	t.Setenv("NOTIFICATION_ENCRYPTION_KEY", "test-notification-key-at-least-32-characters")
	cfg, err := config.Load()
	if err != nil || cfg.NativeTransport != "websocket" {
		t.Fatalf("shared outbound config: %v", err)
	}
	t.Setenv("NATIVE_AUTH_SECRET", "")
	if _, err := config.Load(); err == nil {
		t.Fatal("shared outbound mode accepted missing key")
	}
}
