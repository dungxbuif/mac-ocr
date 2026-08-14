package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	MezonBotID  string
	MezonToken  string
	MezonHost   string
	MezonPort   string
	OCRProxyURL string
	OCRAPIKey   string
	RedisURL    string

	// LLM Agent Configuration
	LLMBaseURL string
	LLMAPIKey  string
	LLMModel   string

	// Limit Quota Configurations
	DailyScanLimit  int
	SessionAskLimit int
}

func Load() (*Config, error) {
	_ = godotenv.Load(".env")

	cfg := &Config{
		MezonBotID:  os.Getenv("MEZON_BOT_ID"),
		MezonToken:  os.Getenv("MEZON_BOT_TOKEN"),
		MezonHost:   getEnvOrDefault("MEZON_HOST", "gw.mezon.ai"),
		MezonPort:   getEnvOrDefault("MEZON_PORT", "443"),
		OCRProxyURL: getEnvOrDefault("OCR_PROXY_URL", "http://localhost:8080"),
		OCRAPIKey:   os.Getenv("OCR_API_KEY"),
		RedisURL:    os.Getenv("REDIS_URL"),

		LLMBaseURL: os.Getenv("LLM_BASE_URL"),
		LLMAPIKey:  os.Getenv("LLM_API_KEY"),
		LLMModel:   os.Getenv("LLM_MODEL"),

		DailyScanLimit:  getEnvIntOrDefault("DAILY_SCAN_LIMIT", 5),
		SessionAskLimit: getEnvIntOrDefault("SESSION_ASK_LIMIT", 5),
	}

	if cfg.MezonBotID == "" {
		return nil, errors.New("MEZON_BOT_ID is required")
	}
	if cfg.MezonToken == "" {
		return nil, errors.New("MEZON_BOT_TOKEN is required")
	}
	if cfg.OCRAPIKey == "" {
		return nil, errors.New("OCR_API_KEY is required (generate via 'macocr-admin create-key')")
	}
	if cfg.LLMBaseURL == "" {
		return nil, errors.New("LLM_BASE_URL is required")
	}
	if cfg.LLMModel == "" {
		return nil, errors.New("LLM_MODEL is required")
	}

	return cfg, nil
}

func getEnvOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func getEnvIntOrDefault(key string, fallback int) int {
	if val := os.Getenv(key); val != "" {
		var res int
		if _, err := fmt.Sscanf(val, "%d", &res); err == nil && res > 0 {
			return res
		}
	}
	return fallback
}
