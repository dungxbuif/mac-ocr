package rest

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/errs"
	"macocr/proxy/internal/usecase/document"
)

var scanIDPattern = regexp.MustCompile(`^scan_[A-Za-z0-9_-]{1,80}$`)

type ScanHandler struct {
	svc        *document.Service
	apiBaseURL string
}

func NewScanHandler(svc *document.Service, apiBaseURL string) *ScanHandler {
	return &ScanHandler{svc: svc, apiBaseURL: strings.TrimRight(apiBaseURL, "/")}
}

type submitScanJSONReq struct {
	Input        inputReq                   `json:"input" binding:"required"`
	Notification *domain.NotificationConfig `json:"notification,omitempty"`
}

func (h *ScanHandler) Submit(c *gin.Context) {
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
	var req submitScanJSONReq
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
	scan, err := h.svc.SubmitScan(c.Request.Context(), k.UserID, input, req.Notification)
	if err != nil {
		RespondError(c, err)
		return
	}
	location := fmt.Sprintf("%s/v1/scans/%s", h.apiBaseURL, scan.ID)
	c.Header("Location", location)
	c.Header("Retry-After", "3")
	c.JSON(http.StatusAccepted, gin.H{"scanId": scan.ID, "status": scan.Status, "createdAt": scan.CreatedAt})
}

func (h *ScanHandler) Get(c *gin.Context) {
	k, ok := apiKeyFrom(c)
	if !ok {
		RespondProblem(c, errs.Unauthorized("authentication required"))
		return
	}
	scanID := c.Param("id")
	if !scanIDPattern.MatchString(scanID) {
		RespondProblem(c, errs.InvalidInput("scan id format is invalid"))
		return
	}
	scan, downloadURL, err := h.svc.ScanDownloadURL(c.Request.Context(), k.UserID, scanID)
	if err != nil {
		RespondError(c, err)
		return
	}
	if scan.Status == domain.StatusQueued || scan.Status == domain.StatusProcessing {
		c.Header("Retry-After", "3")
	}
	res := gin.H{"scanId": scan.ID, "status": scan.Status, "createdAt": scan.CreatedAt, "updatedAt": scan.UpdatedAt}
	if downloadURL != "" {
		res["downloadUrl"] = downloadURL
		res["downloadUrlExpiresAt"] = time.Now().Add(15 * time.Minute)
	}
	if scan.ErrorDetail != "" {
		res["errorDetail"] = scan.ErrorDetail
	}
	c.JSON(http.StatusOK, res)
}
