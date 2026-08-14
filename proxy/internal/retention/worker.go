package retention

import (
	"context"
	"log/slog"
	"time"

	"macocr/proxy/domain"
)

type objectDeleter interface {
	Delete(ctx context.Context, key string) error
}

type Worker struct {
	docs            domain.DocumentRepository
	notifications   domain.NotificationRepository
	objects         objectDeleter
	logger          *slog.Logger
	inputTTL        time.Duration
	notificationTTL time.Duration
}

func New(docs domain.DocumentRepository, notifications domain.NotificationRepository, objects domain.ObjectRepository, logger *slog.Logger, inputTTL, notificationTTL time.Duration) *Worker {
	deleter, _ := objects.(objectDeleter)
	return &Worker{docs: docs, notifications: notifications, objects: deleter, logger: logger, inputTTL: inputTTL, notificationTTL: notificationTTL}
}

func (w *Worker) Run(ctx context.Context) {
	if w.objects == nil {
		w.logger.Warn("object cleanup disabled: object repository cannot delete objects; database cleanup remains active")
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		w.cleanup(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) cleanup(ctx context.Context) {
	w.cleanupResults(ctx)
	w.cleanupInputs(ctx)
	if w.notifications != nil {
		if _, err := w.notifications.DeleteBefore(ctx, time.Now().Add(-w.notificationTTL), 1000); err != nil {
			w.logger.Error("delete expired notification events failed", "error", err)
		}
	}
}

func (w *Worker) cleanupResults(ctx context.Context) {
	docs, err := w.docs.ListExpiredResults(ctx, time.Now(), 100)
	if err != nil {
		w.logger.Error("list expired OCR results failed", "error", err)
		return
	}
	for _, doc := range docs {
		if w.objects != nil && doc.ResultKey != "" {
			if err := w.objects.Delete(ctx, doc.ResultKey); err != nil {
				w.logger.Error("delete expired OCR result object failed", "documentID", doc.ID, "error", err)
			}
		}
		if err := w.docs.MarkResultExpired(ctx, doc.ID); err != nil {
			w.logger.Error("remove expired OCR payload from database failed", "documentID", doc.ID, "error", err)
		}
	}
}

func (w *Worker) cleanupInputs(ctx context.Context) {
	docs, err := w.docs.ListExpiredInputs(ctx, time.Now().Add(-w.inputTTL), 100)
	if err != nil {
		w.logger.Error("list expired OCR inputs failed", "error", err)
		return
	}
	for _, doc := range docs {
		if w.objects != nil && doc.InputKey != "" {
			if err := w.objects.Delete(ctx, doc.InputKey); err != nil {
				w.logger.Error("delete expired OCR input failed", "documentID", doc.ID, "error", err)
				continue
			}
		}
		if err := w.docs.MarkInputExpired(ctx, doc.ID); err != nil {
			w.logger.Error("clear expired OCR input metadata failed", "documentID", doc.ID, "error", err)
		}
	}
}
