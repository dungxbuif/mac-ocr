package rest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/errs"
	"macocr/proxy/internal/native"
	"macocr/proxy/internal/scheduler"
)

const maxWebhookRequestBytes = 1024 * 1024
const maxNativeVerifyBytes = 4 * 1024
const maxNativeScanOutputBytes = 100 * 1024 * 1024

type WebhookHandler struct {
	docs       domain.DocumentRepository
	objects    domain.ObjectRepository
	authSecret string
	sched      *scheduler.Scheduler
	logger     *slog.Logger
	notifier   domain.NotificationPublisher
	results    domain.ResultCache
	resultTTL  time.Duration
	workers    *native.WorkerHub
}

// HandleNativeScanOutput stores the binary scan before its small completion
// callback is accepted. The body is signed with the same native HMAC scheme as
// callbacks, so an object key cannot be populated by an unauthenticated peer.
func (h *WebhookHandler) HandleNativeScanOutput(c *gin.Context) {
	scanID, attemptID := c.Param("scanId"), c.Param("attemptId")
	nodeID, ts, eventID, sig := c.GetHeader("X-Native-Node-Id"), c.GetHeader("X-Native-Timestamp"), c.GetHeader("X-Native-Event-Id"), c.GetHeader("X-Native-Signature")
	if eventID != "out_"+scanID+"_"+attemptID || c.ContentType() != "image/jpeg" {
		RespondProblem(c, errs.InvalidInput("invalid scan output request"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxNativeScanOutputBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if respondRequestTooLarge(c, err, maxNativeScanOutputBytes) {
			return
		}
		RespondProblem(c, errs.InvalidInput("failed to read scan output"))
		return
	}
	if len(body) == 0 {
		RespondProblem(c, errs.InvalidInput("scan output is empty"))
		return
	}
	secret, secretErr := h.nativeSecret(c, nodeID)
	if secretErr != nil || secret == "" || !h.nativePayloadAuthorized(c, secret, nodeID, ts, eventID, body, sig) {
		RespondProblem(c, errs.Unauthorized("invalid native scan output signature"))
		return
	}
	doc, err := h.docs.GetByID(c.Request.Context(), scanID)
	if err != nil {
		RespondProblem(c, errs.NotFound("scan not found"))
		return
	}
	if doc.Status != domain.StatusProcessing || doc.AttemptID != attemptID || doc.Options == nil || doc.Options.ProcessingMode != "scan" {
		RespondProblem(c, errs.New(errs.CodeStateConflict, http.StatusConflict, "Scan output does not match the active scan attempt"))
		return
	}
	if !h.assignedWorker(c, scanID, attemptID, nodeID) {
		return
	}
	key := scanOutputKey(doc, attemptID)
	if err := h.objects.Put(c.Request.Context(), key, bytes.NewReader(body), "image/jpeg"); err != nil {
		RespondProblem(c, errs.ServiceUnavailable("scan output storage is unavailable", err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "stored"})
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

// HandleNativeVerify performs a side-effect-free signed handshake so the
// native menu-bar app can validate the proxy URL, node ID, clock, and shared
// HMAC key before opening its OCR listener.
func (h *WebhookHandler) HandleNativeVerify(c *gin.Context) {
	if c.ContentType() != "application/json" {
		RespondProblem(c, errs.New(errs.CodeUnsupportedContentType, http.StatusUnsupportedMediaType, "Only application/json is supported"))
		return
	}
	nodeID := c.GetHeader("X-Native-Node-Id")
	ts := c.GetHeader("X-Native-Timestamp")
	eventID := c.GetHeader("X-Native-Event-Id")
	sig := c.GetHeader("X-Native-Signature")

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxNativeVerifyBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if respondRequestTooLarge(c, err, maxNativeVerifyBytes) {
			return
		}
		RespondProblem(c, errs.InvalidInput("failed to read verification body"))
		return
	}
	secret, secretErr := h.nativeSecret(c, nodeID)
	if secretErr != nil {
		RespondProblem(c, errs.Unauthorized("invalid worker credential"))
		return
	}
	if secret == "" {
		RespondProblem(c, errs.ServiceUnavailable("native authentication is not configured", nil))
		return
	}
	if !h.nativePayloadAuthorized(c, secret, nodeID, ts, eventID, body, sig) {
		h.logger.Warn("native connection verification failed", "nodeID", nodeID)
		RespondProblem(c, errs.Unauthorized("native shared key verification failed"))
		return
	}

	var payload struct {
		NodeID string `json:"nodeId"`
		Nonce  string `json:"nonce"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		RespondProblem(c, errs.InvalidInput("invalid native verification body"))
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		RespondProblem(c, errs.InvalidInput("verification body must contain one JSON object"))
		return
	}
	if payload.NodeID == "" || payload.NodeID != nodeID || len(payload.Nonce) < 16 || len(payload.Nonce) > 128 || len(eventID) < 16 || len(eventID) > 128 {
		RespondProblem(c, errs.InvalidInput("verification identity or nonce is invalid"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "verified", "nodeId": nodeID})
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

	secret, secretErr := h.nativeSecret(c, nodeID)
	if secretErr != nil {
		RespondProblem(c, errs.Unauthorized("invalid worker credential"))
		return
	}
	if secret != "" {
		if !h.nativePayloadAuthorized(c, secret, nodeID, ts, eventID, body, sig) {
			h.logger.Warn("webhook signature verification failed", "nodeID", nodeID)
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
	validOCREvent := (event.Type == "attempt.completed" && event.Result != nil) || (event.Type == "attempt.failed" && event.Error != "")
	validScanEvent := (event.Type == "scan.completed" && event.ScanResult != nil) || (event.Type == "scan.failed" && event.Error != "")
	if !validOCREvent && !validScanEvent {
		RespondProblem(c, errs.InvalidInput("event type and payload are inconsistent"))
		return
	}

	doc, err := h.docs.GetByID(c.Request.Context(), event.DocumentID)
	if err != nil {
		RespondProblem(c, errs.NotFound("document not found"))
		return
	}
	if !h.assignedWorker(c, event.DocumentID, event.AttemptID, nodeID) {
		return
	}
	if (event.Type == "scan.completed" || event.Type == "scan.failed") && (doc.Options == nil || doc.Options.ProcessingMode != "scan") {
		RespondProblem(c, errs.New(errs.CodeStateConflict, http.StatusConflict, "Callback does not match a scan job"))
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
		if h.workers != nil {
			h.workers.Complete(event.AttemptID)
		}
		c.JSON(http.StatusOK, gin.H{"status": "acknowledged", "eventId": event.EventID, "documentId": event.DocumentID})
		return
	}
	if doc.Status != domain.StatusProcessing || doc.AttemptID != event.AttemptID {
		RespondProblem(c, errs.New(errs.CodeStateConflict, http.StatusConflict, "Callback does not match the active document attempt"))
		return
	}

	if event.Type == "scan.completed" && event.ScanResult != nil {
		resultKey := scanOutputKey(doc, event.AttemptID)
		exists, storageErr := h.objects.Exists(c.Request.Context(), resultKey)
		if storageErr != nil || !exists {
			RespondProblem(c, errs.ServiceUnavailable("scan output storage is unavailable", storageErr))
			return
		}
		expiresAt := time.Now().Add(h.resultTTL)
		terminalDoc := *doc
		terminalDoc.Status, terminalDoc.ResultKey, terminalDoc.ResultExpiresAt = domain.StatusCompleted, resultKey, &expiresAt
		outboxEvent, buildErr := h.buildNotificationEvent(&terminalDoc)
		if buildErr != nil {
			RespondError(c, buildErr)
			return
		}
		if _, err = h.docs.FinalizeAttempt(c.Request.Context(), domain.DocumentFinalization{
			DocumentID: doc.ID, AttemptID: event.AttemptID, TerminalEventID: event.EventID,
			Status: domain.StatusCompleted, ResultKey: resultKey, ResultExpiresAt: &expiresAt,
		}, outboxEvent); err != nil {
			RespondError(c, err)
			return
		}
	} else if event.Type == "attempt.completed" && event.Result != nil {
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
	} else if event.Type == "attempt.failed" || event.Type == "scan.failed" {
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

	if h.workers != nil {
		h.workers.Complete(event.AttemptID)
	}
	if h.sched != nil {
		h.sched.Wake()
	}
	if h.notifier != nil {
		publishedDoc := *doc
		if event.Type == "attempt.completed" || event.Type == "scan.completed" {
			publishedDoc.Status = domain.StatusCompleted
		} else {
			publishedDoc.Status, publishedDoc.ErrorDetail = domain.StatusFailed, event.Error
		}
		if err := h.notifier.PublishDocument(c.Request.Context(), &publishedDoc); err != nil {
			h.logger.Error("failed to publish SSE lifecycle event", "documentID", doc.ID, "error", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "acknowledged",
		"eventId":    event.EventID,
		"documentId": event.DocumentID,
	})
}

func scanOutputKey(doc *domain.Document, attemptID string) string {
	return fmt.Sprintf("scan-results/%d/%s/%s.jpg", doc.UserID, doc.ID, attemptID)
}

func (h *WebhookHandler) buildNotificationEvent(doc *domain.Document) (*domain.NotificationEvent, error) {
	if h.notifier == nil {
		return nil, nil
	}
	return h.notifier.BuildDocumentEvent(doc)
}

// Use per-node credentials exclusively when outbound mode is enabled.
func (h *WebhookHandler) SetWorkerHub(hub *native.WorkerHub) { h.workers = hub }
func (h *WebhookHandler) nativeSecret(c *gin.Context, nodeID string) (string, error) {
	if h.workers != nil {
		return h.workers.ResolveSecret(c.Request.Context(), nodeID)
	}
	return h.authSecret, nil
}
func (h *WebhookHandler) assignedWorker(c *gin.Context, docID, attemptID, nodeID string) bool {
	if h.workers == nil {
		return true
	}
	assigned, err := h.workers.AssignedNode(c.Request.Context(), docID, attemptID)
	if err != nil || assigned != nodeID {
		RespondProblem(c, errs.New(errs.CodeStateConflict, http.StatusConflict, "Callback worker does not own this attempt"))
		return false
	}
	return true
}

func (h *WebhookHandler) nativePayloadAuthorized(c *gin.Context, secret, nodeID, ts, eventID string, body []byte, sig string) bool {
	if h.workers != nil && h.workers.SharedMode() {
		authorization := c.GetHeader("Authorization")
		return strings.HasPrefix(authorization, "Bearer ") && h.workers.AuthorizeBearer(c.Request.Context(), nodeID, strings.TrimPrefix(authorization, "Bearer "))
	}
	return native.VerifyWebhook(secret, nodeID, ts, eventID, body, sig) == nil
}
