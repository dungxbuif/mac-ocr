package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"macocr/proxy/domain"
	"macocr/proxy/internal/notifications"
)

type DocumentRepository struct {
	pool   *pgxpool.Pool
	cipher *notifications.SecretCipher
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func NewDocumentRepository(pool *pgxpool.Pool, cipher *notifications.SecretCipher) *DocumentRepository {
	return &DocumentRepository{pool: pool, cipher: cipher}
}

func (r *DocumentRepository) Create(ctx context.Context, doc *domain.Document) (*domain.Document, error) {
	return createDocument(ctx, r.pool, r.cipher, doc)
}

func (r *DocumentRepository) CreateWithQuota(ctx context.Context, doc *domain.Document) (*domain.Document, error) {
	docs, err := r.CreateManyWithQuota(ctx, doc.UserID, []domain.Document{*doc})
	if err != nil {
		return nil, err
	}
	return &docs[0], nil
}

func (r *DocumentRepository) CreateManyWithQuota(ctx context.Context, userID int64, docs []domain.Document) ([]domain.Document, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin quota/document transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE account_configs SET doc_used = doc_used + $2, updated_at = now()
		WHERE user_id = $1 AND (doc_quota = 0 OR doc_used + $2 <= doc_quota)`, userID, len(docs))
	if err != nil {
		return nil, fmt.Errorf("reserve document quota: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, domain.ErrQuotaExceeded
	}
	created := make([]domain.Document, 0, len(docs))
	for i := range docs {
		if docs[i].UserID != userID {
			return nil, domain.ErrBadParamInput
		}
		doc, err := createDocument(ctx, tx, r.cipher, &docs[i])
		if err != nil {
			return nil, fmt.Errorf("insert document %d: %w", i, err)
		}
		created = append(created, *doc)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit quota/document transaction: %w", err)
	}
	return created, nil
}

func (r *DocumentRepository) CreateMany(ctx context.Context, docs []domain.Document) ([]domain.Document, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin document transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	createdDocs := make([]domain.Document, 0, len(docs))
	for i := range docs {
		created, err := createDocument(ctx, tx, r.cipher, &docs[i])
		if err != nil {
			return nil, fmt.Errorf("insert document %d: %w", i, err)
		}
		createdDocs = append(createdDocs, *created)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit document transaction: %w", err)
	}
	return createdDocs, nil
}

func createDocument(ctx context.Context, db rowQuerier, secretCipher *notifications.SecretCipher, doc *domain.Document) (*domain.Document, error) {
	var optsJSON []byte
	if doc.Options != nil {
		b, err := json.Marshal(doc.Options)
		if err == nil {
			optsJSON = b
		}
	}

	var out domain.Document
	var outOptsJSON []byte
	var statusStr string
	var resultKey, resultText, errorDetail, attemptID *string
	var notificationType, notificationURL *string
	var notificationSecret []byte
	if doc.Notification != nil && doc.Notification.Secret != "" {
		var err error
		notificationSecret, err = secretCipher.Encrypt(doc.Notification.Secret)
		if err != nil {
			return nil, fmt.Errorf("encrypt notification secret: %w", err)
		}
	}

	err := db.QueryRow(ctx,
		`INSERT INTO documents (
			id, user_id, status,
			input_key, input_sha256, input_content_type, input_size_bytes, options_json,
			result_key, result_text, error_detail, attempt_id,
			notification_type, notification_url, notification_secret, result_expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id, user_id, status,
		          input_key, input_sha256, input_content_type, input_size_bytes, options_json,
		          result_key, result_text, error_detail, attempt_id,
		          notification_type, notification_url, notification_secret, result_expires_at,
		          created_at, updated_at`,
		doc.ID, doc.UserID, string(doc.Status),
		doc.InputKey, doc.InputSHA256, doc.InputContentType, doc.InputSizeBytes, optsJSON,
		nilIfEmpty(doc.ResultKey), nilIfEmpty(doc.ResultText), nilIfEmpty(doc.ErrorDetail),
		nilIfEmpty(doc.AttemptID), notificationTypeValue(doc.Notification), notificationURLValue(doc.Notification), notificationSecret, doc.ResultExpiresAt,
	).Scan(
		&out.ID, &out.UserID, &statusStr,
		&out.InputKey, &out.InputSHA256, &out.InputContentType, &out.InputSizeBytes, &outOptsJSON,
		&resultKey, &resultText, &errorDetail, &attemptID,
		&notificationType, &notificationURL, &notificationSecret, &out.ResultExpiresAt,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert document: %w", err)
	}

	out.Status = domain.DocumentStatus(statusStr)
	out.ResultKey = stringVal(resultKey)
	out.ResultText = stringVal(resultText)
	out.ErrorDetail = stringVal(errorDetail)
	out.AttemptID = stringVal(attemptID)
	if err := setNotification(&out, notificationType, notificationURL, notificationSecret, secretCipher); err != nil {
		return nil, err
	}
	if len(outOptsJSON) > 0 {
		var opts domain.OCROptions
		if err := json.Unmarshal(outOptsJSON, &opts); err == nil {
			out.Options = &opts
		}
	}
	return &out, nil
}

func (r *DocumentRepository) GetByID(ctx context.Context, id string) (*domain.Document, error) {
	return getDocumentByID(ctx, r.pool, id, r.cipher)
}

func getDocumentByID(ctx context.Context, db rowQuerier, id string, cipher *notifications.SecretCipher) (*domain.Document, error) {
	var out domain.Document
	var outOptsJSON []byte
	var statusStr string
	var resultKey, resultText, errorDetail, attemptID, terminalEventID *string
	var notificationType, notificationURL *string
	var notificationSecret []byte

	err := db.QueryRow(ctx,
		`SELECT id, user_id, status,
		        COALESCE(input_key, ''), COALESCE(input_sha256, ''), COALESCE(input_content_type, ''), COALESCE(input_size_bytes, 0), options_json,
		        result_key, result_text, error_detail, attempt_id, attempt_count, processing_until, terminal_event_id,
		        notification_type, notification_url, notification_secret, result_expires_at,
		        created_at, updated_at
		 FROM documents WHERE id = $1`, id,
	).Scan(
		&out.ID, &out.UserID, &statusStr,
		&out.InputKey, &out.InputSHA256, &out.InputContentType, &out.InputSizeBytes, &outOptsJSON,
		&resultKey, &resultText, &errorDetail, &attemptID, &out.AttemptCount, &out.ProcessingUntil, &terminalEventID,
		&notificationType, &notificationURL, &notificationSecret, &out.ResultExpiresAt,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select document: %w", err)
	}

	out.Status = domain.DocumentStatus(statusStr)
	out.ResultKey = stringVal(resultKey)
	out.ResultText = stringVal(resultText)
	out.ErrorDetail = stringVal(errorDetail)
	out.AttemptID = stringVal(attemptID)
	out.TerminalEventID = stringVal(terminalEventID)
	if err := setNotification(&out, notificationType, notificationURL, notificationSecret, cipher); err != nil {
		return nil, err
	}
	if len(outOptsJSON) > 0 {
		var opts domain.OCROptions
		if err := json.Unmarshal(outOptsJSON, &opts); err == nil {
			out.Options = &opts
		}
	}
	return &out, nil
}

func (r *DocumentRepository) ListByUser(ctx context.Context, userID int64, status domain.DocumentStatus, limit, offset int) ([]domain.Document, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	query := `SELECT id, user_id, status,
	                 COALESCE(input_key, ''), COALESCE(input_sha256, ''), COALESCE(input_content_type, ''), COALESCE(input_size_bytes, 0), options_json,
	                 result_key, result_text, error_detail, attempt_id, result_expires_at,
	                 created_at, updated_at
	          FROM documents WHERE true`
	args := []any{}

	// A zero user ID is an internal admin-only sentinel. Public callers always
	// pass the authenticated positive user ID and remain tenant-isolated.
	if userID > 0 {
		args = append(args, userID)
		query += fmt.Sprintf(` AND user_id = $%d`, len(args))
	}

	if status != "" {
		args = append(args, string(status))
		query += fmt.Sprintf(` AND status = $%d`, len(args))
	}

	query += fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)
	args = append(args, limit, offset)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	defer rows.Close()

	docs := []domain.Document{}
	for rows.Next() {
		var d domain.Document
		var outOptsJSON []byte
		var statusStr string
		var resultKey, resultText, errorDetail, attemptID *string

		if err := rows.Scan(
			&d.ID, &d.UserID, &statusStr,
			&d.InputKey, &d.InputSHA256, &d.InputContentType, &d.InputSizeBytes, &outOptsJSON,
			&resultKey, &resultText, &errorDetail, &attemptID, &d.ResultExpiresAt,
			&d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan document: %w", err)
		}

		d.Status = domain.DocumentStatus(statusStr)
		d.ResultKey = stringVal(resultKey)
		d.ResultText = stringVal(resultText)
		d.ErrorDetail = stringVal(errorDetail)
		d.AttemptID = stringVal(attemptID)
		if len(outOptsJSON) > 0 {
			var opts domain.OCROptions
			if err := json.Unmarshal(outOptsJSON, &opts); err == nil {
				d.Options = &opts
			}
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

func (r *DocumentRepository) UpdateStatus(ctx context.Context, id string, status domain.DocumentStatus, attemptID, resultKey, resultText, errorDetail string, resultExpiresAt *time.Time) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE documents
		 SET status = $2, attempt_id = COALESCE(NULLIF($3, ''), attempt_id),
		     result_key = COALESCE(NULLIF($4, ''), result_key),
		     result_text = COALESCE(NULLIF($5, ''), result_text),
		     error_detail = COALESCE(NULLIF($6, ''), error_detail),
		     result_expires_at = COALESCE($7, result_expires_at),
		     updated_at = now()
		 WHERE id = $1`,
		id, string(status), attemptID, resultKey, resultText, errorDetail, resultExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("update document status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *DocumentRepository) Cancel(ctx context.Context, id string, userID int64) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE documents
		 SET status = 'cancelled', updated_at = now()
		 WHERE id = $1 AND user_id = $2 AND status = 'queued'`,
		id, userID,
	)
	if err != nil {
		return fmt.Errorf("cancel document: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrConflict
	}
	return nil
}

func (r *DocumentRepository) CancelWithRefund(ctx context.Context, id string, userID int64, event *domain.NotificationEvent) (*domain.Document, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin cancellation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE documents SET status='cancelled', processing_until=NULL, updated_at=now()
		WHERE id=$1 AND user_id=$2 AND status='queued'`, id, userID)
	if err != nil {
		return nil, fmt.Errorf("cancel document: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, domain.ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE account_configs SET doc_used=GREATEST(0, doc_used-1), updated_at=now() WHERE user_id=$1`, userID); err != nil {
		return nil, fmt.Errorf("refund cancelled document quota: %w", err)
	}
	doc, err := getDocumentByID(ctx, tx, id, r.cipher)
	if err != nil {
		return nil, err
	}
	if err := insertNotificationEvent(ctx, tx, r.cipher, event); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit cancellation transaction: %w", err)
	}
	return doc, nil
}

func (r *DocumentRepository) ClaimNext(ctx context.Context, attemptID string, lease time.Duration, maxAttempts int) (*domain.Document, error) {
	if attemptID == "" || lease <= 0 || maxAttempts <= 0 {
		return nil, domain.ErrBadParamInput
	}
	var id string
	err := r.pool.QueryRow(ctx,
		`UPDATE documents
		 SET status = 'processing', attempt_id=$1, attempt_count=attempt_count+1,
		     processing_until=$2, error_detail=NULL, updated_at=now()
		 WHERE id = (
		     SELECT id FROM documents
		     WHERE status = 'queued'
		        OR (status='processing' AND processing_until <= now() AND attempt_count < $3)
		     ORDER BY CASE WHEN status='queued' THEN 0 ELSE 1 END, created_at ASC
		     FOR UPDATE SKIP LOCKED
		     LIMIT 1
		 )
		 RETURNING id`, attemptID, time.Now().Add(lease), maxAttempts,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim next document: %w", err)
	}

	return r.GetByID(ctx, id)
}

func (r *DocumentRepository) RequeueAttempt(ctx context.Context, id, attemptID string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE documents SET status='queued', attempt_id=NULL, processing_until=NULL, updated_at=now()
		WHERE id=$1 AND status='processing' AND attempt_id=$2`, id, attemptID)
	if err != nil {
		return fmt.Errorf("requeue document attempt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrConflict
	}
	return nil
}

func (r *DocumentRepository) ReleaseAttempt(ctx context.Context, id, attemptID string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE documents SET status='queued', attempt_id=NULL, processing_until=NULL,
		attempt_count=GREATEST(0, attempt_count-1), updated_at=now()
		WHERE id=$1 AND status='processing' AND attempt_id=$2`, id, attemptID)
	if err != nil {
		return fmt.Errorf("release document attempt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrConflict
	}
	return nil
}

func (r *DocumentRepository) ListExhaustedAttempts(ctx context.Context, before time.Time, maxAttempts, limit int) ([]domain.Document, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `SELECT id FROM documents WHERE status='processing'
		AND processing_until <= $1 AND attempt_count >= $2 ORDER BY processing_until ASC LIMIT $3`, before, maxAttempts, limit)
	if err != nil {
		return nil, fmt.Errorf("list exhausted attempts: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	docs := make([]domain.Document, 0, len(ids))
	for _, id := range ids {
		doc, err := r.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		docs = append(docs, *doc)
	}
	return docs, nil
}

func (r *DocumentRepository) FinalizeAttempt(ctx context.Context, f domain.DocumentFinalization, event *domain.NotificationEvent) (*domain.Document, error) {
	if f.Status != domain.StatusCompleted && f.Status != domain.StatusFailed {
		return nil, domain.ErrBadParamInput
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin terminal transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE documents SET status=$3, terminal_event_id=$4,
		result_key=$5, result_text=$6, error_detail=$7, result_expires_at=$8,
		processing_until=NULL, updated_at=now()
		WHERE id=$1 AND status='processing' AND attempt_id=$2`,
		f.DocumentID, f.AttemptID, string(f.Status), f.TerminalEventID,
		nilIfEmpty(f.ResultKey), nilIfEmpty(f.ResultText), nilIfEmpty(f.ErrorDetail), f.ResultExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("finalize document attempt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, domain.ErrConflict
	}
	doc, err := getDocumentByID(ctx, tx, f.DocumentID, r.cipher)
	if err != nil {
		return nil, err
	}
	if f.RefundQuota {
		if _, err := tx.Exec(ctx, `UPDATE account_configs SET doc_used=GREATEST(0, doc_used-1), updated_at=now() WHERE user_id=$1`, doc.UserID); err != nil {
			return nil, fmt.Errorf("refund terminal document quota: %w", err)
		}
	}
	if err := insertNotificationEvent(ctx, tx, r.cipher, event); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit terminal transaction: %w", err)
	}
	return doc, nil
}

func (r *DocumentRepository) CountByStatus(ctx context.Context, userID *int64) (map[domain.DocumentStatus]int64, error) {
	query := `SELECT status, count(*) FROM documents`
	args := []any{}
	if userID != nil {
		query += ` WHERE user_id = $1`
		args = append(args, *userID)
	}
	query += ` GROUP BY status`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("count documents by status: %w", err)
	}
	defer rows.Close()

	counts := make(map[domain.DocumentStatus]int64)
	for rows.Next() {
		var statusStr string
		var count int64
		if err := rows.Scan(&statusStr, &count); err != nil {
			return nil, fmt.Errorf("scan count: %w", err)
		}
		counts[domain.DocumentStatus(statusStr)] = count
	}
	return counts, rows.Err()
}

func (r *DocumentRepository) ListExpiredResults(ctx context.Context, before time.Time, limit int) ([]domain.Document, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `SELECT id, result_key FROM documents
		WHERE result_key IS NOT NULL AND result_expires_at <= $1
		ORDER BY result_expires_at ASC LIMIT $2`, before, limit)
	if err != nil {
		return nil, fmt.Errorf("list expired results: %w", err)
	}
	defer rows.Close()
	var docs []domain.Document
	for rows.Next() {
		var doc domain.Document
		if err := rows.Scan(&doc.ID, &doc.ResultKey); err != nil {
			return nil, fmt.Errorf("scan expired result: %w", err)
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

func (r *DocumentRepository) MarkResultExpired(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE documents SET result_key = NULL, result_text = NULL
		WHERE id = $1 AND result_expires_at <= now()`, id)
	if err != nil {
		return fmt.Errorf("mark result expired: %w", err)
	}
	return nil
}

func (r *DocumentRepository) ListExpiredInputs(ctx context.Context, before time.Time, limit int) ([]domain.Document, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `SELECT id, COALESCE(input_key, '') FROM documents
		WHERE input_key IS NOT NULL AND status IN ('completed','failed','cancelled') AND updated_at <= $1
		ORDER BY updated_at ASC LIMIT $2`, before, limit)
	if err != nil {
		return nil, fmt.Errorf("list expired inputs: %w", err)
	}
	defer rows.Close()
	var docs []domain.Document
	for rows.Next() {
		var doc domain.Document
		if err := rows.Scan(&doc.ID, &doc.InputKey); err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

func (r *DocumentRepository) MarkInputExpired(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE documents SET input_key=NULL
		WHERE id=$1 AND status IN ('completed','failed','cancelled')`, id)
	if err != nil {
		return fmt.Errorf("mark input expired: %w", err)
	}
	return nil
}

func (r *DocumentRepository) ListExpiredDocuments(ctx context.Context, before time.Time, limit int) ([]domain.Document, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `SELECT id, COALESCE(input_key, ''), COALESCE(result_key, '') FROM documents
		WHERE status IN ('completed','failed','cancelled') AND updated_at <= $1
		ORDER BY updated_at ASC LIMIT $2`, before, limit)
	if err != nil {
		return nil, fmt.Errorf("list expired documents: %w", err)
	}
	defer rows.Close()
	var docs []domain.Document
	for rows.Next() {
		var doc domain.Document
		if err := rows.Scan(&doc.ID, &doc.InputKey, &doc.ResultKey); err != nil {
			return nil, fmt.Errorf("scan expired document: %w", err)
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

func (r *DocumentRepository) DeleteExpiredDocument(ctx context.Context, id string, before time.Time) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM documents
		WHERE id=$1 AND status IN ('completed','failed','cancelled') AND updated_at <= $2`, id, before)
	if err != nil {
		return fmt.Errorf("delete expired document: %w", err)
	}
	return nil
}

func (r *DocumentRepository) IsInputKeyReferenced(ctx context.Context, key string) (bool, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM documents WHERE input_key=$1)`, key).Scan(&exists); err != nil {
		return false, fmt.Errorf("check document input reference: %w", err)
	}
	return exists, nil
}

func insertNotificationEvent(ctx context.Context, tx pgx.Tx, cipher *notifications.SecretCipher, event *domain.NotificationEvent) error {
	if event == nil || event.Channel == "" {
		return nil
	}
	secret, err := cipher.Encrypt(event.WebhookSecret)
	if err != nil {
		return fmt.Errorf("encrypt notification secret: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO notification_events
		(id, user_id, document_id, event_type, channel, webhook_url, webhook_secret, payload_json)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (document_id, event_type) DO NOTHING`, event.ID, event.UserID, event.DocumentID,
		event.Type, event.Channel, nilIfEmpty(event.WebhookURL), secret, []byte(event.Payload))
	if err != nil {
		return fmt.Errorf("insert notification event: %w", err)
	}
	return nil
}

func notificationTypeValue(cfg *domain.NotificationConfig) any {
	if cfg == nil || cfg.Type == "" {
		return nil
	}
	return cfg.Type
}

func notificationURLValue(cfg *domain.NotificationConfig) any {
	if cfg == nil || cfg.URL == "" {
		return nil
	}
	return cfg.URL
}

func setNotification(doc *domain.Document, notificationType, notificationURL *string, encrypted []byte, secretCipher *notifications.SecretCipher) error {
	if notificationType == nil {
		return nil
	}
	secret, err := secretCipher.Decrypt(encrypted)
	if err != nil {
		return err
	}
	doc.Notification = &domain.NotificationConfig{Type: *notificationType, URL: stringVal(notificationURL), Secret: secret}
	return nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func stringVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
