package rest

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/errs"
	"macocr/proxy/internal/usecase/document"
)

type BatchHandler struct {
	svc         *document.Service
	apiBaseURL  string
	docsBaseURL string
}

func NewBatchHandler(svc *document.Service, apiBaseURL, docsBaseURL string) *BatchHandler {
	return &BatchHandler{
		svc:         svc,
		apiBaseURL:  strings.TrimRight(apiBaseURL, "/"),
		docsBaseURL: strings.TrimRight(docsBaseURL, "/"),
	}
}

type batchItemReq struct {
	Input        inputReq                   `json:"input" binding:"required"`
	Options      *domain.OCROptions         `json:"options,omitempty"`
	Notification *domain.NotificationConfig `json:"notification,omitempty"`
}

const maxBatchJSONRequestBytes = 128 * 1024 * 1024

func (h *BatchHandler) Submit(c *gin.Context) {
	k, ok := apiKeyFrom(c)
	if !ok {
		RespondProblem(c, errs.Unauthorized("authentication required"))
		return
	}
	if c.ContentType() != "application/json" {
		RespondProblem(c, errs.New(errs.CodeUnsupportedContentType, http.StatusUnsupportedMediaType, "Only application/json submissions are supported"))
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBatchJSONRequestBytes)
	var req []batchItemReq
	if err := decodeStrictRequestJSON(c, &req); err != nil {
		if respondRequestTooLarge(c, err, maxBatchJSONRequestBytes) {
			return
		}
		RespondProblem(c, errs.InvalidInput(err.Error()))
		return
	}

	if len(req) == 0 || len(req) > 100 {
		RespondProblem(c, errs.InvalidInput("batch must contain between 1 and 100 items"))
		return
	}

	items := make([]document.BatchItemInput, len(req))
	for i, it := range req {
		input, err := it.Input.source()
		if err != nil {
			RespondError(c, fmt.Errorf("batch item %d: %w", i, err))
			return
		}
		items[i] = document.BatchItemInput{
			Input:        input,
			Options:      it.Options,
			Notification: it.Notification,
		}
	}

	docs, err := h.svc.SubmitBatch(c.Request.Context(), k.UserID, items)
	if err != nil {
		RespondError(c, err)
		return
	}

	docItems := make([]gin.H, len(docs))
	for i, d := range docs {
		docItems[i] = gin.H{
			"index":      i,
			"documentId": d.ID,
			"status":     d.Status,
			"links": []errs.Link{{
				Rel:  "self",
				Href: fmt.Sprintf("%s/v1/documents/%s", h.apiBaseURL, d.ID),
			}},
		}
	}

	c.Header("Retry-After", "3")

	c.JSON(http.StatusAccepted, gin.H{
		"status": "accepted",
		"summary": gin.H{
			"total":    len(docs),
			"accepted": len(docs),
			"rejected": 0,
		},
		"items": docItems,
	})
}
