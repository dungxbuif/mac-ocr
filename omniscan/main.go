package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	mezon "github.com/quangledang23/mezon-sdk-go"

	"omniscan/agent"
	"omniscan/bot"
	"omniscan/config"
	"omniscan/ocr"
	"omniscan/security"
	"omniscan/storage"
)

func main() {
	log.Println("🚀 Starting OmniScan Mezon Bot Service...")

	// Single-instance file lock protection
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

	if cfg.RedisURL != "" {
		log.Printf("🔌 Initializing Redis cluster support (%s)...", cfg.RedisURL)
		redisStore, err := storage.NewRedisQuotaStore(cfg.RedisURL)
		if err != nil {
			log.Fatalf("❌ Failed to connect to Redis: %v", err)
		}
		quotaStore = redisStore
		sessionStore = storage.NewRedisSessionStore(redisStore.Client)
		dedup = storage.NewRedisDeduplicator(redisStore.Client)
		sharedStore = storage.NewRedisSharedStore(redisStore.Client)
		log.Println("✅ Redis QuotaStore, SessionStore, Message Deduplicator, and L2 Shared Cache initialized!")
	} else {
		log.Println("📦 Initializing local SQLite store (omniscan.db & omniscan_sessions.db)...")
		sqStore, err := storage.NewSQLiteQuotaStore("omniscan.db")
		if err != nil {
			log.Fatalf("❌ Failed to initialize SQLite QuotaStore: %v", err)
		}
		quotaStore = sqStore

		sqSessionStore, err := storage.NewSQLiteSessionStore("omniscan_sessions.db")
		if err != nil {
			log.Fatalf("❌ Failed to initialize SQLite SessionStore: %v", err)
		}
		sessionStore = sqSessionStore
		dedup = storage.NewInMemoryDeduplicator()
		log.Println("✅ SQLite QuotaStore, SessionStore & In-Memory Deduplicator initialized!")
	}
	defer quotaStore.Close()
	defer sessionStore.Close()

	validator := security.NewValidator()
	ocrClient := ocr.NewClient(cfg.OCRProxyURL, cfg.OCRAPIKey)
	aiAgent := agent.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)

	b, err := bot.New(cfg, ocrClient, quotaStore, sessionStore, validator, aiAgent, dedup, sharedStore)
	if err != nil {
		log.Fatalf("❌ Failed to initialize OmniScan bot: %v", err)
	}

	if err := b.Start(); err != nil {
		log.Fatalf("❌ Bot login error: %v", err)
	}

	log.Printf("✅ OmniScan AI Bot service is running (Daily scan limit: %d/day, Ask limit: %d/session). Press Ctrl+C to exit.",
		cfg.DailyScanLimit, cfg.SessionAskLimit)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("👋 Shutting down OmniScan AI Bot service gracefully...")
}
