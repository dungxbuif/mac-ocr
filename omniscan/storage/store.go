package storage

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type UserConfig struct {
	UserID          string
	DailyScanLimit  int
	SessionAskLimit int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type QuotaStore interface {
	GetOrCreateUserConfig(userID string, defaultScanLimit, defaultAskLimit int) (dailyScanLimit int, sessionAskLimit int, err error)
	CheckAndIncrementQuota(userID string, dailyLimit int) (allowed bool, currentCount int, err error)
	GetQuota(userID string, dailyLimit int) (used int, remaining int, err error)
	RefundQuota(userID string) error
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
		user_id TEXT NOT NULL,
		scan_date TEXT NOT NULL,
		count INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (user_id, scan_date)
	);

	CREATE TABLE IF NOT EXISTS user_configs (
		user_id TEXT PRIMARY KEY,
		daily_scan_limit INTEGER NOT NULL,
		session_ask_limit INTEGER NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create user tables: %w", err)
	}

	return &SQLiteQuotaStore{db: db}, nil
}

func (s *SQLiteQuotaStore) GetOrCreateUserConfig(userID string, defaultScanLimit, defaultAskLimit int) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	// Insert default config if user does not exist
	insertQuery := `
	INSERT INTO user_configs (user_id, daily_scan_limit, session_ask_limit, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(user_id) DO NOTHING;
	`
	_, _ = s.db.Exec(insertQuery, userID, defaultScanLimit, defaultAskLimit, now, now)

	// Fetch current user config from DB
	var scanLimit, askLimit int
	query := `SELECT daily_scan_limit, session_ask_limit FROM user_configs WHERE user_id = ?`
	err := s.db.QueryRow(query, userID).Scan(&scanLimit, &askLimit)
	if err != nil {
		return defaultScanLimit, defaultAskLimit, err
	}

	return scanLimit, askLimit, nil
}

func (s *SQLiteQuotaStore) CheckAndIncrementQuota(userID string, dailyLimit int) (bool, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	today := time.Now().Format("2006-01-02")

	tx, err := s.db.Begin()
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()

	var currentCount int
	err = tx.QueryRow("SELECT count FROM user_daily_scans WHERE user_id = ? AND scan_date = ?", userID, today).Scan(&currentCount)
	if err == sql.ErrNoRows {
		currentCount = 0
	} else if err != nil {
		return false, 0, err
	}

	if currentCount >= dailyLimit {
		return false, currentCount, nil
	}

	newCount := currentCount + 1
	_, err = tx.Exec(`
		INSERT INTO user_daily_scans (user_id, scan_date, count)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id, scan_date) DO UPDATE SET count = ?
	`, userID, today, newCount, newCount)

	if err != nil {
		return false, 0, err
	}

	if err := tx.Commit(); err != nil {
		return false, 0, err
	}

	return true, newCount, nil
}

func (s *SQLiteQuotaStore) GetQuota(userID string, dailyLimit int) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	var count int
	err := s.db.QueryRow("SELECT count FROM user_daily_scans WHERE user_id = ? AND scan_date = ?", userID, today).Scan(&count)
	if err == sql.ErrNoRows {
		count = 0
	} else if err != nil {
		return 0, 0, err
	}

	remaining := dailyLimit - count
	if remaining < 0 {
		remaining = 0
	}
	return count, remaining, nil
}

func (s *SQLiteQuotaStore) RefundQuota(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	_, err := s.db.Exec(`
		UPDATE user_daily_scans SET count = MAX(0, count - 1)
		WHERE user_id = ? AND scan_date = ?
	`, userID, today)
	return err
}

func (s *SQLiteQuotaStore) Close() error {
	return s.db.Close()
}
