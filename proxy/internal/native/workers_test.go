package native

import (
	"context"
	"github.com/gorilla/websocket"
	"macocr/proxy/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReservationsPreventOversubscription(t *testing.T) {
	h := NewWorkerHub(nil)
	h.sessions["a"] = &workerSession{node: WorkerNode{NodeID: "a", Connected: true, LastSeen: time.Now(), Capabilities: []string{"ocr", "scan"}, Capacity: domain.NativeCapacity{State: "ready", EffectiveLimit: 1, Available: 1, AvailableUnits: 2, ImageJobUnits: 1, PDFJobUnits: 2}}, pending: map[string]chan bool{}}
	_, cap, release, err := h.Reserve(context.Background())
	if err != nil || cap.Available != 1 {
		t.Fatalf("first reserve: %v %+v", err, cap)
	}
	if _, _, _, err = h.Reserve(context.Background()); err == nil {
		t.Fatal("second reservation exceeded slot limit")
	}
	release()
	if _, _, _, err = h.Reserve(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestExpiredAndPausedWorkersCannotReserve(t *testing.T) {
	h := NewWorkerHub(nil)
	h.sessions["a"] = &workerSession{node: WorkerNode{NodeID: "a", Connected: true, LastSeen: time.Now().Add(-time.Minute), Capacity: domain.NativeCapacity{EffectiveLimit: 1, Available: 1, AvailableUnits: 1}}}
	if _, _, _, err := h.Reserve(context.Background()); err == nil {
		t.Fatal("expired node accepted")
	}
	h.sessions["a"].node.LastSeen = time.Now()
	h.sessions["a"].node.Paused = true
	if _, _, _, err := h.Reserve(context.Background()); err == nil {
		t.Fatal("paused node accepted")
	}
}

type testWorkerStore struct {
	mu       sync.Mutex
	assigned map[string]string
	revoked  bool
}

func (s *testWorkerStore) CreateEnrollment(context.Context, string, time.Time) error    { return nil }
func (s *testWorkerStore) Enroll(context.Context, string, string, string, string) error { return nil }
func (s *testWorkerStore) Secret(context.Context, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revoked {
		return "", domain.ErrUnauthorized
	}
	return "secret", nil
}
func (s *testWorkerStore) ListWorkers(context.Context) ([]WorkerNode, error) {
	return []WorkerNode{{NodeID: "node_123"}}, nil
}
func (s *testWorkerStore) Revoke(context.Context, string) error {
	s.mu.Lock()
	s.revoked = true
	s.mu.Unlock()
	return nil
}
func (s *testWorkerStore) Assign(_ context.Context, r WorkerRoute) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.assigned[r.AttemptID] = r.NodeID
	return nil
}
func (s *testWorkerStore) AssignedNode(_ context.Context, docID, attemptID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.assigned[attemptID], nil
}
func (s *testWorkerStore) Routes(context.Context) ([]WorkerRoute, error) { return []WorkerRoute{}, nil }
func TestSocketDispatchAndRevoke(t *testing.T) {
	store := &testWorkerStore{assigned: map[string]string{}}
	h := NewWorkerHub(store)
	defer h.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.Authenticate(r.Context(), r.Header.Get("X-Native-Node-Id"), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
			w.WriteHeader(401)
			return
		}
		h.ServeSocket(w, r, "node_123", "secret")
	}))
	defer srv.Close()
	headers := http.Header{"Authorization": []string{"Bearer wrong"}, "X-Native-Node-Id": []string{"node_123"}}
	if _, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), headers); err == nil || resp.StatusCode != 401 {
		t.Fatal("bad credential accepted")
	}
	headers.Set("Authorization", "Bearer secret")
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), headers)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cap := domain.NativeCapacity{State: "ready", EffectiveLimit: 2, Available: 2, AvailableUnits: 4, ImageJobUnits: 1, PDFJobUnits: 2}
	if err := conn.WriteJSON(WorkerMessage{Type: "hello", Name: "Mac", Capabilities: []string{"ocr", "scan"}, Capacity: &cap}); err != nil {
		t.Fatal(err)
	}
	var msg WorkerMessage
	if err := conn.ReadJSON(&msg); err != nil || msg.Type != "welcome" {
		t.Fatalf("welcome %v %+v", err, msg)
	}
	worker, _, _, err := h.Reserve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- worker.DispatchOCR(context.Background(), &domain.NativeOCRRequest{DocumentID: "doc", AttemptID: "att", Input: domain.NativeInputRef{MediaType: "image/png"}})
	}()
	if err := conn.ReadJSON(&msg); err != nil || msg.Type != "ocr" {
		t.Fatalf("dispatch %v %+v", err, msg)
	}
	node, _ := store.AssignedNode(context.Background(), "doc", "att")
	if node != "node_123" {
		t.Fatal("route not persisted before dispatch")
	}
	cap.Active = 1
	cap.Available = 1
	cap.ActiveUnits = 1
	cap.AvailableUnits = 3
	if err := conn.WriteJSON(WorkerMessage{Type: "ack", RequestID: "att", Accepted: true, Capacity: &cap}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_, capacity, release, err := h.Reserve(context.Background())
	if err != nil || capacity.AvailableUnits != 3 {
		t.Fatalf("reported active double counted: %+v %v", capacity, err)
	}
	release()
	if err := h.Control(context.Background(), "node_123", "pause"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := h.Reserve(context.Background()); err == nil {
		t.Fatal("paused node selected")
	}
	if err := h.Control(context.Background(), "node_123", "revoke"); err != nil {
		t.Fatal(err)
	}
	if h.Authenticate(context.Background(), "node_123", "secret") {
		t.Fatal("revoked credential accepted")
	}
}
func TestUnknownACKRetainsReservationUntilLease(t *testing.T) {
	store := &testWorkerStore{assigned: map[string]string{}}
	h := NewWorkerHub(store)
	h.SetLease(time.Minute)
	s := &workerSession{node: WorkerNode{NodeID: "a", Connected: true, LastSeen: time.Now(), Capabilities: []string{"ocr"}, Capacity: domain.NativeCapacity{State: "ready", EffectiveLimit: 1, Available: 1, AvailableUnits: 1, ImageJobUnits: 1}}, pending: map[string]chan bool{}}
	h.sessions["a"] = s
	transport, _, _, err := h.Reserve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.DispatchOCR(context.Background(), &domain.NativeOCRRequest{DocumentID: "doc", AttemptID: "att"}); err == nil {
		t.Fatal("missing socket should fail")
	}
	if _, _, _, err := h.Reserve(context.Background()); err == nil {
		t.Fatal("unknown outcome reservation released")
	}
	h.Complete("att")
	if _, _, _, err := h.Reserve(context.Background()); err != nil {
		t.Fatal("completion failed to recover capacity")
	}
}

func (s *testWorkerStore) RegisterShared(context.Context, string, string) error { return nil }
func TestSharedAuthenticationRequiresBearerAndRejectsRevokedNode(t *testing.T) {
	store := &testWorkerStore{assigned: map[string]string{}}
	h := NewWorkerHub(store)
	h.SetSharedSecret("shared-secret-key-with-at-least-32-characters")
	if h.Authenticate(context.Background(), "node_123", "wrong") {
		t.Fatal("bad shared bearer accepted")
	}
	if !h.Authenticate(context.Background(), "node_123", "shared-secret-key-with-at-least-32-characters") {
		t.Fatal("shared credential rejected")
	}
	if h.Authenticate(context.Background(), "/invalid/id", "shared-secret-key-with-at-least-32-characters") {
		t.Fatal("invalid node ID accepted")
	}
	store.Revoke(context.Background(), "node_123")
	if h.Authenticate(context.Background(), "node_123", "shared-secret-key-with-at-least-32-characters") {
		t.Fatal("revoked node accepted")
	}
}
func TestWorkerSelectionDoesNotStarveOtherCapabilities(t *testing.T) {
	h := NewWorkerHub(nil)
	for _, id := range []string{"ocr", "scan"} {
		h.sessions[id] = &workerSession{node: WorkerNode{NodeID: id, Connected: true, LastSeen: time.Now(), Capabilities: []string{id}, Capacity: domain.NativeCapacity{State: "ready", EffectiveLimit: 2, Available: 2, AvailableUnits: 8, ImageJobUnits: 1, PDFJobUnits: 4}}}
	}
	first, _, release, err := h.Reserve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	firstID := first.(*workerReservation).session.node.NodeID
	release()
	second, _, release, err := h.Reserve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if second.(*workerReservation).session.node.NodeID == firstID {
		t.Fatal("scheduler repeatedly selected same capability")
	}
}

func TestReconnectDisplacesOldSessionAndPreservesPauseAndReservations(t *testing.T) {
	store := &testWorkerStore{assigned: map[string]string{}}
	h := NewWorkerHub(store)
	defer h.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeSocket(w, r, "node_123", "secret") }))
	defer srv.Close()
	connect := func() *websocket.Conn {
		c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		capacity := domain.NativeCapacity{State: "ready", EffectiveLimit: 1, Available: 1, AvailableUnits: 1, ImageJobUnits: 1, PDFJobUnits: 1}
		if err := c.WriteJSON(WorkerMessage{Type: "hello", Capabilities: []string{"ocr"}, Capacity: &capacity}); err != nil {
			t.Fatal(err)
		}
		var m WorkerMessage
		if err := c.ReadJSON(&m); err != nil || m.Type != "welcome" {
			t.Fatalf("welcome %v %+v", err, m)
		}
		return c
	}
	old := connect()
	defer old.Close()
	transport, _, _, err := h.Reserve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	reservation := transport.(*workerReservation)
	h.mu.Lock()
	reservation.attempt = "retained"
	h.mu.Unlock()
	if err := h.Control(context.Background(), "node_123", "pause"); err != nil {
		t.Fatal(err)
	}
	var m WorkerMessage
	if err := old.ReadJSON(&m); err != nil || m.Action != "pause" {
		t.Fatal("old pause control missing")
	}
	replacement := connect()
	defer replacement.Close()
	if err := replacement.ReadJSON(&m); err != nil || m.Type != "control" || m.Action != "pause" {
		t.Fatalf("pause not preserved: %v %+v", err, m)
	}
	if _, _, _, err := h.Reserve(context.Background()); err == nil {
		t.Fatal("reconnected paused worker accepted")
	}
	if err := h.Control(context.Background(), "node_123", "resume"); err != nil {
		t.Fatal(err)
	}
	if err := replacement.ReadJSON(&m); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := h.Reserve(context.Background()); err == nil {
		t.Fatal("reconnect lost reservation")
	}
	h.Complete("retained")
	if _, _, release, err := h.Reserve(context.Background()); err != nil {
		t.Fatal("completion did not free migrated reservation")
	} else {
		release()
	}
	_ = old.SetReadDeadline(time.Now().Add(time.Second))
	if err := old.ReadJSON(&m); err == nil {
		t.Fatal("stale session remained connected")
	}
}
