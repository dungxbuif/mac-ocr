package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"macocr/proxy/domain"
	"macocr/proxy/internal/rest"
)

func TestBase64TooLargeProblemIncludesBatchIndexAndAction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/", func(c *gin.Context) {
		rest.RespondError(c, fmt.Errorf("batch item 3: %w", domain.ErrBase64TooLarge))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	var problem struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != "BASE64_TOO_LARGE" || !strings.Contains(problem.Detail, "batch item 3") || !strings.Contains(problem.Detail, "HTTPS URL") {
		t.Fatalf("unexpected problem: %s", w.Body.String())
	}
}
