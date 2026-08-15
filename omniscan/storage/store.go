package storage

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type UserConfig struct {
	UserID           string
	DailyScanLimit   int
	DailyOCRLimit    int
	SessionAskLimit int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type QuotaStore interface {
	GetOrCreateUserConfig(userID string, defScan, defOCR, defAsk int) (scanLimit, ocrLimit, askLimit int, err error)
	CheckAndIncrementScanQuota(userID string, dailyLimit int) (allowed bool, currentCount int, err error)
	CheckAndIncrementOCRQuota(userID string, dailyLimit int) (allowed bool, currentCount int, err error)
	GetQuota(userID string, scanLimit, ocrLimit int) (scanUsed, scanRem, ocrUsed, ocrRem int, err error)
	RefundScanQuota(userID string) error
	RefundOCRQuota(userID string) error
	Close() error
}

type SQLiteQuotaStore struct {
	db *sql.DB
	mu sync.Mutex
}

func NewSQLiteQuotaStore(dbPath string) (*SQLiteQuotaStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	schema := `
	CREATE TABLE IF NOT EXISTS user_daily_scans (
		user_id    TEXT NOT NULL,
		scan_date  TEXT NOT NULL,
		scan_count INTEGER NOT NULL DEFAULT 0,
		ocr_count  INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (user_id, scan_date)
	);

	CREATE TABLE IF NOT EXISTS user_configs (
		user_id           TEXT PRIMARY KEY,
		daily_scan_limit  INTEGER NOT NULL,
		daily_ocr_limit   INTEGER NOT NULL,
		session_ask_limit INTEGER NOT NULL,
		created_at        DATETIME NOT NULL,
		updated_at        DATETIME NOT NULL
	);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create user tables: %w", err)
	}

	return &SQLiteQuotaStore{db: db}, nil
}

func (s *SQLiteQuotaStore) GetOrCreateUserConfig(userID string, defScan, defOCR, defAsk int) (int, int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	// Seed per-user limits with env defaults on first sight. Admin overrides
	// persist in the row and are never clobbered here (ON CONFLICT DO NOTHING).
	insertQuery := `
	INSERT INTO user_configs (user_id, daily_scan_limit, daily_ocr_limit, session_ask_limit, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(user_id) DO NOTHING;
	`
	_, _ = s.db.Exec(insertQuery, userID, defScan, defOCR, defAsk, now, now)

	var scanLimit, ocrLimit, askLimit int
	query := `SELECT daily_scan_limit, daily_ocr_limit, session_ask_limit FROM user_configs WHERE user_id = ?`
	err := s.db.QueryRow(query, userID).Scan(&scanLimit, &ocrLimit, &askLimit)
	if err != nil {
		return defScan, defOCR, defAsk, err
	}
	return scanLimit, ocrLimit, askLimit, nil
}

// checkAndIncrement is the shared implementation for the *scan and *ocr
// counters; `column` selects which daily counter to bump. The mutex guards
// the full transaction so concurrent replicas cannot race a row.
func (s *SQLiteQuotaStore) checkAndIncrement(userID, column string, dailyLimit int) (bool, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	tx, err := s.db.Begin()
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()

	var currentCount int
	query := "SELECT " + column + " FROM user_daily_scans WHERE user_id = ? AND scan_date = ?"
	err = tx.QueryRow(query, userID, today).Scan(&currentCount)
	if err == sql.ErrNoRows {
		currentCount = 0
	} else if err != nil {
		return false, 0, err
	}

	if currentCount >= dailyLimit {
		return false, currentCount, nil
	}

	newCount := currentCount + 1
	// On a brand-new row seed the chosen counter with newCount and the other
	// with 0; ON CONFLICT bumps only the chosen column to newCount.
	var scanInit, ocrInit int
	if column == "scan_count" {
		scanInit, ocrInit = newCount, 0
	} else {
		scanInit, ocrInit = 0, newCount
	}
	upsert := `INSERT INTO user_daily_scans (user_id, scan_date, scan_count, ocr_count) VALUES (?, ?, ?, ?) ON CONFLICT(user_id, scan_date) DO UPDATE SET ` + column + " = ?"
	_, err = tx.Exec(upsert, userID, today, scanInit, ocrInit, newCount)
	if err != nil {
		return false, 0, err
	}
	if err := tx.Commit(); err != nil {
		return false, 0, err
	}
	return true, newCount, nil
}

func (s *SQLiteQuotaStore) CheckAndIncrementScanQuota(userID string, dailyLimit int) (bool, int, error) {
	return s.checkAndIncrement(userID, "scan_count", dailyLimit)
}

func (s *SQLiteQuotaStore) CheckAndIncrementOCRQuota(userID string, dailyLimit int) (bool, int, error) {
	return s.checkAndIncrement(userID, "ocr_count", dailyLimit)
}

func (s *SQLiteQuotaStore) GetQuota(userID string, scanLimit, ocrLimit int) (int, int, int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	var scanCount, ocrCount int
	err := s.db.QueryRow("SELECT scan_count, ocr_count FROM user_daily_scans WHERE user_id = ? AND scan_date = ?", userID, today).Scan(&scanCount, &ocrCount)
	if err == sql.ErrNoRows {
		scanCount, ocrCount = 0, 0
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

func (s *SQLiteQuotaStore) refundOne(userID, column string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	stmt := "UPDATE user_daily_scans SET " + column + " = MAX(0, " + column + " - 1) WHERE user_id = ? AND scan_date = ?"
	_, err := s.db.Exec(stmt, userID, today)
	return err
}

func (s *SQLiteQuotaStore) RefundScanQuota(userID string) error {
	return s.refundOne(userID, "scan_count")
}

func (s *SQLiteQuotaStore) RefundOCRQuota(userID string) error {
	return s.refundOne(userID, "ocr_count")
}

func (s *SQLiteQuotaStore) Close() error {
	return s.db.Close()
}
