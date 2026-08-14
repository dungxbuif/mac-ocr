package rest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/errs"
	"macocr/proxy/internal/native"
	"macocr/proxy/internal/scheduler"
)

const maxWebhookRequestBytes = 1024 * 1024

type WebhookHandler struct {
	docs       domain.DocumentRepository
	objects    domain.ObjectRepository
	authSecret string
	sched      *scheduler.Scheduler
	logger     *slog.Logger
	notifier   domain.NotificationPublisher
	results    domain.ResultCache
	resultTTL  time.Duration
}

func NewWebhookHandler(
	docs domain.DocumentRepository,
	objects domain.ObjectRepository,
	authSecret string,
	sched *scheduler.Scheduler,
	logger *slog.Logger,
	notifier domain.NotificationPublisher,
	results domain.ResultCache,
	resultTTL time.Duration,
) *WebhookHandler {
	return &WebhookHandler{
		docs:       docs,
		objects:    objects,
		authSecret: authSecret,
		sched:      sched,
		logger:     logger,
		notifier:   notifier,
		results:    results,
		resultTTL:  resultTTL,
	}
}

func (h *WebhookHandler) HandleNativeEvent(c *gin.Context) {
	nodeID := c.GetHeader("X-Native-Node-Id")
	ts := c.GetHeader("X-Native-Timestamp")
	eventID := c.GetHeader("X-Native-Event-Id")
	sig := c.GetHeader("X-Native-Signature")

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWebhookRequestBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if respondRequestTooLarge(c, err, maxWebhookRequestBytes) {
			return
		}
		RespondProblem(c, errs.InvalidInput("failed to read body"))
		return
	}

	if h.authSecret != "" {
		if err := native.VerifyWebhook(h.authSecret, nodeID, ts, eventID, body, sig); err != nil {
			h.logger.Warn("webhook signature verification failed", "error", err, "nodeID", nodeID)
			RespondProblem(c, errs.Unauthorized("invalid webhook signature"))
			return
		}
	}

	var event domain.NativeEvent
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		RespondProblem(c, errs.InvalidInput("invalid event json"))
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		RespondProblem(c, errs.InvalidInput("event body must contain one JSON object"))
		return
	}
	if event.EventID == "" || event.EventID != eventID || event.NodeID == "" || event.NodeID != nodeID || event.DocumentID == "" || event.AttemptID == "" {
		RespondProblem(c, errs.InvalidInput("event identity does not match signed headers or required identifiers are missing"))
		return
	}
	if (event.Type == "attempt.completed" && event.Result == nil) || (event.Type == "attempt.failed" && event.Error == "") || (event.Type != "attempt.completed" && event.Type != "attempt.failed") {
		RespondProblem(c, errs.InvalidInput("event type and payload are inconsistent"))
		return
	}

	doc, err := h.docs.GetByID(c.Request.Context(), event.DocumentID)
	if err != nil {
		RespondProblem(c, errs.NotFound("document not found"))
		return
	}
	if doc.AttemptID == event.AttemptID && (doc.Status == domain.StatusCompleted || doc.Status == domain.StatusFailed) {
		if doc.Status == domain.StatusCompleted && event.Result != nil && h.results != nil {
			remainingTTL := h.resultTTL
			if doc.ResultExpiresAt != nil {
				remainingTTL = time.Until(*doc.ResultExpiresAt)
			}
			if remainingTTL > 0 {
				if err := h.results.SetResult(c.Request.Context(), doc.ID, event.Result, remainingTTL); err != nil {
					RespondProblem(c, errs.ServiceUnavailable("result cache is unavailable", err))
					return
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "acknowledged", "eventId": event.EventID, "documentId": event.DocumentID})
		return
	}
	if doc.Status != domain.StatusProcessing || doc.AttemptID != event.AttemptID {
		RespondProblem(c, errs.New(errs.CodeStateConflict, http.StatusConflict, "Callback does not match the active document attempt"))
		return
	}

	if event.Type == "attempt.completed" && event.Result != nil {
		resultBytes, err := json.Marshal(event.Result)
		if err != nil {
			RespondError(c, fmt.Errorf("encode OCR result: %w", err))
			return
		}
		resultKey := fmt.Sprintf("results/%d/%s/%s.json", doc.UserID, doc.ID, event.EventID)
		if err := h.objects.Put(c.Request.Context(), resultKey, bytes.NewReader(resultBytes), "application/json"); err != nil {
			h.logger.Error("failed to store OCR result", "docID", doc.ID, "error", err)
			RespondProblem(c, errs.ServiceUnavailable("result storage is unavailable", err))
			return
		}
		expiresAt := time.Now().Add(h.resultTTL)
		terminalDoc := *doc
		terminalDoc.Status, terminalDoc.ResultKey, terminalDoc.ResultText, terminalDoc.Result, terminalDoc.ResultExpiresAt = domain.StatusCompleted, resultKey, event.Result.Text, event.Result, &expiresAt
		outboxEvent, buildErr := h.buildNotificationEvent(&terminalDoc)
		if buildErr != nil {
			_ = h.objects.Delete(c.Request.Context(), resultKey)
			RespondError(c, buildErr)
			return
		}
		_, err = h.docs.FinalizeAttempt(c.Request.Context(), domain.DocumentFinalization{
			DocumentID: doc.ID, AttemptID: event.AttemptID, TerminalEventID: event.EventID,
			Status: domain.StatusCompleted, ResultKey: resultKey, ResultText: event.Result.Text, ResultExpiresAt: &expiresAt,
		}, outboxEvent)
		if err != nil {
			_ = h.objects.Delete(c.Request.Context(), resultKey)
			h.logger.Error("failed to update document status to completed", "docID", doc.ID, "error", err)
			RespondError(c, err)
			return
		}
		if h.results != nil {
			if err := h.results.SetResult(c.Request.Context(), doc.ID, event.Result, h.resultTTL); err != nil {
				h.logger.Error("failed to cache OCR result", "docID", doc.ID, "error", err)
				RespondProblem(c, errs.ServiceUnavailable("result cache is unavailable", err))
				return
			}
		}
	} else if event.Type == "attempt.failed" {
		terminalDoc := *doc
		terminalDoc.Status, terminalDoc.ErrorDetail = domain.StatusFailed, event.Error
		outboxEvent, buildErr := h.buildNotificationEvent(&terminalDoc)
		if buildErr != nil {
			RespondError(c, buildErr)
			return
		}
		if _, err := h.docs.FinalizeAttempt(c.Request.Context(), domain.DocumentFinalization{
			DocumentID: doc.ID, AttemptID: event.AttemptID, TerminalEventID: event.EventID,
			Status: domain.StatusFailed, ErrorDetail: event.Error,
		}, outboxEvent); err != nil {
			h.logger.Error("failed to update document status to failed", "docID", doc.ID, "error", err)
			RespondError(c, err)
			return
		}
	}

	if h.sched != nil {
		h.sched.Wake()
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "acknowledged",
		"eventId":    event.EventID,
		"documentId": event.DocumentID,
	})
}

func (h *WebhookHandler) buildNotificationEvent(doc *domain.Document) (*domain.NotificationEvent, error) {
	if h.notifier == nil {
		return nil, nil
	}
	return h.notifier.BuildDocumentEvent(doc)
}
