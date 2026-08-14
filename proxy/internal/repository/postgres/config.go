package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

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
		`SELECT user_id, rate_limit_rpm, doc_quota, doc_used,
		        storage_quota_bytes, storage_used_bytes, storage_reserved_bytes,
		        quota_reset_at, updated_at, updated_by
		 FROM account_configs WHERE user_id = $1`, userID,
	).Scan(&cfg.UserID, &cfg.RateLimitRPM, &cfg.DocQuota, &cfg.DocUsed,
		&cfg.StorageQuotaBytes, &cfg.StorageUsedBytes, &cfg.StorageReservedBytes,
		&cfg.QuotaResetAt, &cfg.UpdatedAt, &cfg.UpdatedBy)
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
		 SET rate_limit_rpm = $1, doc_quota = $2, storage_quota_bytes = $3,
		     quota_reset_at = $4, updated_by = $5, updated_at = now()
		 WHERE user_id = $6
		 RETURNING user_id, rate_limit_rpm, doc_quota, doc_used,
		           storage_quota_bytes, storage_used_bytes, storage_reserved_bytes,
		           quota_reset_at, updated_at, updated_by`,
		cfg.RateLimitRPM, cfg.DocQuota, cfg.StorageQuotaBytes, cfg.QuotaResetAt, cfg.UpdatedBy, cfg.UserID,
	).Scan(&out.UserID, &out.RateLimitRPM, &out.DocQuota, &out.DocUsed,
		&out.StorageQuotaBytes, &out.StorageUsedBytes, &out.StorageReservedBytes,
		&out.QuotaResetAt, &out.UpdatedAt, &out.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update account_config: %w", err)
	}
	return &out, nil
}

func (r *AccountConfigRepository) ReserveUpload(ctx context.Context, reservation domain.UploadReservation) error {
	if reservation.UserID <= 0 || reservation.ObjectKey == "" || reservation.SizeBytes <= 0 || reservation.ExpiresAt.IsZero() {
		return domain.ErrBadParamInput
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin upload reservation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `UPDATE account_configs
		SET storage_reserved_bytes = storage_reserved_bytes + $2, updated_at = now()
		WHERE user_id = $1
		  AND (storage_quota_bytes = 0 OR storage_used_bytes + storage_reserved_bytes + $2 <= storage_quota_bytes)`,
		reservation.UserID, reservation.SizeBytes)
	if err != nil {
		return fmt.Errorf("reserve upload bytes: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		_ = tx.QueryRow(ctx, `SELECT true FROM account_configs WHERE user_id=$1`, reservation.UserID).Scan(&exists)
		if !exists {
			return domain.ErrNotFound
		}
		return domain.ErrStorageQuotaExceeded
	}
	_, err = tx.Exec(ctx, `INSERT INTO upload_reservations (object_key, user_id, size_bytes, expires_at)
		VALUES ($1, $2, $3, $4)`, reservation.ObjectKey, reservation.UserID, reservation.SizeBytes, reservation.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert upload reservation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit upload reservation: %w", err)
	}
	return nil
}

func (r *AccountConfigRepository) ReleaseUpload(ctx context.Context, userID int64, objectKey string) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin upload release: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Keep the same lock order as document submission and upload reservation:
	// account config first, reservation second. This avoids a config/reservation
	// lock inversion when expiry cleanup races an upload submission.
	var accountExists bool
	err = tx.QueryRow(ctx, `SELECT true FROM account_configs WHERE user_id=$1 FOR UPDATE`, userID).Scan(&accountExists)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, domain.ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("lock account for upload release: %w", err)
	}
	var size int64
	err = tx.QueryRow(ctx, `DELETE FROM upload_reservations
		WHERE object_key=$1 AND user_id=$2 AND state='reserved' RETURNING size_bytes`, objectKey, userID).Scan(&size)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("delete upload reservation: %w", err)
	}
	_, err = tx.Exec(ctx, `UPDATE account_configs
		SET storage_reserved_bytes=GREATEST(0, storage_reserved_bytes-$2), updated_at=now()
		WHERE user_id=$1`, userID, size)
	if err != nil {
		return false, fmt.Errorf("refund upload reservation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit upload release: %w", err)
	}
	return true, nil
}

func (r *AccountConfigRepository) ListExpiredUploads(ctx context.Context, before time.Time, limit int) ([]domain.UploadReservation, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `SELECT object_key, user_id, size_bytes, expires_at, created_at
		FROM upload_reservations WHERE state='reserved' AND expires_at <= $1
		ORDER BY expires_at ASC LIMIT $2`, before, limit)
	if err != nil {
		return nil, fmt.Errorf("list expired upload reservations: %w", err)
	}
	defer rows.Close()
	var reservations []domain.UploadReservation
	for rows.Next() {
		var item domain.UploadReservation
		if err := rows.Scan(&item.ObjectKey, &item.UserID, &item.SizeBytes, &item.ExpiresAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		reservations = append(reservations, item)
	}
	return reservations, rows.Err()
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
