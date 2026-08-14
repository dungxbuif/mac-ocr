package rest

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"macocr/proxy/internal/errs"
	"macocr/proxy/internal/notifications"
)

type NotificationHandler struct {
	svc *notifications.Service
}

func NewNotificationHandler(svc *notifications.Service) *NotificationHandler {
	return &NotificationHandler{svc: svc}
}

func (h *NotificationHandler) Events(c *gin.Context) {
	key, ok := apiKeyFrom(c)
	if !ok {
		RespondProblem(c, errs.Unauthorized("authentication required"))
		return
	}
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		RespondProblem(c, errs.New(errs.CodeInternal, http.StatusInternalServerError, "Streaming is unavailable"))
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache, no-store")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	_, _ = c.Writer.WriteString("retry: 3000\n\n")
	flusher.Flush()

	cursor := strings.TrimSpace(c.GetHeader("Last-Event-ID"))
	poll := time.NewTicker(time.Second)
	heartbeat := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	defer heartbeat.Stop()

	for {
		if revalidateAPIKey(c) != nil {
			return
		}
		events, err := h.svc.ListSSE(c.Request.Context(), key.UserID, cursor)
		if err != nil {
			return
		}
		for _, event := range events {
			_, _ = fmt.Fprintf(c.Writer, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, event.Payload)
			cursor = event.ID
		}
		if len(events) > 0 {
			flusher.Flush()
			continue
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = c.Writer.WriteString(": heartbeat\n\n")
			flusher.Flush()
		case <-poll.C:
		}
	}
}
