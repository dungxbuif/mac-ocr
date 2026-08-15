package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresSessionStore implements SessionStore on top of PostgreSQL, keeping
// Q&A context alive across multiple pods without sticky routing. No in-process
// mutex is needed because PostgreSQL row-level locks guarantee safe concurrent
// ask counting.
type PostgresSessionStore struct {
	pool *pgxpool.Pool
}

func NewPostgresSessionStore(ctx context.Context, connString string) (*PostgresSessionStore, error) {
	cfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("parse postgres session url: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres sessions: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres sessions: %w", err)
	}

	store := &PostgresSessionStore{pool: pool}
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate session schema: %w", err)
	}
	return store, nil
}

const pgSessionSchema = `
CREATE TABLE IF NOT EXISTS scan_sessions (
    session_id  TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL,
    document_id TEXT NOT NULL,
    doc_type    TEXT NOT NULL,
    ocr_text    TEXT NOT NULL,
    ask_count   INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS user_session_asks (
    session_id  TEXT NOT NULL,
    user_id     TEXT NOT NULL,
    ask_count   INTEGER NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_scan_sessions_user ON scan_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_scan_sessions_created ON scan_sessions(created_at);
CREATE INDEX IF NOT EXISTS idx_user_session_asks_user ON user_session_asks(user_id);
`

func (s *PostgresSessionStore) migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, pgSessionSchema); err != nil {
		return fmt.Errorf("apply session schema: %w", err)
	}
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM scan_sessions WHERE created_at < now() - INTERVAL '24 hours'`); err != nil {
		return fmt.Errorf("sweep stale sessions: %w", err)
	}
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM user_session_asks WHERE updated_at < now() - INTERVAL '24 hours'`); err != nil {
		return fmt.Errorf("sweep stale user asks: %w", err)
	}
	return nil
}

func (s *PostgresSessionStore) CreateSession(sessionID, userID, documentID, docType, ocrText string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	const q = `
	INSERT INTO scan_sessions (session_id, user_id, document_id, doc_type, ocr_text, ask_count, created_at)
	VALUES ($1, $2, $3, $4, $5, 0, now())
	ON CONFLICT (session_id) DO UPDATE SET ocr_text = EXCLUDED.ocr_text, doc_type = EXCLUDED.doc_type
	`
	_, err := s.pool.Exec(ctx, q, sessionID, userID, documentID, docType, ocrText)
	return err
}

func (s *PostgresSessionStore) GetSession(sessionID string) (*ScanSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var sess ScanSession
	err := s.pool.QueryRow(ctx, `
		SELECT session_id, user_id, document_id, doc_type, ocr_text, ask_count, created_at
		FROM scan_sessions WHERE session_id = $1
	`, sessionID).Scan(&sess.SessionID, &sess.UserID, &sess.DocumentID, &sess.DocType, &sess.OCRText, &sess.AskCount, &sess.CreatedAt)
	if isNoRows(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &sess, nil
}

// CheckAndIncrementAskQuota checks and increments the ask count for the asking user
// on a given session. This allows other users in the channel to also ask questions
// without exhausting the original scanner's quota.
func (s *PostgresSessionStore) CheckAndIncrementAskQuota(sessionID, userID string, maxAsks int) (bool, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback(ctx)

	// Verify session exists
	var dummy string
	err = tx.QueryRow(ctx, `SELECT session_id FROM scan_sessions WHERE session_id = $1`, sessionID).Scan(&dummy)
	if isNoRows(err) {
		return false, 0, nil
	} else if err != nil {
		return false, 0, err
	}

	var userAskCount int
	err = tx.QueryRow(ctx, `
		SELECT ask_count FROM user_session_asks WHERE session_id = $1 AND user_id = $2 FOR UPDATE
	`, sessionID, userID).Scan(&userAskCount)
	if isNoRows(err) {
		userAskCount = 0
	} else if err != nil {
		return false, 0, err
	}

	if userAskCount >= maxAsks {
		return false, userAskCount, nil
	}

	newAsk := userAskCount + 1
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_session_asks (session_id, user_id, ask_count, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (session_id, user_id) DO UPDATE SET ask_count = $3, updated_at = now()
	`, sessionID, userID, newAsk); err != nil {
		return false, 0, err
	}

	// Update overall session count for reporting
	_, _ = tx.Exec(ctx, `UPDATE scan_sessions SET ask_count = ask_count + 1 WHERE session_id = $1`, sessionID)

	if err := tx.Commit(ctx); err != nil {
		return false, 0, err
	}
	return true, newAsk, nil
}

func (s *PostgresSessionStore) DeleteSession(sessionID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = s.pool.Exec(ctx, `DELETE FROM user_session_asks WHERE session_id = $1`, sessionID)
	_, err := s.pool.Exec(ctx, `DELETE FROM scan_sessions WHERE session_id = $1`, sessionID)
	return err
}

func (s *PostgresSessionStore) Close() error { s.pool.Close(); return nil }
