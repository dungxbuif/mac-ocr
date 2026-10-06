package rest

import (
	"crypto/rand"
	"encoding/hex"
	"github.com/gin-gonic/gin"
	"macocr/proxy/internal/errs"
	"macocr/proxy/internal/native"
	"net/http"
	"strings"
	"time"
)

type WorkersHandler struct {
	hub   *native.WorkerHub
	store native.WorkerStore
}

func NewWorkersHandler(hub *native.WorkerHub, store native.WorkerStore) *WorkersHandler {
	return &WorkersHandler{hub, store}
}
func workerToken() (string, error) {
	var b [32]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}
func (h *WorkersHandler) CreateEnrollment(c *gin.Context) {
	token, err := workerToken()
	if err != nil {
		RespondError(c, err)
		return
	}
	expires := time.Now().UTC().Add(15 * time.Minute)
	if err := h.store.CreateEnrollment(c.Request.Context(), token, expires); err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"token": token, "expiresAt": expires})
}
func (h *WorkersHandler) Enroll(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req struct {
		Token  string `json:"token"`
		NodeID string `json:"nodeId"`
		Name   string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Token) != 64 || len(req.NodeID) < 8 || len(req.NodeID) > 128 || len(req.Name) > 128 || strings.TrimSpace(req.Name) == "" {
		RespondProblem(c, errs.InvalidInput("invalid enrollment request"))
		return
	}
	for _, ch := range req.NodeID {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			RespondProblem(c, errs.InvalidInput("invalid node ID"))
			return
		}
	}
	secret, err := workerToken()
	if err != nil {
		RespondError(c, err)
		return
	}
	if err := h.store.Enroll(c.Request.Context(), req.Token, req.NodeID, req.Name, secret); err != nil {
		RespondProblem(c, errs.Unauthorized("enrollment token expired, consumed, or invalid"))
		return
	}
	h.hub.Reenrolled(req.NodeID)
	c.JSON(http.StatusCreated, gin.H{"nodeId": req.NodeID, "secret": secret})
}
func (h *WorkersHandler) Connect(c *gin.Context) {
	nodeID := c.GetHeader("X-Native-Node-Id")
	authorization := c.GetHeader("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") || !h.hub.Authenticate(c.Request.Context(), nodeID, strings.TrimPrefix(authorization, "Bearer ")) {
		RespondProblem(c, errs.Unauthorized("invalid worker credential"))
		return
	}
	h.hub.ServeSocket(c.Writer, c.Request, nodeID, strings.TrimPrefix(authorization, "Bearer "))
}
func (h *WorkersHandler) List(c *gin.Context) {
	nodes, routes, err := h.hub.Snapshot(c.Request.Context())
	if err != nil {
		RespondError(c, err)
		return
	}
	mode := "enrollment"
	if h.hub.SharedMode() {
		mode = "shared"
	}
	c.JSON(http.StatusOK, gin.H{"nodes": nodes, "routes": routes, "authMode": mode})
}
func (h *WorkersHandler) Control(c *gin.Context) {
	var req struct {
		Action string `json:"action"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondProblem(c, errs.InvalidInput("invalid control request"))
		return
	}
	if err := h.hub.Control(c.Request.Context(), c.Param("nodeId"), req.Action); err != nil {
		RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "accepted"})
}
