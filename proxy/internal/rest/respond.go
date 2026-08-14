package rest

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/errs"
)

func RespondError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	var p *errs.Problem
	switch {
	case errors.Is(err, domain.ErrNotFound):
		p = errs.NotFound(err.Error())
	case errors.Is(err, domain.ErrResultExpired):
		p = errs.New(errs.CodeResultExpired, http.StatusGone, "OCR result has expired").WithDetail(err.Error())
	case errors.Is(err, domain.ErrConflict):
		p = errs.New(errs.CodeStateConflict, http.StatusConflict, "Conflict").WithDetail(err.Error())
	case errors.Is(err, domain.ErrInvalidSource):
		p = errs.New(errs.CodeInvalidSource, http.StatusBadRequest, "Invalid input source").WithDetail(err.Error())
	case errors.Is(err, domain.ErrInvalidBase64):
		p = errs.New(errs.CodeInvalidBase64, http.StatusBadRequest, "Invalid Base64 input").WithDetail(err.Error())
	case errors.Is(err, domain.ErrBase64TooLarge):
		p = errs.New(errs.CodeBase64TooLarge, http.StatusBadRequest, "Base64 input is too large").
			WithDetail(err.Error()+". Upload the file to your own storage and submit its HTTPS URL, or remove this item from the batch.").
			WithLimit("maxDecodedBytes", 25*1024*1024)
	case errors.Is(err, domain.ErrURLContentTooLarge):
		p = errs.New(errs.CodeURLContentTooLarge, http.StatusRequestEntityTooLarge, "URL content is too large").
			WithDetail(err.Error())
	case errors.Is(err, domain.ErrUnsupportedMediaType):
		p = errs.New(errs.CodeUnsupportedMediaType, http.StatusUnsupportedMediaType, "Unsupported file type").WithDetail(err.Error())
	case errors.Is(err, domain.ErrFileValidation):
		p = errs.New(errs.CodeFileValidationFailed, http.StatusBadRequest, "File validation failed").WithDetail(err.Error())
	case errors.Is(err, domain.ErrSSRFBlocked):
		p = errs.New(errs.CodeSSRFBlocked, http.StatusBadRequest, "URL is not allowed").WithDetail(err.Error())
	case errors.Is(err, domain.ErrInvalidURL):
		p = errs.New(errs.CodeInvalidURL, http.StatusBadRequest, "Invalid input URL").WithDetail(err.Error())
	case errors.Is(err, domain.ErrBadParamInput):
		p = errs.InvalidInput(err.Error())
	case errors.Is(err, domain.ErrUnauthorized):
		p = errs.Unauthorized("invalid or missing API key")
	case errors.Is(err, domain.ErrUserDisabled):
		p = errs.Forbidden("user account has been disabled")
	case errors.Is(err, domain.ErrRateLimited):
		c.Header("Retry-After", "1")
		p = errs.New(errs.CodeRateLimited, http.StatusTooManyRequests, "Rate limit exceeded").WithDetail(err.Error())
	case errors.Is(err, domain.ErrQuotaExceeded):
		c.Header("Retry-After", "60")
		p = errs.New(errs.CodeQuotaExceeded, http.StatusTooManyRequests, "Document quota exceeded").WithDetail(err.Error())
	case errors.Is(err, domain.ErrStorageUnavailable):
		p = errs.ServiceUnavailable("storage backend unavailable", err)
	default:
		p = errs.Internal("an unexpected error occurred")
	}
	RespondProblem(c, p)
}

func RespondProblem(c *gin.Context, p *errs.Problem) {
	c.Header("Content-Type", "application/problem+json")
	c.JSON(p.Status, p)
}
