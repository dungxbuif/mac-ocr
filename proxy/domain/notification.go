package domain

import (
	"context"
	"encoding/json"
	"time"
)

type NotificationEvent struct {
	ID            string
	UserID        int64
	DocumentID    string
	Type          string
	Channel       string
	WebhookURL    string
	WebhookSecret string
	Payload       json.RawMessage
	AttemptCount  int
	CreatedAt     time.Time
}

type NotificationRepository interface {
	Create(ctx context.Context, event *NotificationEvent) error
	ClaimWebhook(ctx context.Context) (*NotificationEvent, error)
	MarkDelivered(ctx context.Context, eventID string) error
	MarkFailed(ctx context.Context, eventID, detail string, retryAt time.Time) error
	ListSSE(ctx context.Context, userID int64, afterID string, limit int) ([]NotificationEvent, error)
	DeleteBefore(ctx context.Context, before time.Time, limit int) (int64, error)
}

type NotificationPublisher interface {
	BuildDocumentEvent(doc *Document) (*NotificationEvent, error)
	PublishDocument(ctx context.Context, doc *Document) error
}
