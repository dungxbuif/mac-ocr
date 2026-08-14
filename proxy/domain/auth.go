package domain

import (
	"context"
	"time"
)

type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

type User struct {
	ID           int64          `json:"id"`
	Email        string         `json:"email"`
	PasswordHash string         `json:"-"`
	Role         Role           `json:"role"`
	Disabled     bool           `json:"disabled"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	Config       *AccountConfig `json:"config,omitempty"`
}

type AccountConfig struct {
	UserID               int64      `json:"user_id"`
	RateLimitRPM         int        `json:"rate_limit_rpm"`
	DocQuota             int64      `json:"doc_quota"`
	DocUsed              int64      `json:"doc_used"`
	StorageQuotaBytes    int64      `json:"storage_quota_bytes"`
	StorageUsedBytes     int64      `json:"storage_used_bytes"`
	StorageReservedBytes int64      `json:"storage_reserved_bytes"`
	QuotaResetAt         *time.Time `json:"quota_reset_at,omitempty"`
	UpdatedAt            time.Time  `json:"updated_at"`
	UpdatedBy            *int64     `json:"updated_by,omitempty"`
}

type UploadReservation struct {
	ObjectKey string    `json:"object_key"`
	UserID    int64     `json:"user_id"`
	SizeBytes int64     `json:"size_bytes"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

type UploadReservationRepository interface {
	ReserveUpload(ctx context.Context, reservation UploadReservation) error
	ReleaseUpload(ctx context.Context, userID int64, objectKey string) (bool, error)
	ListExpiredUploads(ctx context.Context, before time.Time, limit int) ([]UploadReservation, error)
}

type ApiKey struct {
	ID           int64      `json:"id"`
	UserID       int64      `json:"user_id"`
	Name         string     `json:"name"`
	Prefix       string     `json:"prefix"`
	KeyHash      string     `json:"-"`
	RateLimitRPM int        `json:"rate_limit_rpm"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type UserRepository interface {
	Create(ctx context.Context, u *User) (*User, error)
	GetByID(ctx context.Context, id int64) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	List(ctx context.Context, limit, offset int) ([]User, error)
	Update(ctx context.Context, u *User) (*User, error)
}

type AccountConfigRepository interface {
	GetByUserID(ctx context.Context, userID int64) (*AccountConfig, error)
	Update(ctx context.Context, cfg *AccountConfig) (*AccountConfig, error)
	ReserveDocs(ctx context.Context, userID int64, count int64) error
	RefundDocs(ctx context.Context, userID int64, count int64) error
	ResetDocUsed(ctx context.Context, userID int64) error
}

type ApiKeyRepository interface {
	Create(ctx context.Context, k *ApiKey) (*ApiKey, error)
	ListByUser(ctx context.Context, userID int64) ([]ApiKey, error)
	GetByHash(ctx context.Context, hash string) (*ApiKey, error)
	Revoke(ctx context.Context, id int64) error
	UpdateRateLimit(ctx context.Context, id int64, rateLimitRPM int) (*ApiKey, error)
}
