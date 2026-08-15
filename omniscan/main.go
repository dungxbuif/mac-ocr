package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	mezon "mezon-bot-sdk"

	"omniscan/agent"
	"omniscan/bot"
	"omniscan/config"
	"omniscan/ocr"
	"omniscan/security"
	"omniscan/storage"
)

func main() {
	log.Println("🚀 Starting OmniScan Mezon Bot Service...")

	// Single-instance file lock protection. NOTE: this prevents two replicas
	// on the same host. For true horizontal scaling across hosts, remove the
	// flock and rely on PostgreSQL (quota/session) + Redis (dedup) instead.
	lockFile, err := os.OpenFile("omniscan.lock", os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		log.Fatalf("❌ Failed to open lock file: %v", err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		log.Fatalf("🛑 Another instance of OmniScan bot is already running (lock active). Exiting to prevent duplicate replies.")
	}
	defer func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	}()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("❌ Failed to load config: %v", err)
	}

	var quotaStore storage.QuotaStore
	var sessionStore storage.SessionStore
	var dedup storage.Deduplicator
	var sharedStore mezon.SharedStore
	var pgQuota *storage.PostgresQuotaStore

	// PostgreSQL is the multi-replica source of truth for quotas and sessions.
	// Redis, when also configured, only backs dedup + the SDK's L2 channel
	// cache (so replicas avoid redundant REST channel fetches).
	if cfg.DatabaseURL != "" {
		log.Printf("🗄️ Initializing PostgreSQL store (%s)...", redactURL(cfg.DatabaseURL))
		quotaStore, sessionStore, pgQuota, err = initPostgres(cfg.DatabaseURL)
		if err != nil {
			log.Fatalf("❌ %v", err)
		}
		log.Println("✅ PostgreSQL QuotaStore & SessionStore initialized (multi-replica ready)!")
	} else {
		quotaStore, sessionStore, err = initSQLite()
		if err != nil {
			log.Fatalf("❌ %v", err)
		}
		log.Println("✅ SQLite QuotaStore & SessionStore initialized (single-replica only).")
	}

	if cfg.RedisURL != "" {
		log.Printf("🔌 Initializing Redis (%s)...", redactURL(cfg.RedisURL))
		redisStore, err := storage.NewRedisQuotaStore(cfg.RedisURL)
		if err != nil {
			log.Fatalf("❌ Failed to connect Redis: %v", err)
		}
		dedup = storage.NewRedisDeduplicator(redisStore.Client)
		sharedStore = storage.NewRedisSharedStore(redisStore.Client)
		log.Println("✅ Redis Deduplicator & SDK L2 Shared Cache initialized!")
	} else {
		dedup = storage.NewInMemoryDeduplicator()
		log.Println("✅ In-Memory Deduplicator initialized (single-replica only).")
	}
	defer quotaStore.Close()
	defer sessionStore.Close()

	validator := security.NewValidator()
	ocrClient := ocr.NewClient(cfg.OCRProxyURL, cfg.OCRAPIKey)
	aiAgent := agent.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)

	// Startup LLM probe: confirm the OpenAI-compatible endpoint is reachable and
	// the key is valid before accepting *scan traffic. *ocr (raw) needs no LLM,
	// so an unreachable LLM does NOT block start — we only warn loudly so the
	// operator can fix the model server while raw OCR keeps working.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := aiAgent.Health(probeCtx); err != nil {
		log.Printf("⚠️ LLM endpoint NOT reachable: %v", err)
		log.Printf("⚠️ *scan (AI flow) will fail fast until the model server is up; *ocr (raw) still works.")
		aiAgent.SetHealth(false)
	} else {
		log.Printf("✅ LLM endpoint OK: %s (model: %s)", cfg.LLMBaseURL, cfg.LLMModel)
		aiAgent.SetHealth(true)
	}
	probeCancel()

	b, err := bot.New(cfg, ocrClient, quotaStore, sessionStore, validator, aiAgent, dedup, sharedStore, pgQuota)
	if err != nil {
		log.Fatalf("❌ Failed to initialize OmniScan bot: %v", err)
	}

	if err := b.Start(); err != nil {
		log.Fatalf("❌ Bot login error: %v", err)
	}

	log.Printf("✅ OmniScan AI Bot service is running (Scan: %d/day, OCR: %d/day, Ask: %d/session). Press Ctrl+C to exit.",
		cfg.DailyScanLimit, cfg.DailyOCRLimit, cfg.SessionAskLimit)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("👋 Shutting down OmniScan AI Bot service gracefully...")
}
