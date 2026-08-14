package errs

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

type Code string

const (
	CodeInvalidInput         Code = "INVALID_INPUT"
	CodeMalformedJSON        Code = "MALFORMED_JSON"
	CodeMissingField         Code = "MISSING_FIELD"
	CodeInvalidField         Code = "INVALID_FIELD"
	CodeUnsupportedMediaType Code = "UNSUPPORTED_MEDIA_TYPE"
	CodePayloadTooLarge      Code = "PAYLOAD_TOO_LARGE"
	CodeInvalidSource        Code = "INVALID_SOURCE"
	CodeInvalidBase64        Code = "INVALID_BASE64"
	CodeBase64TooLarge       Code = "BASE64_TOO_LARGE"
	CodeInvalidURL           Code = "INVALID_URL"
	CodeSSRFBlocked          Code = "SSRF_BLOCKED"
	CodeFileValidationFailed Code = "FILE_VALIDATION_FAILED"

	CodeUnauthorized Code = "UNAUTHORIZED"
	CodeForbidden    Code = "FORBIDDEN"

	CodeNotFound         Code = "NOT_FOUND"
	CodeDocumentNotFound Code = "DOCUMENT_NOT_FOUND"
	CodeBatchNotFound    Code = "BATCH_NOT_FOUND"
	CodeUploadNotFound   Code = "UPLOAD_NOT_FOUND"

	CodeStateConflict Code = "STATE_CONFLICT"

	CodeResultExpired Code = "RESULT_EXPIRED"

	CodeMissingContentLength Code = "MISSING_CONTENT_LENGTH"
	CodeURLContentTooLarge   Code = "URL_CONTENT_TOO_LARGE"

	CodeUnsupportedContentType Code = "UNSUPPORTED_CONTENT_TYPE"
	CodeBatchValidationFailed  Code = "BATCH_VALIDATION_FAILED"
	CodeOptionValidationFailed Code = "OPTION_VALIDATION_FAILED"

	CodeRateLimited          Code = "RATE_LIMITED"
	CodeQuotaExceeded        Code = "QUOTA_EXCEEDED"
	CodeStorageQuotaExceeded Code = "STORAGE_QUOTA_EXCEEDED"

	CodeInternal       Code = "INTERNAL_ERROR"
	CodeNotImplemented Code = "NOT_IMPLEMENTED"

	CodeServiceUnavailable Code = "SERVICE_UNAVAILABLE"
	CodeNativeUnavailable  Code = "NATIVE_UNAVAILABLE"
	CodeCircuitOpen        Code = "CIRCUIT_OPEN"
)

type Problem struct {
	Type   string            `json:"type"`
	Code   Code              `json:"code"`
	Status int               `json:"status"`
	Title  string            `json:"title"`
	Detail string            `json:"detail,omitempty"`
	Limits map[string]any    `json:"limits,omitempty"`
	Links  []Link            `json:"links,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

type Link struct {
	Rel    string `json:"rel"`
	Href   string `json:"href"`
	Method string `json:"method,omitempty"`
}

func (p *Problem) Error() string {
	return fmt.Sprintf("%s (%d): %s", p.Code, p.Status, p.Title)
}

func (p *Problem) WithDetail(detail string) *Problem {
	c := *p
	c.Detail = detail
	return &c
}

func (p *Problem) WithField(field, msg string) *Problem {
	c := *p
	if c.Fields == nil {
		c.Fields = map[string]string{}
	}
	c.Fields[field] = msg
	return &c
}

func (p *Problem) WithLink(rel string, href string, method string) *Problem {
	c := *p
	c.Links = append([]Link(nil), p.Links...)
	link := Link{Rel: rel, Href: href, Method: method}
	for i := range c.Links {
		if c.Links[i].Rel == rel {
			c.Links[i] = link
			return &c
		}
	}
	c.Links = append(c.Links, link)
	return &c
}

func (p *Problem) WithLimit(name string, value any) *Problem {
	c := *p
	if c.Limits == nil {
		c.Limits = map[string]any{}
	}
	c.Limits[name] = value
	return &c
}

func (p *Problem) MarshalJSON() ([]byte, error) {
	type alias Problem
	return json.Marshal((*alias)(p))
}

func New(code Code, status int, title string) *Problem {
	return &Problem{
		Type:   "about:blank",
		Code:   code,
		Status: status,
		Title:  title,
	}
}

func InvalidInput(detail string) *Problem {
	return New(CodeInvalidInput, http.StatusBadRequest, "Invalid input").WithDetail(detail)
}

func Unauthorized(detail string) *Problem {
	return New(CodeUnauthorized, http.StatusUnauthorized, "Unauthorized").WithDetail(detail)
}

func Forbidden(detail string) *Problem {
	return New(CodeForbidden, http.StatusForbidden, "Forbidden").WithDetail(detail)
}

func NotFound(detail string) *Problem {
	return New(CodeNotFound, http.StatusNotFound, "Not found").WithDetail(detail)
}

func Internal(detail string) *Problem {
	return New(CodeInternal, http.StatusInternalServerError, "Internal error").WithDetail(detail)
}

func ServiceUnavailable(detail string, _ error) *Problem {
	p := New(CodeServiceUnavailable, http.StatusServiceUnavailable, "Service unavailable")
	if detail != "" {
		p = p.WithDetail(detail)
	}
	return p
}

func AsProblem(err error) *Problem {
	if err == nil {
		return nil
	}
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	return Internal("an unexpected error occurred")
}
