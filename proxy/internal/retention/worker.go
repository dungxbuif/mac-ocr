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

type expiredUploadLister interface {
	ListExpiredUploads(ctx context.Context, before time.Time, limit int) ([]domain.ObjectInfo, error)
}

type documentRepository interface {
	ListExpiredResults(ctx context.Context, before time.Time, limit int) ([]domain.Document, error)
	MarkResultExpired(ctx context.Context, id string) error
	ListExpiredInputs(ctx context.Context, before time.Time, limit int) ([]domain.Document, error)
	MarkInputExpired(ctx context.Context, id string) error
	ListExpiredDocuments(ctx context.Context, before time.Time, limit int) ([]domain.Document, error)
	DeleteExpiredDocument(ctx context.Context, id string, before time.Time) error
	IsInputKeyReferenced(ctx context.Context, key string) (bool, error)
}

type Worker struct {
	docs            documentRepository
	notifications   domain.NotificationRepository
	objects         objectDeleter
	uploads         expiredUploadLister
	logger          *slog.Logger
	inputTTL        time.Duration
	uploadTTL       time.Duration
	documentTTL     time.Duration
	notificationTTL time.Duration
}

type cleanupStats struct {
	resultsExpired       int
	inputsExpired        int
	documentsDeleted     int
	orphanUploadsDeleted int
	referencedUploads    int
	notificationsDeleted int64
}

func New(docs documentRepository, notifications domain.NotificationRepository, objects objectDeleter, logger *slog.Logger, inputTTL, uploadTTL, documentTTL, notificationTTL time.Duration) *Worker {
	deleter := objects
	uploads, _ := objects.(expiredUploadLister)
	return &Worker{docs: docs, notifications: notifications, objects: deleter, uploads: uploads, logger: logger, inputTTL: inputTTL, uploadTTL: uploadTTL, documentTTL: documentTTL, notificationTTL: notificationTTL}
}

func (w *Worker) Run(ctx context.Context) {
	if w.objects == nil {
		w.logger.Warn("object cleanup disabled: object repository cannot delete objects; database cleanup remains active")
	}
	if w.uploads == nil {
		w.logger.Warn("orphan upload cleanup disabled: object repository cannot list expired uploads")
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
	startedAt := time.Now()
	stats := cleanupStats{}
	stats.resultsExpired = w.cleanupResults(ctx)
	stats.inputsExpired = w.cleanupInputs(ctx)
	stats.documentsDeleted = w.cleanupDocuments(ctx)
	stats.orphanUploadsDeleted, stats.referencedUploads = w.cleanupOrphanUploads(ctx)
	if w.notifications != nil {
		deleted, err := w.notifications.DeleteBefore(ctx, time.Now().Add(-w.notificationTTL), 1000)
		if err != nil {
			w.logger.Error("delete expired notification events failed", "error", err)
		} else {
			stats.notificationsDeleted = deleted
		}
	}

	totalChanged := int64(stats.resultsExpired + stats.inputsExpired + stats.documentsDeleted + stats.orphanUploadsDeleted)
	totalChanged += stats.notificationsDeleted
	log := w.logger.Debug
	if totalChanged > 0 {
		log = w.logger.Info
	}
	log("retention cleanup completed",
		"duration_ms", time.Since(startedAt).Milliseconds(),
		"results_expired", stats.resultsExpired,
		"inputs_expired", stats.inputsExpired,
		"documents_deleted", stats.documentsDeleted,
		"orphan_uploads_deleted", stats.orphanUploadsDeleted,
		"referenced_uploads_skipped", stats.referencedUploads,
		"notifications_deleted", stats.notificationsDeleted,
	)
}

func (w *Worker) cleanupOrphanUploads(ctx context.Context) (deleted, referencedCount int) {
	if w.uploads == nil || w.objects == nil {
		return 0, 0
	}
	uploads, err := w.uploads.ListExpiredUploads(ctx, time.Now().Add(-w.uploadTTL), 100)
	if err != nil {
		w.logger.Error("list expired presigned uploads failed", "error", err)
		return 0, 0
	}
	for _, upload := range uploads {
		referenced, err := w.docs.IsInputKeyReferenced(ctx, upload.Key)
		if err != nil {
			w.logger.Error("check expired upload reference failed", "objectKey", upload.Key, "error", err)
			continue
		}
		if referenced {
			referencedCount++
			continue
		}
		if err := w.objects.Delete(ctx, upload.Key); err != nil {
			w.logger.Error("delete orphan presigned upload failed", "objectKey", upload.Key, "error", err)
			continue
		}
		deleted++
	}
	return deleted, referencedCount
}

func (w *Worker) cleanupDocuments(ctx context.Context) int {
	cutoff := time.Now().Add(-w.documentTTL)
	docs, err := w.docs.ListExpiredDocuments(ctx, cutoff, 100)
	if err != nil {
		w.logger.Error("list expired document metadata failed", "error", err)
		return 0
	}
	deleted := 0
	for _, doc := range docs {
		if w.objects != nil {
			for _, key := range []string{doc.InputKey, doc.ResultKey} {
				if key == "" {
					continue
				}
				if err := w.objects.Delete(ctx, key); err != nil {
					w.logger.Error("delete object for expired document failed", "documentID", doc.ID, "objectKey", key, "error", err)
					continue
				}
			}
		}
		if err := w.docs.DeleteExpiredDocument(ctx, doc.ID, cutoff); err != nil {
			w.logger.Error("delete expired document metadata failed", "documentID", doc.ID, "error", err)
			continue
		}
		deleted++
	}
	return deleted
}

func (w *Worker) cleanupResults(ctx context.Context) int {
	docs, err := w.docs.ListExpiredResults(ctx, time.Now(), 100)
	if err != nil {
		w.logger.Error("list expired OCR results failed", "error", err)
		return 0
	}
	expired := 0
	for _, doc := range docs {
		if w.objects != nil && doc.ResultKey != "" {
			if err := w.objects.Delete(ctx, doc.ResultKey); err != nil {
				w.logger.Error("delete expired OCR result object failed", "documentID", doc.ID, "error", err)
			}
		}
		if err := w.docs.MarkResultExpired(ctx, doc.ID); err != nil {
			w.logger.Error("remove expired OCR payload from database failed", "documentID", doc.ID, "error", err)
			continue
		}
		expired++
	}
	return expired
}

func (w *Worker) cleanupInputs(ctx context.Context) int {
	docs, err := w.docs.ListExpiredInputs(ctx, time.Now().Add(-w.inputTTL), 100)
	if err != nil {
		w.logger.Error("list expired OCR inputs failed", "error", err)
		return 0
	}
	expired := 0
	for _, doc := range docs {
		if w.objects != nil && doc.InputKey != "" {
			if err := w.objects.Delete(ctx, doc.InputKey); err != nil {
				w.logger.Error("delete expired OCR input failed", "documentID", doc.ID, "error", err)
				continue
			}
		}
		if err := w.docs.MarkInputExpired(ctx, doc.ID); err != nil {
			w.logger.Error("clear expired OCR input metadata failed", "documentID", doc.ID, "error", err)
			continue
		}
		expired++
	}
	return expired
}
