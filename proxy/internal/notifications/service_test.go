package notifications

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"macocr/proxy/domain"
)

type deliveryRepo struct {
	event          *domain.NotificationEvent
	delivered      bool
	failed         bool
	failureDetails string
}

func (r *deliveryRepo) Create(context.Context, *domain.NotificationEvent) error { return nil }
func (r *deliveryRepo) ClaimWebhook(context.Context) (*domain.NotificationEvent, error) {
	return r.event, nil
}
func (r *deliveryRepo) MarkDelivered(context.Context, string) error {
	r.delivered = true
	return nil
}
func (r *deliveryRepo) MarkFailed(_ context.Context, _ string, detail string, _ time.Time) error {
	r.failed = true
	r.failureDetails = detail
	return nil
}
func (r *deliveryRepo) ListSSE(context.Context, int64, string, int) ([]domain.NotificationEvent, error) {
	return nil, nil
}
func (r *deliveryRepo) DeleteBefore(context.Context, time.Time, int) (int64, error) { return 0, nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWebhookDeliverySignature(t *testing.T) {
	service := &Service{client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		mac := hmac.New(sha256.New, []byte("my-hmac-secret-123"))
		_, _ = io.WriteString(mac, req.Header.Get("X-OCR-Timestamp")+"."+req.Header.Get("X-OCR-Event-Id")+".")
		_, _ = mac.Write(body)
		if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(req.Header.Get("X-OCR-Signature"))) {
			t.Fatal("invalid webhook signature")
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}}
	event := &domain.NotificationEvent{ID: "evt_1", WebhookURL: "https://example.com/hook", WebhookSecret: "my-hmac-secret-123", Payload: []byte(`{"status":"completed"}`)}
	if err := service.deliver(context.Background(), event); err != nil {
		t.Fatal(err)
	}
}

func TestBlockedWebhookTargetIsFailedInsteadOfMarkedDelivered(t *testing.T) {
	repo := &deliveryRepo{event: &domain.NotificationEvent{
		ID: "evt_blocked", WebhookURL: "https://127.0.0.1/hook", AttemptCount: 1,
	}}
	service := &Service{repo: repo, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	service.deliverNext(context.Background())

	if repo.delivered || !repo.failed || repo.failureDetails == "" {
		t.Fatalf("blocked target state is wrong: delivered=%v failed=%v detail=%q", repo.delivered, repo.failed, repo.failureDetails)
	}
}
