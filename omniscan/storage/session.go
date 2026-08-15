package storage

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type ScanSession struct {
	SessionID  string
	UserID     string
	DocumentID string
	DocType    string
	OCRText    string
	AskCount   int
	CreatedAt  time.Time
}

type SessionStore interface {
	CreateSession(sessionID, userID, documentID, docType, ocrText string) error
	GetSession(sessionID string) (*ScanSession, error)
	CheckAndIncrementAskQuota(sessionID, userID string, maxAsks int) (allowed bool, currentAsk int, err error)
	DeleteSession(sessionID string) error
	Close() error
}

type SQLiteSessionStore struct {
	db *sql.DB
	mu sync.Mutex
}

func NewSQLiteSessionStore(dbPath string) (*SQLiteSessionStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite for sessions: %w", err)
	}

	schema := `
	CREATE TABLE IF NOT EXISTS scan_sessions (
		session_id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		document_id TEXT NOT NULL,
		doc_type TEXT NOT NULL,
		ocr_text TEXT NOT NULL,
		ask_count INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL
	);
	CREATE TABLE IF NOT EXISTS user_session_asks (
		session_id TEXT NOT NULL,
		user_id TEXT NOT NULL,
		ask_count INTEGER NOT NULL DEFAULT 0,
		updated_at DATETIME NOT NULL,
		PRIMARY KEY (session_id, user_id)
	);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create scan_sessions table: %w", err)
	}

	store := &SQLiteSessionStore{db: db}
	go store.startAutoCleanup(24 * time.Hour)
	return store, nil
}

func (s *SQLiteSessionStore) CreateSession(sessionID, userID, documentID, docType, ocrText string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	INSERT INTO scan_sessions (session_id, user_id, document_id, doc_type, ocr_text, ask_count, created_at)
	VALUES (?, ?, ?, ?, ?, 0, ?)
	ON CONFLICT(session_id) DO UPDATE SET ocr_text = ?, doc_type = ?
	`
	_, err := s.db.Exec(query, sessionID, userID, documentID, docType, ocrText, time.Now(), ocrText, docType)
	return err
}

func (s *SQLiteSessionStore) GetSession(sessionID string) (*ScanSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `SELECT session_id, user_id, document_id, doc_type, ocr_text, ask_count, created_at FROM scan_sessions WHERE session_id = ?`
	var sess ScanSession
	err := s.db.QueryRow(query, sessionID).Scan(&sess.SessionID, &sess.UserID, &sess.DocumentID, &sess.DocType, &sess.OCRText, &sess.AskCount, &sess.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &sess, nil
}

// CheckAndIncrementAskQuota checks and increments the ask count for a specific user
// on a given session. This allows other users in the channel to also ask questions
// on a scanned document without exhausting or affecting the original owner's quota.
func (s *SQLiteSessionStore) CheckAndIncrementAskQuota(sessionID, userID string, maxAsks int) (bool, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()

	// Verify session exists
	var dummy string
	err = tx.QueryRow("SELECT session_id FROM scan_sessions WHERE session_id = ?", sessionID).Scan(&dummy)
	if err == sql.ErrNoRows {
		return false, 0, nil
	} else if err != nil {
		return false, 0, err
	}

	var userAskCount int
	err = tx.QueryRow("SELECT ask_count FROM user_session_asks WHERE session_id = ? AND user_id = ?", sessionID, userID).Scan(&userAskCount)
	if err == sql.ErrNoRows {
		userAskCount = 0
	} else if err != nil {
		return false, 0, err
	}

	if userAskCount >= maxAsks {
		return false, userAskCount, nil
	}

	newAsk := userAskCount + 1
	_, err = tx.Exec(`
		INSERT INTO user_session_asks (session_id, user_id, ask_count, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id, user_id) DO UPDATE SET ask_count = ?, updated_at = ?
	`, sessionID, userID, newAsk, time.Now(), newAsk, time.Now())
	if err != nil {
		return false, 0, err
	}

	// Update overall session counter too
	_, _ = tx.Exec("UPDATE scan_sessions SET ask_count = ask_count + 1 WHERE session_id = ?", sessionID)

	if err := tx.Commit(); err != nil {
		return false, 0, err
	}

	return true, newAsk, nil
}

func (s *SQLiteSessionStore) DeleteSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, _ = s.db.Exec("DELETE FROM user_session_asks WHERE session_id = ?", sessionID)
	_, err := s.db.Exec("DELETE FROM scan_sessions WHERE session_id = ?", sessionID)
	return err
}

func (s *SQLiteSessionStore) startAutoCleanup(ttl time.Duration) {
	ticker := time.NewTicker(1 * time.Hour)
	for range ticker.C {
		s.mu.Lock()
		cutoff := time.Now().Add(-ttl)
		_, _ = s.db.Exec("DELETE FROM scan_sessions WHERE created_at < ?", cutoff)
		_, _ = s.db.Exec("DELETE FROM user_session_asks WHERE updated_at < ?", cutoff)
		s.mu.Unlock()
	}
}

func (s *SQLiteSessionStore) Close() error {
	return s.db.Close()
}
