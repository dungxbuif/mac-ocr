package domain

import (
	"context"
	"time"
)

type DocumentStatus string

const (
	StatusQueued     DocumentStatus = "queued"
	StatusProcessing DocumentStatus = "processing"
	StatusCompleted  DocumentStatus = "completed"
	StatusFailed     DocumentStatus = "failed"
	StatusCancelled  DocumentStatus = "cancelled"
)

type OCROptions struct {
	RecognitionLevel             string   `json:"recognitionLevel,omitempty"`
	Languages                    []string `json:"languages,omitempty"`
	AutomaticallyDetectsLanguage *bool    `json:"automaticallyDetectsLanguage,omitempty"`
	UsesLanguageCorrection       *bool    `json:"usesLanguageCorrection,omitempty"`
	CustomWords                  []string `json:"customWords,omitempty"`
	MinimumTextHeight            float64  `json:"minimumTextHeight,omitempty"`
}

type NotificationConfig struct {
	Type   string `json:"type"`
	URL    string `json:"url,omitempty"`
	Secret string `json:"secret,omitempty"`
}

type OCRBlock struct {
	Text       string    `json:"text"`
	Confidence float64   `json:"confidence"`
	BBox       []float64 `json:"bbox,omitempty"`
}

type OCRPageResult struct {
	PageNumber int        `json:"pageNumber"`
	Text       string     `json:"text"`
	Blocks     []OCRBlock `json:"blocks,omitempty"`
}

type OCRResult struct {
	Text      string          `json:"text"`
	PageCount int             `json:"pageCount"`
	Pages     []OCRPageResult `json:"pages,omitempty"`
}

type Document struct {
	ID               string              `json:"id"`
	UserID           int64               `json:"user_id"`
	Status           DocumentStatus      `json:"status"`
	InputKey         string              `json:"input_key"`
	InputSHA256      string              `json:"-"`
	InputContentType string              `json:"input_content_type"`
	InputSizeBytes   int64               `json:"input_size_bytes"`
	Options          *OCROptions         `json:"options,omitempty"`
	ResultKey        string              `json:"result_key,omitempty"`
	ResultText       string              `json:"result_text,omitempty"`
	Result           *OCRResult          `json:"-"`
	ErrorDetail      string              `json:"error_detail,omitempty"`
	AttemptID        string              `json:"attempt_id,omitempty"`
	AttemptCount     int                 `json:"attempt_count,omitempty"`
	ProcessingUntil  *time.Time          `json:"processing_until,omitempty"`
	TerminalEventID  string              `json:"terminal_event_id,omitempty"`
	Notification     *NotificationConfig `json:"-"`
	ResultExpiresAt  *time.Time          `json:"result_expires_at,omitempty"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
}

type DocumentFinalization struct {
	DocumentID      string
	AttemptID       string
	TerminalEventID string
	Status          DocumentStatus
	ResultKey       string
	ResultText      string
	ErrorDetail     string
	ResultExpiresAt *time.Time
	RefundQuota     bool
}

// ResultCache is the serving store for completed OCR payloads. PostgreSQL keeps
// lifecycle metadata; API and MCP reads use this cache directly.
type ResultCache interface {
	SetResult(ctx context.Context, documentID string, result *OCRResult, ttl time.Duration) error
	GetResult(ctx context.Context, documentID string) (*OCRResult, error)
	DeleteResult(ctx context.Context, documentID string) error
}

type DocumentRepository interface {
	Create(ctx context.Context, doc *Document) (*Document, error)
	CreateMany(ctx context.Context, docs []Document) ([]Document, error)
	CreateWithQuota(ctx context.Context, doc *Document) (*Document, error)
	CreateManyWithQuota(ctx context.Context, userID int64, docs []Document) ([]Document, error)
	GetByID(ctx context.Context, id string) (*Document, error)
	ListByUser(ctx context.Context, userID int64, status DocumentStatus, limit, offset int) ([]Document, error)
	UpdateStatus(ctx context.Context, id string, status DocumentStatus, attemptID, resultKey, resultText, errorDetail string, resultExpiresAt *time.Time) error
	Cancel(ctx context.Context, id string, userID int64) error
	CancelWithRefund(ctx context.Context, id string, userID int64, event *NotificationEvent) (*Document, error)
	ClaimNext(ctx context.Context, attemptID string, lease time.Duration, maxAttempts int) (*Document, error)
	RequeueAttempt(ctx context.Context, id, attemptID string) error
	ReleaseAttempt(ctx context.Context, id, attemptID string) error
	ListExhaustedAttempts(ctx context.Context, before time.Time, maxAttempts, limit int) ([]Document, error)
	FinalizeAttempt(ctx context.Context, finalization DocumentFinalization, event *NotificationEvent) (*Document, error)
	CountByStatus(ctx context.Context, userID *int64) (map[DocumentStatus]int64, error)
	ListExpiredResults(ctx context.Context, before time.Time, limit int) ([]Document, error)
	MarkResultExpired(ctx context.Context, id string) error
	ListExpiredInputs(ctx context.Context, before time.Time, limit int) ([]Document, error)
	MarkInputExpired(ctx context.Context, id string) error
	ListExpiredDocuments(ctx context.Context, before time.Time, limit int) ([]Document, error)
	DeleteExpiredDocument(ctx context.Context, id string, before time.Time) error
	IsInputKeyReferenced(ctx context.Context, key string) (bool, error)
}
