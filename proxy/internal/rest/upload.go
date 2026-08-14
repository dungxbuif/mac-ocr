package rest

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/errs"
	"macocr/proxy/internal/usecase/document"
)

type PresignedUploadRepository interface {
	PresignPutURL(ctx context.Context, key, contentType string, contentLength int64, ttl time.Duration) (string, http.Header, error)
	SourceURLForKey(key string) string
}

type accountConfigInvalidator interface {
	InvalidateAccountConfig(ctx context.Context, userID int64)
}

type UploadHandler struct {
	objects        PresignedUploadRepository
	reservations   domain.UploadReservationRepository
	maxUploadBytes int64
	ttl            time.Duration
	reservationTTL time.Duration
	invalidator    accountConfigInvalidator
}

func NewUploadHandler(objects PresignedUploadRepository, maxUploadBytes int64, repositories ...domain.UploadReservationRepository) *UploadHandler {
	var reservations domain.UploadReservationRepository
	if len(repositories) > 0 {
		reservations = repositories[0]
	}
	return NewUploadHandlerWithReservationTTL(objects, maxUploadBytes, reservations, 24*time.Hour)
}

func NewUploadHandlerWithReservationTTL(objects PresignedUploadRepository, maxUploadBytes int64, reservations domain.UploadReservationRepository, reservationTTL time.Duration, invalidators ...accountConfigInvalidator) *UploadHandler {
	if maxUploadBytes <= 0 {
		maxUploadBytes = document.MaxUploadedObjectBytes
	}
	if reservationTTL <= 0 {
		reservationTTL = 24 * time.Hour
	}
	var invalidator accountConfigInvalidator
	if len(invalidators) > 0 {
		invalidator = invalidators[0]
	}
	return &UploadHandler{objects: objects, reservations: reservations, invalidator: invalidator, maxUploadBytes: maxUploadBytes, ttl: 15 * time.Minute, reservationTTL: reservationTTL}
}

type presignUploadReq struct {
	Filename    string `json:"filename" binding:"required"`
	SizeBytes   int64  `json:"sizeBytes" binding:"required"`
	ContentType string `json:"contentType,omitempty"`
}

const maxPresignJSONRequestBytes = 8 << 10

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

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPresignJSONRequestBytes)
	var req presignUploadReq
	if err := decodeStrictRequestJSON(c, &req); err != nil {
		if respondRequestTooLarge(c, err, maxPresignJSONRequestBytes) {
			return
		}
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
	expiresAt := time.Now().UTC().Add(h.ttl)
	reservationExpiresAt := time.Now().UTC().Add(h.reservationTTL)
	if h.reservations == nil {
		RespondError(c, domain.ErrStorageUnavailable)
		return
	}
	if err := h.reservations.ReserveUpload(c.Request.Context(), domain.UploadReservation{
		ObjectKey: key, UserID: k.UserID, SizeBytes: req.SizeBytes, ExpiresAt: reservationExpiresAt,
	}); err != nil {
		RespondError(c, err)
		return
	}
	if h.invalidator != nil {
		h.invalidator.InvalidateAccountConfig(c.Request.Context(), k.UserID)
	}
	uploadURL, signedHeaders, err := h.objects.PresignPutURL(c.Request.Context(), key, req.ContentType, req.SizeBytes, h.ttl)
	if err != nil {
		_, _ = h.reservations.ReleaseUpload(c.Request.Context(), k.UserID, key)
		if h.invalidator != nil {
			h.invalidator.InvalidateAccountConfig(c.Request.Context(), k.UserID)
		}
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

	c.JSON(http.StatusCreated, gin.H{
		"uploadUrl":            uploadURL,
		"sourceUrl":            h.objects.SourceURLForKey(key),
		"method":               "PUT",
		"expiresAt":            expiresAt,
		"reservationExpiresAt": reservationExpiresAt,
		"maxUploadBytes":       h.maxUploadBytes,
		"sizeBytes":            req.SizeBytes,
		"headers":              headers,
	})
}
