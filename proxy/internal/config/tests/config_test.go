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
