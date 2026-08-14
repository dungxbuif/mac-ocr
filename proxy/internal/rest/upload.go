package rest

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"macocr/proxy/internal/errs"
	"macocr/proxy/internal/usecase/document"
)

type PresignedUploadRepository interface {
	PresignPutURL(ctx context.Context, key, contentType string, contentLength int64, ttl time.Duration) (string, http.Header, error)
	SourceURLForKey(key string) string
}

type UploadHandler struct {
	objects        PresignedUploadRepository
	maxUploadBytes int64
	ttl            time.Duration
}

func NewUploadHandler(objects PresignedUploadRepository, maxUploadBytes int64) *UploadHandler {
	if maxUploadBytes <= 0 {
		maxUploadBytes = document.MaxUploadedObjectBytes
	}
	return &UploadHandler{objects: objects, maxUploadBytes: maxUploadBytes, ttl: 15 * time.Minute}
}

type presignUploadReq struct {
	Filename    string `json:"filename" binding:"required"`
	SizeBytes   int64  `json:"sizeBytes" binding:"required"`
	ContentType string `json:"contentType,omitempty"`
}

func (h *UploadHandler) Presign(c *gin.Context) {
	k, ok := apiKeyFrom(c)
	if !ok {
		RespondProblem(c, errs.Unauthorized("authentication required"))
		return
	}
	if c.ContentType() != "application/json" {
		RespondProblem(c, errs.New(errs.CodeUnsupportedContentType, http.StatusUnsupportedMediaType, "Only application/json requests are supported"))
		return
	}

	var req presignUploadReq
	if err := decodeStrictRequestJSON(c, &req); err != nil {
		RespondProblem(c, errs.InvalidInput(err.Error()))
		return
	}
	req.Filename = strings.TrimSpace(req.Filename)
	req.ContentType = strings.TrimSpace(req.ContentType)
	if req.Filename == "" || len(req.Filename) > 255 {
		RespondProblem(c, errs.InvalidInput("filename must contain 1-255 characters"))
		return
	}
	if req.SizeBytes <= 0 {
		RespondProblem(c, errs.InvalidInput("sizeBytes must be greater than zero"))
		return
	}
	if req.SizeBytes > h.maxUploadBytes {
		RespondProblem(c, withUploadRecoveryLinks(errs.New(errs.CodeURLContentTooLarge, http.StatusRequestEntityTooLarge, "Upload file is too large").
			WithDetail("sizeBytes exceeds the configured upload limit").
			WithLimit("maxUploadBytes", h.maxUploadBytes)))
		return
	}

	key := document.MakeUploadKey(k.UserID, req.Filename)
	uploadURL, signedHeaders, err := h.objects.PresignPutURL(c.Request.Context(), key, req.ContentType, req.SizeBytes, h.ttl)
	if err != nil {
		RespondError(c, err)
		return
	}

	headers := map[string]string{}
	for name, values := range signedHeaders {
		if strings.EqualFold(name, "host") || len(values) == 0 {
			continue
		}
		headers[name] = values[0]
	}
	if req.ContentType != "" {
		headers["Content-Type"] = req.ContentType
	}

	expiresAt := time.Now().UTC().Add(h.ttl)
	c.JSON(http.StatusCreated, gin.H{
		"uploadUrl":      uploadURL,
		"sourceUrl":      h.objects.SourceURLForKey(key),
		"method":         "PUT",
		"expiresAt":      expiresAt,
		"maxUploadBytes": h.maxUploadBytes,
		"sizeBytes":      req.SizeBytes,
		"headers":        headers,
	})
}
