package rest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/errs"
	"macocr/proxy/internal/usecase/document"
)

type DocumentHandler struct {
	svc         *document.Service
	apiBaseURL  string
	docsBaseURL string
}

func NewDocumentHandler(svc *document.Service, apiBaseURL, docsBaseURL string) *DocumentHandler {
	return &DocumentHandler{
		svc:         svc,
		apiBaseURL:  strings.TrimRight(apiBaseURL, "/"),
		docsBaseURL: strings.TrimRight(docsBaseURL, "/"),
	}
}

type submitDocJSONReq struct {
	Input        inputReq                   `json:"input" binding:"required"`
	Options      *domain.OCROptions         `json:"options,omitempty"`
	Notification *domain.NotificationConfig `json:"notification,omitempty"`
}

type inputReq struct {
	URL    string `json:"url,omitempty"`
	Base64 string `json:"base64,omitempty"`
}

const maxSingleJSONRequestBytes = 36 * 1024 * 1024

var documentIDPattern = regexp.MustCompile(`^doc_[A-Za-z0-9_-]{1,80}$`)

func (r inputReq) source() (document.InputSource, error) {
	hasURL := strings.TrimSpace(r.URL) != ""
	hasBase64 := strings.TrimSpace(r.Base64) != ""
	if hasURL == hasBase64 {
		return document.InputSource{}, fmt.Errorf("%w: input must contain exactly one of url or base64", domain.ErrInvalidSource)
	}
	if hasURL {
		return document.InputSource{Type: "url", URL: r.URL}, nil
	}
	return document.InputSource{Type: "base64", Base64Data: r.Base64}, nil
}

func (h *DocumentHandler) Submit(c *gin.Context) {
	k, ok := apiKeyFrom(c)
	if !ok {
		RespondProblem(c, errs.Unauthorized("authentication required"))
		return
	}

	if c.ContentType() != "application/json" {
		RespondProblem(c, errs.New(errs.CodeUnsupportedContentType, http.StatusUnsupportedMediaType, "Only application/json submissions are supported"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSingleJSONRequestBytes)
	var req submitDocJSONReq
	if err := decodeStrictRequestJSON(c, &req); err != nil {
		if respondRequestTooLarge(c, err, maxSingleJSONRequestBytes) {
			return
		}
		RespondProblem(c, errs.InvalidInput(err.Error()))
		return
	}
	input, err := req.Input.source()
	if err != nil {
		RespondError(c, err)
		return
	}

	doc, err := h.svc.SubmitSingle(c.Request.Context(), k.UserID, input, req.Options, req.Notification)
	if err != nil {
		RespondError(c, err)
		return
	}

	locationURL := fmt.Sprintf("%s/v1/documents/%s", h.apiBaseURL, doc.ID)
	c.Header("Location", locationURL)
	c.Header("Retry-After", "3")

	c.JSON(http.StatusAccepted, gin.H{
		"documentId": doc.ID,
		"status":     doc.Status,
		"createdAt":  doc.CreatedAt,
		"links":      h.documentLinks(doc.ID),
	})
}

func decodeStrictRequestJSON(c *gin.Context, target any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func respondRequestTooLarge(c *gin.Context, err error, maxBytes int64) bool {
	var maxErr *http.MaxBytesError
	if !errors.As(err, &maxErr) {
		return false
	}
	problem := errs.New(errs.CodePayloadTooLarge, http.StatusRequestEntityTooLarge, "Request body is too large").
		WithDetail("The complete HTTP request exceeds the allowed size.").WithLimit("maxRequestBytes", maxBytes)
	if strings.HasPrefix(c.FullPath(), "/v1/") {
		problem = withUploadRecoveryLinks(problem)
	}
	RespondProblem(c, problem)
	return true
}

func (h *DocumentHandler) Get(c *gin.Context) {
	k, ok := apiKeyFrom(c)
	if !ok {
		RespondProblem(c, errs.Unauthorized("authentication required"))
		return
	}

	docID := c.Param("id")
	if !documentIDPattern.MatchString(docID) {
		RespondProblem(c, errs.InvalidInput("document id format is invalid"))
		return
	}
	doc, err := h.svc.GetDocument(c.Request.Context(), k.UserID, docID)
	if err != nil {
		RespondError(c, err)
		return
	}

	etag := fmt.Sprintf(`"%s-%d"`, doc.Status, doc.UpdatedAt.Unix())
	if match := c.GetHeader("If-None-Match"); match == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.Header("ETag", etag)

	if doc.Status == domain.StatusQueued || doc.Status == domain.StatusProcessing {
		c.Header("Retry-After", "3")
	}

	c.JSON(http.StatusOK, h.documentResponse(doc))
}

func (h *DocumentHandler) documentLinks(docID string) []errs.Link {
	return []errs.Link{{
		Rel:  "self",
		Href: fmt.Sprintf("%s/v1/documents/%s", h.apiBaseURL, docID),
	}}
}

func (h *DocumentHandler) documentResponse(doc *domain.Document) gin.H {
	res := gin.H{
		"documentId":       doc.ID,
		"status":           doc.Status,
		"inputContentType": doc.InputContentType,
		"inputSizeBytes":   doc.InputSizeBytes,
		"createdAt":        doc.CreatedAt,
		"updatedAt":        doc.UpdatedAt,
		"links":            h.documentLinks(doc.ID),
	}
	if doc.Status == domain.StatusCompleted && doc.ResultExpiresAt != nil && !time.Now().Before(*doc.ResultExpiresAt) {
		res["resultExpired"] = true
		res["resultExpiresAt"] = doc.ResultExpiresAt
	} else if doc.Status == domain.StatusCompleted && doc.Result != nil {
		res["result"] = doc.Result
		res["resultExpiresAt"] = doc.ResultExpiresAt
	}
	if doc.ErrorDetail != "" {
		res["errorDetail"] = doc.ErrorDetail
	}
	return res
}
