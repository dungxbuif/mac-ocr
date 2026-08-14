// Command native simulates the private OCR worker for local development.
// It downloads the submitted S3 object, validates it, and sends the same
// signed asynchronous callback expected from a production worker.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"macocr/proxy/domain"
)

const maxInputBytes = 100 << 20

type serverState struct {
	mu            sync.Mutex
	operatorLimit int
	active        int
	configVersion uint64
	nodeID        string
	bootID        string
	sequence      uint64
	authSecret    string
	delay         time.Duration
	client        *http.Client
	logger        *slog.Logger
	jobs          sync.WaitGroup
}

func main() {
	port := flag.Int("port", envInt("NATIVE_PORT", 8787), "HTTP port")
	limit := flag.Int("limit", envInt("NATIVE_LIMIT", 1), "concurrent OCR limit")
	delay := flag.Duration("delay", envDuration("NATIVE_DELAY", 500*time.Millisecond), "simulated processing delay")
	secret := flag.String("secret", envOr("NATIVE_AUTH_SECRET", "change-me-in-production"), "shared bearer and callback HMAC secret")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	state := &serverState{
		operatorLimit: max(0, *limit), configVersion: 1,
		nodeID: "ocr-local-dev", bootID: fmt.Sprintf("boot_%x", time.Now().UnixNano()),
		authSecret: *secret, delay: *delay,
		client: &http.Client{Timeout: 20 * time.Second}, logger: logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /ocr", state.handleOCR)
	mux.HandleFunc("GET /health", state.handleHealth)
	mux.HandleFunc("GET /capacity", state.handleCapacity)
	mux.HandleFunc("PUT /runtime/config", state.handleConfig)

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		logger.Info("local native service starting", "addr", addr, "limit", *limit, "delay", delay.String())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("local native service failed", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	jobsDone := make(chan struct{})
	go func() {
		state.jobs.Wait()
		close(jobsDone)
	}()
	select {
	case <-jobsDone:
	case <-shutdownCtx.Done():
		logger.Warn("local OCR jobs did not finish before shutdown deadline")
	}
}

func (s *serverState) handleOCR(w http.ResponseWriter, r *http.Request) {
	if s.authSecret != "" && !hmac.Equal([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.authSecret)) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid native bearer token"})
		return
	}
	if !strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]), "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "content type must be application/json"})
		return
	}
	var req domain.NativeOCRRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request json: " + err.Error()})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must contain exactly one JSON object"})
		return
	}
	if req.DocumentID == "" || req.AttemptID == "" || req.Input.URL == "" || req.Callback.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "documentId, attemptId, input.url and callback.url are required"})
		return
	}

	s.mu.Lock()
	if s.operatorLimit == 0 || s.active >= s.operatorLimit {
		capacity := s.capacityLocked()
		s.mu.Unlock()
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "capacity unavailable", "capacity": capacity})
		return
	}
	s.active++
	s.jobs.Add(1)
	s.mu.Unlock()

	writeJSON(w, http.StatusAccepted, map[string]string{"attemptId": req.AttemptID, "status": "accepted"})
	go s.process(req)
}

func (s *serverState) process(req domain.NativeOCRRequest) {
	defer s.jobs.Done()
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	result, processErr := s.fetchAndRecognize(req)

	s.mu.Lock()
	s.active--
	sequence := atomic.AddUint64(&s.sequence, 1)
	capacity := s.capacityLocked()
	s.mu.Unlock()

	event := domain.NativeEvent{
		EventID: fmt.Sprintf("evt_local_%x", time.Now().UnixNano()), NodeID: s.nodeID,
		BootID: s.bootID, Sequence: sequence, AttemptID: req.AttemptID,
		DocumentID: req.DocumentID, Capacity: capacity, OccurredAt: time.Now().UTC(),
	}
	if processErr != nil {
		event.Type = "attempt.failed"
		event.Error = processErr.Error()
	} else {
		event.Type = "attempt.completed"
		event.Result = result
	}
	if err := s.deliverCallback(req.Callback.URL, event); err != nil {
		s.logger.Error("callback delivery failed", "documentId", req.DocumentID, "error", err)
	}
}

func (s *serverState) fetchAndRecognize(req domain.NativeOCRRequest) (*domain.OCRResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fetchReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.Input.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build input request: %w", err)
	}
	resp, err := s.client.Do(fetchReq)
	if err != nil {
		return nil, fmt.Errorf("download input: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download input returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read input: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("downloaded input is empty")
	}
	if len(data) > maxInputBytes {
		return nil, fmt.Errorf("input exceeds %d bytes", maxInputBytes)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if req.Input.SHA256 != "" && !strings.EqualFold(req.Input.SHA256, hash) {
		return nil, fmt.Errorf("input SHA-256 mismatch")
	}

	languages := []string{"vi-VN", "en-US"}
	if req.Options != nil && len(req.Options.Languages) > 0 {
		languages = req.Options.Languages
	}
	text := fmt.Sprintf("LOCAL OCR DEVELOPMENT RESULT\nDocument: %s\nMedia type: %s\nInput bytes: %d\nSHA-256: %s\nLanguages: %s",
		req.DocumentID, req.Input.MediaType, len(data), hash, strings.Join(languages, ", "))
	block := domain.OCRBlock{Text: text, Confidence: 1, BBox: []float64{0, 0, 1, 1}}
	return &domain.OCRResult{Text: text, PageCount: 1, Pages: []domain.OCRPageResult{{PageNumber: 1, Text: text, Blocks: []domain.OCRBlock{block}}}}, nil
}

func (s *serverState) deliverCallback(callbackURL string, event domain.NativeEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(event.OccurredAt.Unix(), 10)
	signature := sign(s.authSecret, s.nodeID, timestamp, event.EventID, body)
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
		if err != nil {
			cancel()
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Native-Node-Id", s.nodeID)
		req.Header.Set("X-Native-Timestamp", timestamp)
		req.Header.Set("X-Native-Event-Id", event.EventID)
		req.Header.Set("X-Native-Signature", signature)
		resp, err := s.client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				cancel()
				return nil
			}
			err = fmt.Errorf("callback returned HTTP %d", resp.StatusCode)
		}
		cancel()
		lastErr = err
		if attempt < 4 {
			time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
		}
	}
	return lastErr
}

func (s *serverState) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "local-native"})
}

func (s *serverState) handleCapacity(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	capacity := s.capacityLocked()
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, capacity)
}

func (s *serverState) handleConfig(w http.ResponseWriter, r *http.Request) {
	if s.authSecret != "" && !hmac.Equal([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.authSecret)) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid native bearer token"})
		return
	}
	if !strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]), "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "content type must be application/json"})
		return
	}
	var cfg struct {
		OperatorLimit *int `json:"operatorLimit"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil || cfg.OperatorLimit == nil || *cfg.OperatorLimit < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "operatorLimit must be a non-negative integer"})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must contain exactly one JSON object"})
		return
	}
	s.mu.Lock()
	s.operatorLimit = *cfg.OperatorLimit
	s.configVersion++
	capacity := s.capacityLocked()
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, capacity)
}

func (s *serverState) capacityLocked() domain.NativeCapacity {
	available := max(0, s.operatorLimit-s.active)
	state := "ready"
	if s.operatorLimit == 0 {
		state = "paused"
	} else if available == 0 {
		state = "busy"
	}
	return domain.NativeCapacity{ConfigVersion: s.configVersion, State: state, OperatorLimit: s.operatorLimit, EffectiveLimit: s.operatorLimit, Active: s.active, Available: available}
}

func sign(secret, nodeID, timestamp, eventID string, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(h, "%s.%s.%s.", nodeID, timestamp, eventID)
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if value, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return value
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if value, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return value
	}
	return fallback
}
