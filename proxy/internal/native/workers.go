package native

import (
	"context"
	"crypto/subtle"
	"errors"
	"github.com/gorilla/websocket"
	"macocr/proxy/domain"
	"net/http"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type Transport interface {
	GetCapacity(context.Context) (*domain.NativeCapacity, error)
	DispatchOCR(context.Context, *domain.NativeOCRRequest) error
	DispatchScan(context.Context, *domain.NativeScanRequest) error
}
type CapabilityTransport interface{ Supports(string) bool }

type ReservationProvider interface {
	Reserve(context.Context) (Transport, *domain.NativeCapacity, func(), error)
}
type WorkerMetadata struct {
	Model        string `json:"model,omitempty"`
	User         string `json:"user,omitempty"`
	Hostname     string `json:"hostname,omitempty"`
	OS           string `json:"os,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	CPUCores     int    `json:"cpuCores,omitempty"`
	MemoryBytes  int64  `json:"memoryBytes,omitempty"`
}

func ValidWorkerMetadata(m WorkerMetadata) bool {
	if m == (WorkerMetadata{}) {
		return true
	}
	if m.CPUCores < 1 || m.CPUCores > 4096 || m.MemoryBytes < 0 || m.MemoryBytes > 1<<44 {
		return false
	}
	for _, value := range []string{m.Model, m.User, m.Hostname, m.OS, m.Architecture} {
		if len(value) > 256 || !utf8.ValidString(value) {
			return false
		}
		for _, character := range value {
			if !unicode.IsPrint(character) {
				return false
			}
		}
	}
	return true
}

type WorkerNode struct {
	NodeID        string                `json:"nodeId"`
	Name          string                `json:"name"`
	Version       string                `json:"version"`
	State         string                `json:"state"`
	Connected     bool                  `json:"connected"`
	Paused        bool                  `json:"paused"`
	LastSeen      time.Time             `json:"lastSeen"`
	Capacity      domain.NativeCapacity `json:"capacity"`
	Capabilities  []string              `json:"capabilities"`
	Revoked       bool                  `json:"revoked"`
	Metadata      *WorkerMetadata       `json:"metadata,omitempty"`
	ControlAction string                `json:"-"`
}
type WorkerRoute struct {
	NodeID     string    `json:"nodeId"`
	DocumentID string    `json:"documentId"`
	AttemptID  string    `json:"attemptId"`
	Kind       string    `json:"kind"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"createdAt"`
}
type WorkerStore interface {
	CreateEnrollment(context.Context, string, time.Time) error
	Enroll(context.Context, string, string, string, string) error
	Secret(context.Context, string) (string, error)
	ListWorkers(context.Context) ([]WorkerNode, error)
	Revoke(context.Context, string) error
	Assign(context.Context, WorkerRoute) error
	AssignedNode(context.Context, string, string) (string, error)
	Routes(context.Context) ([]WorkerRoute, error)
}
type WorkerMessage struct {
	Type         string                    `json:"type"`
	RequestID    string                    `json:"requestId,omitempty"`
	Name         string                    `json:"name,omitempty"`
	Version      string                    `json:"version,omitempty"`
	Capabilities []string                  `json:"capabilities,omitempty"`
	Capacity     *domain.NativeCapacity    `json:"capacity,omitempty"`
	OCR          *domain.NativeOCRRequest  `json:"ocr,omitempty"`
	Scan         *domain.NativeScanRequest `json:"scan,omitempty"`
	Accepted     bool                      `json:"accepted,omitempty"`
	Error        string                    `json:"error,omitempty"`
	Action       string                    `json:"action,omitempty"`
	Metadata     *WorkerMetadata           `json:"metadata,omitempty"`
}
type workerSession struct {
	node         WorkerNode
	conn         *websocket.Conn
	writeMu      sync.Mutex
	selectedAt   time.Time
	pending      map[string]chan bool
	reservations map[*workerReservation]struct{}
}
type WorkerHub struct {
	mu           sync.Mutex
	store        WorkerStore
	sessions     map[string]*workerSession
	wake         func()
	lease        time.Duration
	sharedSecret string
}

func NewWorkerHub(store WorkerStore) *WorkerHub {
	return &WorkerHub{store: store, sessions: map[string]*workerSession{}, lease: 15 * time.Minute}
}
func (h *WorkerHub) SetWake(wake func()) { h.mu.Lock(); h.wake = wake; h.mu.Unlock() }
func (h *WorkerHub) ResolveSecret(ctx context.Context, nodeID string) (string, error) {
	if nodeID == "" {
		return "", errors.New("missing worker identity")
	}
	secret, err := h.store.Secret(ctx, nodeID)
	if err != nil {
		return "", err
	}
	if h.sharedSecret != "" {
		return h.sharedSecret, nil
	}
	return secret, nil
}
func (h *WorkerHub) AssignedNode(ctx context.Context, docID, attemptID string) (string, error) {
	return h.store.AssignedNode(ctx, docID, attemptID)
}
func (h *WorkerHub) SharedMode() bool              { return h.sharedSecret != "" }
func (h *WorkerHub) SetSharedSecret(secret string) { h.sharedSecret = secret }
func (h *WorkerHub) AuthorizeBearer(ctx context.Context, nodeID, secret string) bool {
	expected, err := h.ResolveSecret(ctx, nodeID)
	return err == nil && expected != "" && subtle.ConstantTimeCompare([]byte(expected), []byte(secret)) == 1
}
func (h *WorkerHub) Authenticate(ctx context.Context, nodeID, secret string) bool {
	if h.sharedSecret == "" {
		return h.AuthorizeBearer(ctx, nodeID, secret)
	}
	if len(nodeID) < 8 || len(nodeID) > 128 || subtle.ConstantTimeCompare([]byte(h.sharedSecret), []byte(secret)) != 1 {
		return false
	}
	for _, ch := range nodeID {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return false
		}
	}
	registration, ok := h.store.(interface {
		RegisterShared(context.Context, string, string) error
	})
	if !ok {
		return false
	}
	if err := registration.RegisterShared(ctx, nodeID, h.sharedSecret); err != nil {
		return false
	}
	return h.AuthorizeBearer(ctx, nodeID, secret)
}

func (h *WorkerHub) ServeSocket(w http.ResponseWriter, r *http.Request, nodeID, authenticatedSecret string) {
	desiredAction := "resume"
	if controls, ok := h.store.(interface {
		DesiredControl(context.Context, string) (string, error)
	}); ok {
		var err error
		desiredAction, err = controls.DesiredControl(r.Context(), nodeID)
		if err != nil {
			http.Error(w, "worker state unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	conn, err := (&websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(64 << 10)
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	s := &workerSession{node: WorkerNode{NodeID: nodeID, Connected: true, Paused: desiredAction != "resume", ControlAction: desiredAction, LastSeen: time.Now(), Capabilities: []string{}}, conn: conn, pending: map[string]chan bool{}, reservations: map[*workerReservation]struct{}{}}
	h.mu.Lock()
	old := h.sessions[nodeID]
	if old != nil {
		s.node.Paused = old.node.Paused
		s.node.ControlAction = old.node.ControlAction
		for reservation := range old.reservations {
			s.reservations[reservation] = struct{}{}
		}
		old.reservations = map[*workerReservation]struct{}{}
	}
	h.sessions[nodeID] = s
	h.mu.Unlock()
	if old != nil && old.conn != nil {
		_ = old.conn.Close()
	}
	defer func() {
		_ = conn.Close()
		h.mu.Lock()
		if h.sessions[nodeID] == s {
			s.node.Connected = false
			s.node.State = "offline"
		}
		h.mu.Unlock()
	}()
	hello := false
	for {
		var msg WorkerMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		if msg.Type != "hello" && msg.Type != "capacity" && msg.Type != "ack" {
			return
		}
		if !hello && msg.Type != "hello" {
			return
		}
		if msg.Type == "hello" {
			if hello || len(msg.Name) > 128 || len(msg.Version) > 128 || len(msg.Capabilities) > 8 {
				return
			}
			if msg.Metadata != nil && !ValidWorkerMetadata(*msg.Metadata) {
				return
			}
			hello = true
			if !h.AuthorizeBearer(r.Context(), nodeID, authenticatedSecret) {
				return
			}
			if msg.Metadata != nil {
				if store, ok := h.store.(interface {
					UpdateMetadata(context.Context, string, WorkerMetadata) error
				}); ok {
					if err := store.UpdateMetadata(r.Context(), nodeID, *msg.Metadata); err != nil {
						return
					}
				}
			}

		}
		if msg.Capacity != nil && !validCapacity(*msg.Capacity) {
			return
		}
		h.mu.Lock()
		if h.sessions[nodeID] != s {
			h.mu.Unlock()
			return
		}
		s.node.LastSeen = time.Now()
		if msg.Type == "hello" {
			s.node.Name = msg.Name
			s.node.Version = msg.Version
			s.node.Capabilities = msg.Capabilities
			s.node.Metadata = msg.Metadata
		}
		if msg.Capacity != nil {
			s.node.Capacity = *msg.Capacity
			s.node.State = msg.Capacity.State
		}
		if msg.Type == "ack" {
			if ch := s.pending[msg.RequestID]; ch != nil {
				select {
				case ch <- msg.Accepted:
				default:
				}
			}
		}
		wake := h.wake
		h.mu.Unlock()
		if msg.Type == "hello" {
			if err := s.write(WorkerMessage{Type: "welcome"}); err != nil {
				return
			}
			h.mu.Lock()
			paused := s.node.Paused
			action := s.node.ControlAction
			h.mu.Unlock()
			if paused {
				if action != "drain" {
					action = "pause"
				}
				if err := s.write(WorkerMessage{Type: "control", Action: action}); err != nil {
					return
				}
			}
		}
		_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
		if wake != nil && msg.Type != "ack" {
			wake()
		}
	}
}
func validCapacity(c domain.NativeCapacity) bool {
	return c.EffectiveLimit >= 0 && c.EffectiveLimit <= 1024 && c.Available >= 0 && c.Available <= c.EffectiveLimit && c.Active >= 0 && c.Active <= 1024 && c.AvailableUnits >= 0 && c.AvailableUnits <= 1048576 && c.ActiveUnits >= 0 && c.ActiveUnits <= 1048576 && c.ImageJobUnits >= 0 && c.ImageJobUnits <= 1048576 && c.PDFJobUnits >= 0 && c.PDFJobUnits <= 1048576
}
func (s *workerSession) write(msg WorkerMessage) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.conn == nil {
		return errors.New("worker disconnected")
	}
	_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return s.conn.WriteJSON(msg)
}
func (h *WorkerHub) Snapshot(ctx context.Context) ([]WorkerNode, []WorkerRoute, error) {
	nodes, err := h.store.ListWorkers(ctx)
	if err != nil {
		return nil, nil, err
	}
	routes, err := h.store.Routes(ctx)
	if err != nil {
		return nil, nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range nodes {
		if s := h.sessions[nodes[i].NodeID]; s != nil {
			revoked := nodes[i].Revoked
			metadata := nodes[i].Metadata
			controlAction := nodes[i].ControlAction
			nodes[i] = s.node
			if nodes[i].Metadata == nil {
				nodes[i].Metadata = metadata
			}
			nodes[i].Revoked = revoked
			if nodes[i].ControlAction == "" {
				nodes[i].ControlAction = controlAction
			}
			nodes[i].Connected = nodes[i].Connected && !revoked && time.Since(nodes[i].LastSeen) < 20*time.Second
		}
		if !nodes[i].Connected {
			nodes[i].State = "offline"
		}
	}
	return nodes, routes, nil
}
func (h *WorkerHub) Control(ctx context.Context, nodeID, action string) error {
	if action != "pause" && action != "resume" && action != "drain" && action != "revoke" && action != "activate" {
		return domain.ErrBadParamInput
	}
	if action == "revoke" {
		if err := h.store.Revoke(ctx, nodeID); err != nil {
			return err
		}
	}
	if action == "activate" {
		activator, ok := h.store.(interface {
			Activate(context.Context, string) error
		})
		if !ok {
			return errors.New("worker store does not support activation")
		}
		if err := activator.Activate(ctx, nodeID); err != nil {
			return err
		}
	}
	persisted := false
	if action != "revoke" && action != "activate" {
		if controls, ok := h.store.(interface {
			SetDesiredControl(context.Context, string, string) error
		}); ok {
			if err := controls.SetDesiredControl(ctx, nodeID, action); err != nil {
				return err
			}
			persisted = true
		}
	}
	h.mu.Lock()
	s := h.sessions[nodeID]
	if s != nil {
		s.node.Paused = action != "resume"
		s.node.ControlAction = action
		if action == "revoke" {
			s.node.Revoked = true
			s.node.Connected = false
		} else if action == "activate" {
			s.node.Revoked = false
			s.node.Paused = false
			s.node.ControlAction = "resume"
		}
	}
	h.mu.Unlock()
	if s == nil {
		if action == "revoke" || action == "activate" || persisted {
			return nil
		}
		return domain.ErrNotFound
	}
	if action == "revoke" {
		_ = s.conn.Close()
		return nil
	}
	if action == "activate" {
		return nil
	}
	return s.write(WorkerMessage{Type: "control", Action: action})
}
func (h *WorkerHub) GetCapacity(ctx context.Context) (*domain.NativeCapacity, error) {
	_, capacity, release, err := h.Reserve(ctx)
	if err == nil {
		release()
	}
	return capacity, err
}
func (h *WorkerHub) DispatchOCR(context.Context, *domain.NativeOCRRequest) error {
	return errors.New("reserve worker before dispatch")
}
func (h *WorkerHub) DispatchScan(context.Context, *domain.NativeScanRequest) error {
	return errors.New("reserve worker before dispatch")
}

type workerReservation struct {
	hub     *WorkerHub
	session *workerSession
	units   int
	until   time.Time
	attempt string
}

func (h *WorkerHub) Reserve(ctx context.Context) (Transport, *domain.NativeCapacity, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var best *workerSession
	var capacity domain.NativeCapacity
	for _, s := range h.sessions {
		n := s.node
		if !n.Connected || n.Paused || n.Revoked || time.Since(n.LastSeen) > 20*time.Second || len(n.Capabilities) == 0 || n.Capacity.State == "paused" || n.Capacity.State == "busy" || n.Capacity.State == "draining" {
			continue
		}
		slots, units := 0, 0
		for r := range s.reservations {
			if time.Now().After(r.until) {
				delete(s.reservations, r)
				continue
			}
			slots++
			units += r.units
		}
		c := n.Capacity
		c.Available = min(c.Available, c.EffectiveLimit-slots)
		c.AvailableUnits = min(c.AvailableUnits, c.AvailableUnits+c.ActiveUnits-units)
		if c.Available <= 0 || c.AvailableUnits <= 0 {
			continue
		}
		if best == nil || s.selectedAt.Before(best.selectedAt) || (s.selectedAt.Equal(best.selectedAt) && c.AvailableUnits > capacity.AvailableUnits) {
			best = s
			capacity = c
		}
	}
	if best == nil {
		return nil, nil, nil, ErrNativeBusy
	}
	best.selectedAt = time.Now()
	if best.reservations == nil {
		best.reservations = map[*workerReservation]struct{}{}
	}
	r := &workerReservation{hub: h, session: best, units: 0, until: time.Now().Add(30 * time.Second)}
	best.reservations[r] = struct{}{}
	return r, &capacity, r.release, nil
}
func (r *workerReservation) release() {
	r.hub.mu.Lock()
	for _, session := range r.hub.sessions {
		delete(session.reservations, r)
	}
	r.hub.mu.Unlock()
}
func (r *workerReservation) GetCapacity(context.Context) (*domain.NativeCapacity, error) {
	r.hub.mu.Lock()
	defer r.hub.mu.Unlock()
	c := r.session.node.Capacity
	return &c, nil
}
func (r *workerReservation) DispatchOCR(ctx context.Context, req *domain.NativeOCRRequest) error {
	return r.dispatch(ctx, req.DocumentID, req.AttemptID, "ocr", req.Input.MediaType, WorkerMessage{Type: "ocr", RequestID: req.AttemptID, OCR: req})
}
func (r *workerReservation) DispatchScan(ctx context.Context, req *domain.NativeScanRequest) error {
	return r.dispatch(ctx, req.DocumentID, req.AttemptID, "scan", req.Input.MediaType, WorkerMessage{Type: "scan", RequestID: req.AttemptID, Scan: req})
}
func (r *workerReservation) dispatch(ctx context.Context, docID, attemptID, kind, mediaType string, msg WorkerMessage) error {
	h := r.hub
	s := r.session
	h.mu.Lock()
	supported := false
	for _, c := range s.node.Capabilities {
		if c == kind {
			supported = true
		}
	}
	if !supported || h.sessions[s.node.NodeID] != s || !s.node.Connected || s.node.Paused {
		for _, session := range h.sessions {
			delete(session.reservations, r)
		}
		h.mu.Unlock()
		return ErrNativeBusy
	}
	r.units = s.node.Capacity.ImageJobUnits
	if mediaType == "application/pdf" {
		r.units = s.node.Capacity.PDFJobUnits
	}
	r.attempt = attemptID
	r.until = time.Now().Add(h.lease)
	ch := make(chan bool, 1)
	s.pending[attemptID] = ch
	h.mu.Unlock()
	// Persist the route before bytes can reach a worker. A lost ACK retains this reservation.
	if err := h.store.Assign(ctx, WorkerRoute{NodeID: s.node.NodeID, DocumentID: docID, AttemptID: attemptID, Kind: kind, Status: "dispatched", CreatedAt: time.Now()}); err != nil {
		r.release()
		h.mu.Lock()
		delete(s.pending, attemptID)
		h.mu.Unlock()
		return err
	}
	defer func() { h.mu.Lock(); delete(s.pending, attemptID); h.mu.Unlock() }()
	if err := s.write(msg); err != nil {
		return err
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case accepted := <-ch:
		if !accepted {
			r.release()
			return ErrNativeBusy
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("worker acknowledgement timed out")
	}
}
func (h *WorkerHub) Complete(attemptID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.sessions {
		for r := range s.reservations {
			if r.attempt == attemptID {
				delete(s.reservations, r)
			}
		}
	}
}

func (h *WorkerHub) SetLease(lease time.Duration) { h.mu.Lock(); h.lease = lease; h.mu.Unlock() }
func (h *WorkerHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.sessions {
		if s.conn != nil {
			_ = s.conn.Close()
		}
		s.node.Connected = false
	}
}

func (r *workerReservation) Supports(kind string) bool {
	r.hub.mu.Lock()
	defer r.hub.mu.Unlock()
	for _, capability := range r.session.node.Capabilities {
		if capability == kind {
			return true
		}
	}
	return false
}

// Enrollment is an explicit admin-authorized credential rotation. Drop the old
// authenticated socket immediately but retain its attempt reservations.
func (h *WorkerHub) Reenrolled(nodeID string) {
	h.mu.Lock()
	s := h.sessions[nodeID]
	if s != nil {
		s.node.Connected = false
		s.node.Paused = false
		s.node.Revoked = false
		s.node.State = "offline"
	}
	h.mu.Unlock()
	if s != nil && s.conn != nil {
		_ = s.conn.Close()
	}
}
