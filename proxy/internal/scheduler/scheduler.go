package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"macocr/proxy/domain"
	"macocr/proxy/internal/native"
	"macocr/proxy/internal/usecase/auth"
)

type Scheduler struct {
	docs        domain.DocumentRepository
	objects     domain.ObjectRepository
	native      *native.Client
	auth        *auth.Service
	callbackURL string
	logger      *slog.Logger
	notifier    domain.NotificationPublisher
	lease       time.Duration
	maxAttempts int
	wakeCh      chan struct{}
}

func New(
	docs domain.DocumentRepository,
	objects domain.ObjectRepository,
	native *native.Client,
	auth *auth.Service,
	callbackURL string,
	logger *slog.Logger,
	lease time.Duration,
	maxAttempts int,
	notifiers ...domain.NotificationPublisher,
) *Scheduler {
	var notifier domain.NotificationPublisher
	if len(notifiers) > 0 {
		notifier = notifiers[0]
	}
	return &Scheduler{
		docs:        docs,
		objects:     objects,
		native:      native,
		auth:        auth,
		callbackURL: callbackURL,
		logger:      logger,
		notifier:    notifier,
		lease:       lease,
		maxAttempts: maxAttempts,
		wakeCh:      make(chan struct{}, 10),
	}
}

func (s *Scheduler) Wake() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollAndDispatch(ctx)
		case <-s.wakeCh:
			s.pollAndDispatch(ctx)
		}
	}
}

func (s *Scheduler) pollAndDispatch(ctx context.Context) {
	s.failExhaustedAttempts(ctx)
	attemptID := fmt.Sprintf("att_%x", time.Now().UnixNano())
	doc, err := s.docs.ClaimNext(ctx, attemptID, s.lease, s.maxAttempts)
	if err != nil {
		s.logger.Error("claim next document failed", "error", err)
		return
	}
	if doc == nil {
		return
	}

	presignedURL, err := s.objects.PresignGetURL(ctx, doc.InputKey, 15*time.Minute)
	if err != nil {
		s.logger.Error("presign input url failed", "docID", doc.ID, "error", err)
		s.retryOrFail(ctx, doc, "input storage remained unavailable after retry")
		return
	}

	req := &domain.NativeOCRRequest{
		DocumentID: doc.ID,
		PageID:     "page_1",
		AttemptID:  attemptID,
		Input: domain.NativeInputRef{
			URL:       presignedURL,
			MediaType: doc.InputContentType,
			SHA256:    doc.InputSHA256,
		},
		Options: doc.Options,
		Callback: domain.NativeCallbackRef{
			URL: s.callbackURL,
		},
	}

	err = s.native.DispatchOCR(ctx, req)
	if err != nil {
		if err == native.ErrNativeBusy {
			s.logger.Warn("native worker busy, returning document to queue", "docID", doc.ID)
			if releaseErr := s.docs.ReleaseAttempt(ctx, doc.ID, attemptID); releaseErr != nil {
				s.logger.Error("release busy worker attempt failed", "docID", doc.ID, "error", releaseErr)
			}
			return
		}

		s.logger.Error("native dispatch failed", "docID", doc.ID, "error", err)
		s.retryOrFail(ctx, doc, "OCR worker dispatch failed after retry")
		return
	}

}

func (s *Scheduler) retryOrFail(ctx context.Context, doc *domain.Document, terminalDetail string) {
	if doc.AttemptCount >= s.maxAttempts {
		s.failDocument(ctx, doc, terminalDetail)
		return
	}
	if err := s.docs.RequeueAttempt(ctx, doc.ID, doc.AttemptID); err != nil {
		s.logger.Error("requeue document attempt failed", "docID", doc.ID, "error", err)
	}
}

func (s *Scheduler) failDocument(ctx context.Context, doc *domain.Document, detail string) {
	terminalDoc := *doc
	terminalDoc.Status = domain.StatusFailed
	terminalDoc.ErrorDetail = detail
	var event *domain.NotificationEvent
	var err error
	if s.notifier != nil {
		event, err = s.notifier.BuildDocumentEvent(&terminalDoc)
		if err != nil {
			s.logger.Error("build failure notification failed", "docID", doc.ID, "error", err)
			return
		}
	}
	_, err = s.docs.FinalizeAttempt(ctx, domain.DocumentFinalization{
		DocumentID: doc.ID, AttemptID: doc.AttemptID, TerminalEventID: fmt.Sprintf("evt_proxy_%x", time.Now().UnixNano()),
		Status: domain.StatusFailed, ErrorDetail: detail, RefundQuota: true,
	}, event)
	if err != nil {
		s.logger.Error("failed to persist terminal document state", "docID", doc.ID, "error", err)
		return
	}
	s.auth.InvalidateAccountConfig(ctx, doc.UserID)
}

func (s *Scheduler) failExhaustedAttempts(ctx context.Context) {
	docs, err := s.docs.ListExhaustedAttempts(ctx, time.Now(), s.maxAttempts, 100)
	if err != nil {
		s.logger.Error("list exhausted processing attempts failed", "error", err)
		return
	}
	for i := range docs {
		s.failDocument(ctx, &docs[i], "processing lease expired after maximum attempts")
	}
}
