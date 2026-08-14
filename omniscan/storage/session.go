package storage

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type ScanSession struct {
	SessionID  string // Bot reply message_id
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
	CheckAndIncrementAskQuota(sessionID string, maxAsks int) (allowed bool, currentAsk int, err error)
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

func (s *SQLiteSessionStore) CheckAndIncrementAskQuota(sessionID string, maxAsks int) (bool, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()

	var askCount int
	err = tx.QueryRow("SELECT ask_count FROM scan_sessions WHERE session_id = ?", sessionID).Scan(&askCount)
	if err == sql.ErrNoRows {
		return false, 0, nil
	} else if err != nil {
		return false, 0, err
	}

	if askCount >= maxAsks {
		// Session completed max asks -> purge session data immediately for privacy
		_, _ = tx.Exec("DELETE FROM scan_sessions WHERE session_id = ?", sessionID)
		_ = tx.Commit()
		return false, askCount, nil
	}

	newAsk := askCount + 1
	_, err = tx.Exec("UPDATE scan_sessions SET ask_count = ? WHERE session_id = ?", newAsk, sessionID)
	if err != nil {
		return false, 0, err
	}

	if err := tx.Commit(); err != nil {
		return false, 0, err
	}

	return true, newAsk, nil
}

func (s *SQLiteSessionStore) DeleteSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec("DELETE FROM scan_sessions WHERE session_id = ?", sessionID)
	return err
}

func (s *SQLiteSessionStore) startAutoCleanup(ttl time.Duration) {
	ticker := time.NewTicker(1 * time.Hour)
	for range ticker.C {
		s.mu.Lock()
		cutoff := time.Now().Add(-ttl)
		_, _ = s.db.Exec("DELETE FROM scan_sessions WHERE created_at < ?", cutoff)
		s.mu.Unlock()
	}
}

func (s *SQLiteSessionStore) Close() error {
	return s.db.Close()
}
