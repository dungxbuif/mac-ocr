package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresQuotaStore is the multi-replica-safe quota store backed by PostgreSQL.
// It replaces the SQLite store when DATABASE_URL is set: quota counters live in
// one shared database, so any number of bot replicas can increment them without
// double-counting scans or racing on the daily reset. Redis (when configured)
// is layered on top purely as a short-TTL read cache to absorb quota polling;
// the database is the source of truth, mirrored by the proxy's repositories.
type PostgresQuotaStore struct {
	pool *pgxpool.Pool
}

// NewPostgresQuotaStore opens a connection pool and applies the schema. The
// schema is idempotent (CREATE TABLE IF NOT EXISTS + ALTER ... IF NOT EXISTS),
// so it runs on every start without a separate migration tool.
func NewPostgresQuotaStore(ctx context.Context, databaseURL string) (*PostgresQuotaStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	store := &PostgresQuotaStore{pool: pool}
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

const pgSchema = `
CREATE TABLE IF NOT EXISTS user_daily_scans (
    user_id    TEXT NOT NULL,
    scan_date  DATE NOT NULL,
    scan_count INTEGER NOT NULL DEFAULT 0,
    ocr_count  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, scan_date)
);

CREATE TABLE IF NOT EXISTS user_configs (
    user_id           TEXT PRIMARY KEY,
    daily_scan_limit  INTEGER NOT NULL,
    daily_ocr_limit   INTEGER NOT NULL,
    session_ask_limit INTEGER NOT NULL,
    display_name      TEXT NOT NULL DEFAULT '',
    username          TEXT NOT NULL DEFAULT '',
    clan_nick         TEXT NOT NULL DEFAULT '',
    seen_count        INTEGER NOT NULL DEFAULT 0,
    last_seen_at      TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_user_daily_scans_user_date
    ON user_daily_scans (user_id, scan_date);
`

func (s *PostgresQuotaStore) migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, pgSchema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	// Upgrade from the old single-counter schema. ADD COLUMN IF NOT EXISTS
	// is idempotent and never errors on the new shape; mirroring it back to
	// the legacy `count` is impossible (Postgres has no RENAME IF EXISTS),
	// so we leave `count` behind and copy its value once for today's rows.
	migrations := []string{
		`ALTER TABLE user_daily_scans ADD COLUMN IF NOT EXISTS scan_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE user_daily_scans ADD COLUMN IF NOT EXISTS ocr_count  INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE user_configs ADD COLUMN IF NOT EXISTS daily_ocr_limit   INTEGER NOT NULL DEFAULT 5`,
		`DO $$
		BEGIN
			IF EXISTS (
				SELECT 1 FROM information_schema.columns 
				WHERE table_name = 'user_daily_scans' AND column_name = 'count'
			) THEN
				EXECUTE 'UPDATE user_daily_scans SET scan_count = count WHERE scan_count = 0 AND count > 0';
			END IF;
		END $$;`,
	}
	for _, q := range migrations {
		if _, err := s.pool.Exec(ctx, q); err != nil {
			return fmt.Errorf("migrate split counters: %w", err)
		}
	}
	return nil
}

// UpsertUser creates or refreshes a user's config row from a seen event, port
// of an event-driven upsert. On first sight it seeds the limits from the
// defaults; on later sights it only bumps seen_count/last_seen and refreshes
// the display fields, so an admin's customised limit is never overwritten.
func (s *PostgresQuotaStore) UpsertUser(ctx context.Context, userID, displayName, username, clanNick string, defScan, defOCR, defAsk int) error {
	const q = `
	INSERT INTO user_configs
	    (user_id, daily_scan_limit, daily_ocr_limit, session_ask_limit, display_name, username, clan_nick, seen_count, last_seen_at, created_at, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, 1, now(), now(), now())
	ON CONFLICT (user_id) DO UPDATE SET
	    display_name = EXCLUDED.display_name,
	    username     = EXCLUDED.username,
	    clan_nick    = EXCLUDED.clan_nick,
	    seen_count   = user_configs.seen_count + 1,
	    last_seen_at = now(),
	    updated_at   = now()
	`
	_, err := s.pool.Exec(ctx, q, userID, defScan, defOCR, defAsk, displayName, username, clanNick)
	return err
}

func (s *PostgresQuotaStore) GetOrCreateUserConfig(userID string, defScan, defOCR, defAsk int) (int, int, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var scanLimit, ocrLimit, askLimit int
	err := s.pool.QueryRow(ctx,
		`SELECT daily_scan_limit, daily_ocr_limit, session_ask_limit FROM user_configs WHERE user_id = $1`,
		userID).Scan(&scanLimit, &ocrLimit, &askLimit)
	if err == nil {
		return scanLimit, ocrLimit, askLimit, nil
	}
	if !isNoRows(err) {
		return defScan, defOCR, defAsk, err
	}

	// First sight via the legacy GetOrCreate path: seed defaults. The
	// event-driven UpsertUser is the preferred entry point, but keep this
	// self-healing so a config miss never blocks a scan.
	_, err = s.pool.Exec(ctx, `
		INSERT INTO user_configs (user_id, daily_scan_limit, daily_ocr_limit, session_ask_limit, created_at, updated_at)
		VALUES ($1, $2, $3, $4, now(), now())
		ON CONFLICT (user_id) DO NOTHING
	`, userID, defScan, defOCR, defAsk)
	if err != nil {
		return defScan, defOCR, defAsk, err
	}
	return defScan, defOCR, defAsk, nil
}

// checkAndIncrement is the shared implementation for the *scan and *ocr
// daily counters; `column` selects which to bump. The FOR UPDATE lock
// guards against two replicas racing the same user in the same window; the
// INSERT ... ON CONFLICT upserts the row for a brand-new day.
func (s *PostgresQuotaStore) checkAndIncrement(userID, column string, dailyLimit int) (bool, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_daily_scans (user_id, scan_date, scan_count, ocr_count)
		VALUES ($1, CURRENT_DATE, 0, 0)
		ON CONFLICT (user_id, scan_date) DO NOTHING
	`, userID); err != nil {
		return false, 0, err
	}

	var currentCount int
	if err := tx.QueryRow(ctx, `
		SELECT `+column+` FROM user_daily_scans WHERE user_id = $1 AND scan_date = CURRENT_DATE FOR UPDATE
	`, userID).Scan(&currentCount); err != nil {
		return false, 0, err
	}

	if currentCount >= dailyLimit {
		return false, currentCount, nil
	}

	newCount := currentCount + 1
	if _, err := tx.Exec(ctx, `
		UPDATE user_daily_scans SET `+column+` = $2 WHERE user_id = $1 AND scan_date = CURRENT_DATE
	`, userID, newCount); err != nil {
		return false, 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, 0, err
	}
	return true, newCount, nil
}

func (s *PostgresQuotaStore) CheckAndIncrementScanQuota(userID string, dailyLimit int) (bool, int, error) {
	return s.checkAndIncrement(userID, "scan_count", dailyLimit)
}

func (s *PostgresQuotaStore) CheckAndIncrementOCRQuota(userID string, dailyLimit int) (bool, int, error) {
	return s.checkAndIncrement(userID, "ocr_count", dailyLimit)
}

func (s *PostgresQuotaStore) GetQuota(userID string, scanLimit, ocrLimit int) (int, int, int, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var scanCount, ocrCount int
	err := s.pool.QueryRow(ctx,
		`SELECT scan_count, ocr_count FROM user_daily_scans WHERE user_id = $1 AND scan_date = CURRENT_DATE`,
		userID).Scan(&scanCount, &ocrCount)
	if isNoRows(err) {
		return 0, scanLimit, 0, ocrLimit, nil
	} else if err != nil {
		return 0, 0, 0, 0, err
	}

	scanRem := scanLimit - scanCount
	ocrRem := ocrLimit - ocrCount
	if scanRem < 0 {
		scanRem = 0
	}
	if ocrRem < 0 {
		ocrRem = 0
	}
	return scanCount, scanRem, ocrCount, ocrRem, nil
}

func (s *PostgresQuotaStore) refundOne(userID, column string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := s.pool.Exec(ctx, `
		UPDATE user_daily_scans SET `+column+` = GREATEST(0, `+column+` - 1)
		WHERE user_id = $1 AND scan_date = CURRENT_DATE
	`, userID)
	return err
}

func (s *PostgresQuotaStore) RefundScanQuota(userID string) error {
	return s.refundOne(userID, "scan_count")
}

func (s *PostgresQuotaStore) RefundOCRQuota(userID string) error {
	return s.refundOne(userID, "ocr_count")
}

func (s *PostgresQuotaStore) Close() error {
	s.pool.Close()
	return nil
}

// isNoRows reports whether err is a pgx "no rows" result, without importing the
// pgx errcode package (keeps the import surface tiny).
func isNoRows(err error) bool {
	return err != nil && err.Error() == "no rows in result set"
}
