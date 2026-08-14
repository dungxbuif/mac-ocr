package document

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"macocr/proxy/domain"
	"macocr/proxy/internal/usecase/auth"
)

type InputSource struct {
	Type       string
	URL        string
	Base64Data string
}

type BatchItemInput struct {
	Input        InputSource
	Options      *domain.OCROptions
	Notification *domain.NotificationConfig
}

type Service struct {
	docs           domain.DocumentRepository
	objects        domain.ObjectRepository
	auth           *auth.Service
	notifier       domain.NotificationPublisher
	results        domain.ResultCache
	maxUploadBytes int64
}

func NewService(
	docs domain.DocumentRepository,
	objects domain.ObjectRepository,
	auth *auth.Service,
	notifier domain.NotificationPublisher,
	results domain.ResultCache,
) *Service {
	return NewServiceWithMaxUploadBytes(docs, objects, auth, notifier, results, MaxUploadedObjectBytes)
}

func NewServiceWithMaxUploadBytes(
	docs domain.DocumentRepository,
	objects domain.ObjectRepository,
	auth *auth.Service,
	notifier domain.NotificationPublisher,
	results domain.ResultCache,
	maxUploadBytes int64,
) *Service {
	if maxUploadBytes <= 0 {
		maxUploadBytes = MaxUploadedObjectBytes
	}
	return &Service{
		docs:           docs,
		objects:        objects,
		auth:           auth,
		notifier:       notifier,
		results:        results,
		maxUploadBytes: maxUploadBytes,
	}
}

func (s *Service) SubmitSingle(
	ctx context.Context,
	userID int64,
	input InputSource,
	opts *domain.OCROptions,
	notification *domain.NotificationConfig,
) (*domain.Document, error) {
	doc, err := s.prepareQueuedDocument(ctx, userID, input, opts, notification)
	if err != nil {
		return nil, err
	}

	created, err := s.docs.CreateWithQuota(ctx, &doc)
	if err != nil {
		_ = s.objects.Delete(ctx, doc.InputKey)
		return nil, err
	}
	s.auth.InvalidateAccountConfig(ctx, userID)

	return created, nil
}

func (s *Service) SubmitBatch(
	ctx context.Context,
	userID int64,
	items []BatchItemInput,
) ([]domain.Document, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("%w: batch must contain at least 1 item", domain.ErrBadParamInput)
	}
	if len(items) > 100 {
		return nil, fmt.Errorf("%w: batch exceeds maximum limit of 100 items", domain.ErrBadParamInput)
	}

	preparedDocs := make([]domain.Document, len(items))

	for i, item := range items {
		doc, err := s.prepareQueuedDocument(ctx, userID, item.Input, item.Options, item.Notification)
		if err != nil {
			s.cleanupPreparedInputs(ctx, preparedDocs[:i])
			return nil, fmt.Errorf("batch item %d: %w", i, err)
		}
		preparedDocs[i] = doc
	}

	createdDocs, err := s.docs.CreateManyWithQuota(ctx, userID, preparedDocs)
	if err != nil {
		s.cleanupPreparedInputs(ctx, preparedDocs)
		return nil, err
	}
	s.auth.InvalidateAccountConfig(ctx, userID)

	return createdDocs, nil
}

func (s *Service) cleanupPreparedInputs(ctx context.Context, docs []domain.Document) {
	for i := range docs {
		if docs[i].InputKey != "" {
			_ = s.objects.Delete(ctx, docs[i].InputKey)
		}
	}
}

func (s *Service) prepareQueuedDocument(
	ctx context.Context,
	userID int64,
	input InputSource,
	opts *domain.OCROptions,
	notification *domain.NotificationConfig,
) (domain.Document, error) {
	validOpts, err := ValidateOptions(opts)
	if err != nil {
		return domain.Document{}, fmt.Errorf("%w: %v", domain.ErrBadParamInput, err)
	}
	validNotification, err := ValidateNotification(notification)
	if err != nil {
		return domain.Document{}, fmt.Errorf("%w: %v", domain.ErrBadParamInput, err)
	}
	processed, err := s.processInput(ctx, userID, input)
	if err != nil {
		return domain.Document{}, err
	}
	return domain.Document{
		ID:               generateID("doc"),
		UserID:           userID,
		Status:           domain.StatusQueued,
		InputKey:         processed.ObjectKey,
		InputSHA256:      processed.SHA256,
		InputContentType: processed.ContentType,
		InputSizeBytes:   processed.SizeBytes,
		Options:          validOpts,
		Notification:     validNotification,
	}, nil
}

func (s *Service) GetDocument(ctx context.Context, userID int64, docID string) (*domain.Document, error) {
	doc, err := s.GetDocumentStatus(ctx, userID, docID)
	if err != nil {
		return nil, err
	}
	if doc.Status == domain.StatusCompleted && doc.ResultExpiresAt != nil && !time.Now().Before(*doc.ResultExpiresAt) {
		return nil, domain.ErrResultExpired
	}
	if doc.Status == domain.StatusCompleted {
		if s.results == nil {
			return nil, fmt.Errorf("%w: result cache is not configured", domain.ErrStorageUnavailable)
		}
		result, err := s.results.GetResult(ctx, doc.ID)
		if err != nil {
			return nil, err
		}
		doc.Result = result
	}
	return doc, nil
}

func (s *Service) GetDocumentStatus(ctx context.Context, userID int64, docID string) (*domain.Document, error) {
	doc, err := s.docs.GetByID(ctx, docID)
	if err != nil {
		return nil, err
	}
	if doc.UserID != userID {
		return nil, domain.ErrNotFound
	}
	return doc, nil
}

func (s *Service) GetDocumentAdmin(ctx context.Context, docID string) (*domain.Document, error) {
	return s.docs.GetByID(ctx, docID)
}

func (s *Service) ListDocumentsAdmin(ctx context.Context, status domain.DocumentStatus, limit, offset int) ([]domain.Document, error) {
	return s.docs.ListByUser(ctx, 0, status, limit, offset)
}

func (s *Service) CountStatus(ctx context.Context, userID *int64) (map[domain.DocumentStatus]int64, error) {
	return s.docs.CountByStatus(ctx, userID)
}

func (s *Service) processInput(ctx context.Context, userID int64, input InputSource) (*ProcessedInput, error) {
	switch input.Type {
	case "base64":
		if input.Base64Data == "" {
			return nil, fmt.Errorf("%w: base64 data is empty", domain.ErrBadParamInput)
		}
		return ProcessBase64(ctx, userID, input.Base64Data, s.objects)
	case "url":
		if input.URL == "" {
			return nil, fmt.Errorf("%w: url is empty", domain.ErrBadParamInput)
		}
		return ProcessURLWithUploadLimit(ctx, userID, input.URL, s.objects, s.maxUploadBytes)
	default:
		return nil, fmt.Errorf("%w: unsupported input type %q (allowed: base64, url)", domain.ErrBadParamInput, input.Type)
	}
}

func generateID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	ts := time.Now().UnixNano() / 1e6
	return fmt.Sprintf("%s_%x%s", prefix, ts, hex.EncodeToString(b[:6]))
}
