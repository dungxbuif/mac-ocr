package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-playground/validator/v10"
	"github.com/joho/godotenv"
)

type Config struct {
	Env string `env:"APP_ENV" envDefault:"development" validate:"oneof=development staging production test"`

	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8080"`

	DatabaseURL string `env:"DATABASE_URL,required" validate:"required,uri"`
	RedisURL    string `env:"REDIS_URL,required"    validate:"required"`

	S3Endpoint        string `env:"S3_ENDPOINT,required"        validate:"required,uri"`
	S3Region          string `env:"S3_REGION" envDefault:"us-east-1"`
	S3Bucket          string `env:"S3_BUCKET,required"          validate:"required"`
	S3AccessKeyID     string `env:"S3_ACCESS_KEY_ID,required"   validate:"required"`
	S3SecretAccessKey string `env:"S3_SECRET_ACCESS_KEY,required" validate:"required"`
	S3ForcePathStyle  bool   `env:"S3_FORCE_PATH_STYLE" envDefault:"true"`

	PublicAPIBaseURL  string `env:"PUBLIC_API_BASE_URL,required"  validate:"required,uri"`
	PublicDocsBaseURL string `env:"PUBLIC_DOCS_BASE_URL,required" validate:"required,uri"`

	NativeBaseURL             string        `env:"NATIVE_BASE_URL"`
	NativeAuthSecret          string        `env:"NATIVE_AUTH_SECRET"`
	NotificationEncryptionKey string        `env:"NOTIFICATION_ENCRYPTION_KEY" envDefault:"local-notification-key-change-me"`
	ResultTTL                 time.Duration `env:"RESULT_TTL" envDefault:"168h" validate:"gt=0"`
	InputTTL                  time.Duration `env:"INPUT_TTL" envDefault:"168h" validate:"gt=0"`
	NotificationTTL           time.Duration `env:"NOTIFICATION_TTL" envDefault:"720h" validate:"gt=0"`
	ProcessingLease           time.Duration `env:"PROCESSING_LEASE" envDefault:"15m" validate:"gt=0"`
	ProcessingMaxAttempts     int           `env:"PROCESSING_MAX_ATTEMPTS" envDefault:"3" validate:"gte=1,lte=20"`

	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
}

var validate = validator.New()

const developmentNotificationKey = "local-notification-key-change-me"

func Load() (*Config, error) {
	_ = godotenv.Load(".env", "../.env")

	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("parse environment config: %w", err)
	}

	if err := validate.Struct(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	if cfg.Env == "production" {
		if cfg.NativeBaseURL == "" || cfg.NativeAuthSecret == "" {
			return nil, fmt.Errorf("NATIVE_BASE_URL and NATIVE_AUTH_SECRET are required in production")
		}
		if cfg.NotificationEncryptionKey == developmentNotificationKey || len(cfg.NotificationEncryptionKey) < 32 {
			return nil, fmt.Errorf("NOTIFICATION_ENCRYPTION_KEY must be a non-default value of at least 32 characters in production")
		}
	}

	return cfg, nil
}
