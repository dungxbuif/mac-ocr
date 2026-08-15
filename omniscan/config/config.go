package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

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

	// ── Operational timeouts & knobs (no hidden defaults) ──
	// Every former hardcoded literal is now an explicit env so an operator can
	// tune the bot for prod latencies without a code change.
	LLMHealthTimeout time.Duration // LLM_HEALTH_TIMEOUT (seconds): startup /v1/models probe
	OCRHTTPTimeout   time.Duration // OCR_HTTP_TIMEOUT (seconds): OCR proxy HTTP client
	OCRPollInterval  time.Duration // OCR_POLL_INTERVAL (seconds): poll cadence for doc status
	OCRPollTimeout   time.Duration // OCR_POLL_TIMEOUT (seconds): give up on a single OCR doc
	OCRProcessTimeout time.Duration // OCR_PROCESS_TIMEOUT (seconds): bot *ocr goroutine budget
	ScanProcessTimeout time.Duration // SCAN_PROCESS_TIMEOUT (seconds): bot *scan goroutine budget
	QATimeout        time.Duration // QA_TIMEOUT (seconds): bot Q&A goroutine budget
	MezonClientTimeout time.Duration // MEZON_CLIENT_TIMEOUT (seconds): Mezon SDK REST/WS client
	MaxAttachmentBytes int // MAX_ATTACHMENT_BYTES: hard cap for attachment download + validation
	DedupTTL         time.Duration // DEDUP_TTL (seconds): message-dedup window
	LLMScanTemperature float32 // LLM_SCAN_TEMPERATURE: sampling temp for *scan classify/format
	LLMQATemperature   float32 // LLM_QA_TEMPERATURE: sampling temp for Q&A
	UserLimitCacheTTL time.Duration // USER_LIMIT_CACHE_TTL (seconds): TTL for per-user limit cache
	SingleHostLock   bool   // SINGLE_HOST_LOCK: true → flock (dev/single-host); false → multi-host scale
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

	// Operational knobs — every value is required and validated so a missing
	// knob fails fast instead of silently using a guessed default.
	if cfg.LLMHealthTimeout, err = parseDurationSec("LLM_HEALTH_TIMEOUT", os.Getenv("LLM_HEALTH_TIMEOUT")); err != nil {
		return nil, err
	}
	if cfg.OCRHTTPTimeout, err = parseDurationSec("OCR_HTTP_TIMEOUT", os.Getenv("OCR_HTTP_TIMEOUT")); err != nil {
		return nil, err
	}
	if cfg.OCRPollInterval, err = parseDurationSec("OCR_POLL_INTERVAL", os.Getenv("OCR_POLL_INTERVAL")); err != nil {
		return nil, err
	}
	if cfg.OCRPollTimeout, err = parseDurationSec("OCR_POLL_TIMEOUT", os.Getenv("OCR_POLL_TIMEOUT")); err != nil {
		return nil, err
	}
	if cfg.OCRProcessTimeout, err = parseDurationSec("OCR_PROCESS_TIMEOUT", os.Getenv("OCR_PROCESS_TIMEOUT")); err != nil {
		return nil, err
	}
	if cfg.ScanProcessTimeout, err = parseDurationSec("SCAN_PROCESS_TIMEOUT", os.Getenv("SCAN_PROCESS_TIMEOUT")); err != nil {
		return nil, err
	}
	if cfg.QATimeout, err = parseDurationSec("QA_TIMEOUT", os.Getenv("QA_TIMEOUT")); err != nil {
		return nil, err
	}
	if cfg.MezonClientTimeout, err = parseDurationSec("MEZON_CLIENT_TIMEOUT", os.Getenv("MEZON_CLIENT_TIMEOUT")); err != nil {
		return nil, err
	}
	if cfg.MaxAttachmentBytes, err = parsePositiveInt("MAX_ATTACHMENT_BYTES", os.Getenv("MAX_ATTACHMENT_BYTES")); err != nil {
		return nil, err
	}
	if cfg.DedupTTL, err = parseDurationSec("DEDUP_TTL", os.Getenv("DEDUP_TTL")); err != nil {
		return nil, err
	}
	if cfg.LLMScanTemperature, err = parseFloat32("LLM_SCAN_TEMPERATURE", os.Getenv("LLM_SCAN_TEMPERATURE")); err != nil {
		return nil, err
	}
	if cfg.LLMQATemperature, err = parseFloat32("LLM_QA_TEMPERATURE", os.Getenv("LLM_QA_TEMPERATURE")); err != nil {
		return nil, err
	}
	if cfg.UserLimitCacheTTL, err = parseDurationSec("USER_LIMIT_CACHE_TTL", os.Getenv("USER_LIMIT_CACHE_TTL")); err != nil {
		return nil, err
	}
	if cfg.SingleHostLock, err = parseBoolStrict("SINGLE_HOST_LOCK", os.Getenv("SINGLE_HOST_LOCK")); err != nil {
		return nil, err
	}

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

// parseDurationSec parses a positive integer number of seconds into a
// time.Duration. Zero or negative seconds are rejected: every timeout must be a
// real value the operator chose.
func parseDurationSec(name, raw string) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, fmt.Errorf("%s is required (integer seconds, > 0)", name)
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer (seconds), got %q", name, raw)
	}
	return time.Duration(v) * time.Second, nil
}

// parseFloat32 parses a non-negative float env var used for LLM sampling
// temperatures. 0 is allowed (greedy decoding); negative is not.
func parseFloat32(name, raw string) (float32, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, fmt.Errorf("%s is required (a non-negative float)", name)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 32)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("%s must be a non-negative float, got %q", name, raw)
	}
	return float32(v), nil
}

// parseBoolStrict accepts only "true" or "false" (case-insensitive). Anything
// else — including empty — is rejected, so a toggle is never silently guessed.
func parseBoolStrict(name, raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be %q or %q, got %q", name, "true", "false", raw)
	}
}

