package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresSessionStore persists threaded Q&A sessions in PostgreSQL so they
// survive across replicas. It mirrors the SQLite store but drops the
// in-process mutex (Postgres row locks make CheckAndIncrementAskQuota safe)
// and the goroutine cleanup loop (replaced by a 24h TTL sweep query).
type PostgresSessionStore struct {
	pool *pgxpool.Pool
}

func NewPostgresSessionStore(ctx context.Context, databaseURL string) (*PostgresSessionStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres session pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	store := &PostgresSessionStore{pool: pool}
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
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
CREATE INDEX IF NOT EXISTS idx_scan_sessions_user ON scan_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_scan_sessions_created ON scan_sessions(created_at);
`

func (s *PostgresSessionStore) migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, pgSessionSchema); err != nil {
		return fmt.Errorf("apply session schema: %w", err)
	}
	// Sweep stale rows on every start, port of the SQLite hourly cleanup
	// collapsed into one shot: a periodic sweeper would still help, but this
	// bounds unbounded growth at least across restarts.
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM scan_sessions WHERE created_at < now() - INTERVAL '24 hours'`); err != nil {
		return fmt.Errorf("sweep stale sessions: %w", err)
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

func (s *PostgresSessionStore) CheckAndIncrementAskQuota(sessionID string, maxAsks int) (bool, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback(ctx)

	var askCount int
	err = tx.QueryRow(ctx, `SELECT ask_count FROM scan_sessions WHERE session_id = $1 FOR UPDATE`,
		sessionID).Scan(&askCount)
	if isNoRows(err) {
		return false, 0, nil
	} else if err != nil {
		return false, 0, err
	}

	if askCount >= maxAsks {
		// Session completed max asks: purge OCR text immediately for privacy,
		// mirroring the SQLite store's behaviour.
		if _, err := tx.Exec(ctx, `DELETE FROM scan_sessions WHERE session_id = $1`, sessionID); err != nil {
			return false, 0, err
		}
		if err := tx.Commit(ctx); err != nil {
			return false, 0, err
		}
		return false, askCount, nil
	}

	newAsk := askCount + 1
	if _, err := tx.Exec(ctx, `UPDATE scan_sessions SET ask_count = $2 WHERE session_id = $1`,
		sessionID, newAsk); err != nil {
		return false, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, 0, err
	}
	return true, newAsk, nil
}

func (s *PostgresSessionStore) DeleteSession(sessionID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := s.pool.Exec(ctx, `DELETE FROM scan_sessions WHERE session_id = $1`, sessionID)
	return err
}

func (s *PostgresSessionStore) Close() error { s.pool.Close(); return nil }
