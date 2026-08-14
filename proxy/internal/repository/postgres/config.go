package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"macocr/proxy/domain"
)

type AccountConfigRepository struct {
	pool *pgxpool.Pool
}

func NewAccountConfigRepository(pool *pgxpool.Pool) *AccountConfigRepository {
	return &AccountConfigRepository{pool: pool}
}

func (r *AccountConfigRepository) GetByUserID(ctx context.Context, userID int64) (*domain.AccountConfig, error) {
	var cfg domain.AccountConfig
	err := r.pool.QueryRow(ctx,
		`SELECT user_id, rate_limit_rpm, doc_quota, doc_used, quota_reset_at, updated_at, updated_by
		 FROM account_configs WHERE user_id = $1`, userID,
	).Scan(&cfg.UserID, &cfg.RateLimitRPM, &cfg.DocQuota, &cfg.DocUsed, &cfg.QuotaResetAt, &cfg.UpdatedAt, &cfg.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select account_config: %w", err)
	}
	return &cfg, nil
}

func (r *AccountConfigRepository) Update(ctx context.Context, cfg *domain.AccountConfig) (*domain.AccountConfig, error) {
	var out domain.AccountConfig
	err := r.pool.QueryRow(ctx,
		`UPDATE account_configs
		 SET rate_limit_rpm = $1, doc_quota = $2, quota_reset_at = $3, updated_by = $4, updated_at = now()
		 WHERE user_id = $5
		 RETURNING user_id, rate_limit_rpm, doc_quota, doc_used, quota_reset_at, updated_at, updated_by`,
		cfg.RateLimitRPM, cfg.DocQuota, cfg.QuotaResetAt, cfg.UpdatedBy, cfg.UserID,
	).Scan(&out.UserID, &out.RateLimitRPM, &out.DocQuota, &out.DocUsed, &out.QuotaResetAt, &out.UpdatedAt, &out.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update account_config: %w", err)
	}
	return &out, nil
}

func (r *AccountConfigRepository) ReserveDocs(ctx context.Context, userID int64, count int64) error {
	if count <= 0 {
		return nil
	}

	var uid int64
	err := r.pool.QueryRow(ctx,
		`UPDATE account_configs
		 SET doc_used = doc_used + $2, updated_at = now()
		 WHERE user_id = $1
		   AND (doc_quota = 0 OR doc_used + $2 <= doc_quota)
		 RETURNING user_id`,
		userID, count,
	).Scan(&uid)

	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		_ = r.pool.QueryRow(ctx, `SELECT true FROM account_configs WHERE user_id = $1`, userID).Scan(&exists)
		if !exists {
			return domain.ErrNotFound
		}
		return domain.ErrQuotaExceeded
	}
	if err != nil {
		return fmt.Errorf("reserve docs: %w", err)
	}
	return nil
}

func (r *AccountConfigRepository) RefundDocs(ctx context.Context, userID int64, count int64) error {
	if count <= 0 {
		return nil
	}

	_, err := r.pool.Exec(ctx,
		`UPDATE account_configs
		 SET doc_used = GREATEST(0, doc_used - $2), updated_at = now()
		 WHERE user_id = $1`,
		userID, count,
	)
	if err != nil {
		return fmt.Errorf("refund docs: %w", err)
	}
	return nil
}

func (r *AccountConfigRepository) ResetDocUsed(ctx context.Context, userID int64) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE account_configs
		 SET doc_used = 0, updated_at = now()
		 WHERE user_id = $1`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("reset doc used: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
