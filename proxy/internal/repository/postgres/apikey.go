package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"macocr/proxy/domain"
)

type APIKeyRepository struct {
	pool *pgxpool.Pool
}

func NewAPIKeyRepository(pool *pgxpool.Pool) *APIKeyRepository {
	return &APIKeyRepository{pool: pool}
}

func (r *APIKeyRepository) Create(ctx context.Context, k *domain.ApiKey) (*domain.ApiKey, error) {
	rateLimit := k.RateLimitRPM
	if rateLimit <= 0 {
		rateLimit = 60
	}
	name := k.Name
	if name == "" {
		name = "default"
	}

	var out domain.ApiKey
	err := r.pool.QueryRow(ctx,
		`INSERT INTO api_keys (user_id, name, key_prefix, key_hash, rate_limit_rpm)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, user_id, name, key_prefix, rate_limit_rpm, revoked_at, created_at`,
		k.UserID, name, k.Prefix, k.KeyHash, rateLimit,
	).Scan(&out.ID, &out.UserID, &out.Name, &out.Prefix, &out.RateLimitRPM, &out.RevokedAt, &out.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert api_key: %w", err)
	}
	return &out, nil
}

func (r *APIKeyRepository) ListByUser(ctx context.Context, userID int64) ([]domain.ApiKey, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, name, key_prefix, rate_limit_rpm, revoked_at, created_at
		 FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list api_keys: %w", err)
	}
	defer rows.Close()

	keys := []domain.ApiKey{}
	for rows.Next() {
		var k domain.ApiKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.RateLimitRPM, &k.RevokedAt, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan api_key: %w", err)
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (r *APIKeyRepository) GetByHash(ctx context.Context, hash string) (*domain.ApiKey, error) {
	var k domain.ApiKey
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, name, key_prefix, rate_limit_rpm, revoked_at, created_at
		 FROM api_keys WHERE key_hash = $1`, hash,
	).Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.RateLimitRPM, &k.RevokedAt, &k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select api_key: %w", err)
	}
	return &k, nil
}

func (r *APIKeyRepository) Revoke(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("revoke api_key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *APIKeyRepository) UpdateRateLimit(ctx context.Context, id int64, rateLimitRPM int) (*domain.ApiKey, error) {
	if rateLimitRPM < 0 {
		rateLimitRPM = 0
	}
	var out domain.ApiKey
	err := r.pool.QueryRow(ctx,
		`UPDATE api_keys SET rate_limit_rpm = $1 WHERE id = $2
		 RETURNING id, user_id, name, key_prefix, rate_limit_rpm, revoked_at, created_at`,
		rateLimitRPM, id,
	).Scan(&out.ID, &out.UserID, &out.Name, &out.Prefix, &out.RateLimitRPM, &out.RevokedAt, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update api_key rate_limit: %w", err)
	}
	return &out, nil
}
