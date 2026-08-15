package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds every application knob as explicit fields, loaded and validated
// once at startup. There are NO hidden defaults: every value must be present
// in the environment, and Load returns an error if any is missing or invalid,
// so the process refuses to start rather than running with guessed values.
type Config struct {
	MezonBotID  string
	MezonToken  string
	MezonHost   string
	MezonPort   string
	OCRProxyURL string
	OCRAPIKey   string
	DatabaseURL string // PostgreSQL — required, primary store for quota + sessions.
	RedisURL    string // Redis — required, dedup + L2 cache shared across replicas.

	// LLM Agent Configuration
	LLMBaseURL string
	LLMAPIKey  string
	LLMModel   string

	// Limit Quota Configurations
	DailyScanLimit  int // *scan quota per day, per user
	DailyOCRLimit   int // *ocr quota per day, per user (separate counter)
	SessionAskLimit int // Q&A asks per document (per session)
}

// Load reads .env (best-effort) plus the real environment, then validates that
// every required variable is present. It returns an error for the first missing
// or invalid field, causing startup to fail fast — there is no silent fallback.
func Load() (*Config, error) {
	_ = godotenv.Load(".env")

	cfg := &Config{
		MezonBotID:  os.Getenv("MEZON_BOT_ID"),
		MezonToken:  os.Getenv("MEZON_BOT_TOKEN"),
		MezonHost:   os.Getenv("MEZON_HOST"),
		MezonPort:   os.Getenv("MEZON_PORT"),
		OCRProxyURL: os.Getenv("OCR_PROXY_URL"),
		OCRAPIKey:   os.Getenv("OCR_API_KEY"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisURL:    os.Getenv("REDIS_URL"),

		LLMBaseURL: os.Getenv("LLM_BASE_URL"),
		LLMAPIKey:  os.Getenv("LLM_API_KEY"),
		LLMModel:   os.Getenv("LLM_MODEL"),
	}

	var missing []string
	require := func(name, val string) {
		if strings.TrimSpace(val) == "" {
			missing = append(missing, name)
		}
	}
	require("MEZON_BOT_ID", cfg.MezonBotID)
	require("MEZON_BOT_TOKEN", cfg.MezonToken)
	require("MEZON_HOST", cfg.MezonHost)
	require("MEZON_PORT", cfg.MezonPort)
	require("OCR_PROXY_URL", cfg.OCRProxyURL)
	require("OCR_API_KEY", cfg.OCRAPIKey)
	require("DATABASE_URL", cfg.DatabaseURL)
	require("REDIS_URL", cfg.RedisURL)
	require("LLM_BASE_URL", cfg.LLMBaseURL)
	require("LLM_API_KEY", cfg.LLMAPIKey)
	require("LLM_MODEL", cfg.LLMModel)
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}

	port, err := strconv.Atoi(cfg.MezonPort)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("MEZON_PORT must be a valid port (1-65535), got %q", cfg.MezonPort)
	}

	scanLimit, err := parsePositiveInt("DAILY_SCAN_LIMIT", os.Getenv("DAILY_SCAN_LIMIT"))
	if err != nil {
		return nil, err
	}
	cfg.DailyScanLimit = scanLimit

	ocrLimit, err := parsePositiveInt("DAILY_OCR_LIMIT", os.Getenv("DAILY_OCR_LIMIT"))
	if err != nil {
		return nil, err
	}
	cfg.DailyOCRLimit = ocrLimit

	askLimit, err := parsePositiveInt("SESSION_ASK_LIMIT", os.Getenv("SESSION_ASK_LIMIT"))
	if err != nil {
		return nil, err
	}
	cfg.SessionAskLimit = askLimit

	return cfg, nil
}

// parsePositiveInt parses a strict positive integer env var. Empty, non-numeric,
// or non-positive values yield an error so configuration problems surface
// immediately instead of falling back to a hidden default.
func parsePositiveInt(name, raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, fmt.Errorf("%s is required (must be a positive integer)", name)
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", name, raw)
	}
	return v, nil
}

