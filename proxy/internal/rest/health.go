package rest

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type HealthService interface {
	Ready(ctx context.Context) error
}

type HealthHandler struct {
	svc HealthService
}

func NewHealthHandler(svc HealthService) *HealthHandler {
	return &HealthHandler{svc: svc}
}

func (h *HealthHandler) Register(g *gin.RouterGroup) {
	g.GET("/healthz", h.Healthz)
	g.GET("/readyz", h.Readyz)
}

func (h *HealthHandler) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *HealthHandler) Readyz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	if err := h.svc.Ready(ctx); err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
