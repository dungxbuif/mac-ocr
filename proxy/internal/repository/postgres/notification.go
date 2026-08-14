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

type NotificationRepository struct {
	pool   *pgxpool.Pool
	cipher *notifications.SecretCipher
}

func NewNotificationRepository(pool *pgxpool.Pool, cipher *notifications.SecretCipher) *NotificationRepository {
	return &NotificationRepository{pool: pool, cipher: cipher}
}

func (r *NotificationRepository) Create(ctx context.Context, event *domain.NotificationEvent) error {
	secret, err := r.cipher.Encrypt(event.WebhookSecret)
	if err != nil {
		return fmt.Errorf("encrypt webhook secret: %w", err)
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO notification_events
		(id, user_id, document_id, event_type, channel, webhook_url, webhook_secret, payload_json)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (document_id, event_type) DO NOTHING`, event.ID, event.UserID, event.DocumentID,
		event.Type, event.Channel, nilIfEmpty(event.WebhookURL), secret, []byte(event.Payload))
	if err != nil {
		return fmt.Errorf("insert notification event: %w", err)
	}
	return nil
}

func (r *NotificationRepository) ClaimWebhook(ctx context.Context) (*domain.NotificationEvent, error) {
	var event domain.NotificationEvent
	var encrypted []byte
	err := r.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM notification_events
		WHERE channel = 'webhook' AND next_attempt_at <= now()
		  AND (delivery_status = 'pending' OR (delivery_status = 'delivering' AND locked_until < now()))
		ORDER BY created_at ASC FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE notification_events e
	SET delivery_status = 'delivering', locked_until = now() + interval '30 seconds'
	FROM candidate c WHERE e.id = c.id
	RETURNING e.id, e.user_id, e.document_id, e.event_type, e.channel, e.webhook_url,
	          e.webhook_secret, e.payload_json, e.attempt_count, e.created_at`).Scan(
		&event.ID, &event.UserID, &event.DocumentID, &event.Type, &event.Channel, &event.WebhookURL,
		&encrypted, &event.Payload, &event.AttemptCount, &event.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim webhook event: %w", err)
	}
	event.WebhookSecret, err = r.cipher.Decrypt(encrypted)
	if err != nil {
		return nil, err
	}
	return &event, nil
}

func (r *NotificationRepository) MarkDelivered(ctx context.Context, eventID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE notification_events SET delivery_status='delivered', delivered_at=now(),
		locked_until=NULL, last_error=NULL WHERE id=$1`, eventID)
	return err
}

func (r *NotificationRepository) MarkFailed(ctx context.Context, eventID, detail string, retryAt time.Time) error {
	if len(detail) > 2000 {
		detail = detail[:2000]
	}
	_, err := r.pool.Exec(ctx, `UPDATE notification_events SET delivery_status='pending', attempt_count=attempt_count+1,
		next_attempt_at=$2, locked_until=NULL, last_error=$3 WHERE id=$1`, eventID, retryAt, detail)
	return err
}

func (r *NotificationRepository) ListSSE(ctx context.Context, userID int64, afterID string, limit int) ([]domain.NotificationEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `SELECT id, user_id, document_id, event_type, channel, payload_json, created_at
		FROM notification_events WHERE user_id=$1 AND channel='sse' AND id > $2
		ORDER BY id ASC LIMIT $3`, userID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list SSE events: %w", err)
	}
	defer rows.Close()
	events := make([]domain.NotificationEvent, 0)
	for rows.Next() {
		var event domain.NotificationEvent
		if err := rows.Scan(&event.ID, &event.UserID, &event.DocumentID, &event.Type, &event.Channel, &event.Payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		if !json.Valid(event.Payload) {
			return nil, fmt.Errorf("notification event %s contains invalid JSON", event.ID)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (r *NotificationRepository) DeleteBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM notification_events WHERE id IN (
		SELECT id FROM notification_events WHERE created_at < $1
		ORDER BY created_at ASC LIMIT $2
	)`, before, limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired notification events: %w", err)
	}
	return tag.RowsAffected(), nil
}
