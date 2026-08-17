package notifications

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"macocr/proxy/domain"
	documentuc "macocr/proxy/internal/usecase/document"
)

type Service struct {
	repo       domain.NotificationRepository
	apiBaseURL string
	client     *http.Client
	logger     *slog.Logger
}

func NewService(repo domain.NotificationRepository, apiBaseURL string, logger *slog.Logger) *Service {
	return &Service{repo: repo, apiBaseURL: strings.TrimRight(apiBaseURL, "/"), client: safeClient(), logger: logger}
}

func (s *Service) PublishDocument(ctx context.Context, doc *domain.Document) error {
	event, err := s.buildEvent(doc, "sse")
	if err != nil {
		return err
	}
	return s.repo.Create(ctx, event)
}

func (s *Service) BuildDocumentEvent(doc *domain.Document) (*domain.NotificationEvent, error) {
	if doc.Notification == nil || doc.Notification.Type == "" {
		return nil, nil
	}
	return s.buildEvent(doc, doc.Notification.Type)
}

func (s *Service) buildEvent(doc *domain.Document, channel string) (*domain.NotificationEvent, error) {
	eventID := newEventID()
	resourceType, resourceID := "document", doc.ID
	resourcePath := "/v1/documents/" + doc.ID
	if doc.Options != nil && doc.Options.ProcessingMode == "scan" {
		resourceType = "scan"
		resourcePath = "/v1/scans/" + doc.ID
	}
	payload, err := json.Marshal(map[string]any{
		"eventId":           eventID,
		"type":              resourceType + "." + string(doc.Status),
		resourceType + "Id": resourceID,
		"status":            doc.Status,
		"resource":          s.apiBaseURL + resourcePath,
		"occurredAt":        time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	return &domain.NotificationEvent{
		ID: eventID, UserID: doc.UserID, DocumentID: doc.ID, Type: resourceType + "." + string(doc.Status),
		Channel: channel, WebhookURL: notificationURL(doc, channel),
		WebhookSecret: notificationSecret(doc, channel), Payload: payload,
	}, nil
}

func notificationURL(doc *domain.Document, channel string) string {
	if channel == "webhook" && doc.Notification != nil {
		return doc.Notification.URL
	}
	return ""
}

func notificationSecret(doc *domain.Document, channel string) string {
	if channel == "webhook" && doc.Notification != nil {
		return doc.Notification.Secret
	}
	return ""
}

func (s *Service) ListSSE(ctx context.Context, userID int64, afterID string) ([]domain.NotificationEvent, error) {
	return s.repo.ListSSE(ctx, userID, afterID, 100)
}

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.deliverNext(ctx)
		}
	}
}

func (s *Service) deliverNext(ctx context.Context) {
	event, err := s.repo.ClaimWebhook(ctx)
	if err != nil {
		s.logger.Error("claim client webhook failed", "error", err)
		return
	}
	if event == nil {
		return
	}
	err = documentuc.ValidateURL(event.WebhookURL)
	if err == nil {
		err = s.deliver(ctx, event)
	}
	if err == nil {
		if markErr := s.repo.MarkDelivered(ctx, event.ID); markErr != nil {
			s.logger.Error("mark client webhook delivered failed", "eventID", event.ID, "error", markErr)
		}
		return
	}
	delay := time.Second << min(event.AttemptCount, 8)
	if markErr := s.repo.MarkFailed(ctx, event.ID, err.Error(), time.Now().Add(delay)); markErr != nil {
		s.logger.Error("schedule client webhook retry failed", "eventID", event.ID, "error", markErr)
	}
}

func (s *Service) deliver(ctx context.Context, event *domain.NotificationEvent) error {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	mac := hmac.New(sha256.New, []byte(event.WebhookSecret))
	_, _ = io.WriteString(mac, ts+"."+event.ID+".")
	_, _ = mac.Write(event.Payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, event.WebhookURL, strings.NewReader(string(event.Payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OCR-Platform-Webhook/1.0")
	req.Header.Set("X-OCR-Event-Id", event.ID)
	req.Header.Set("X-OCR-Timestamp", ts)
	req.Header.Set("X-OCR-Signature", hex.EncodeToString(mac.Sum(nil)))
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("deliver webhook: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func safeClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{TLSHandshakeTimeout: 5 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !documentuc.IsBlockedIP(ip.IP) {
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			}
		}
		return nil, fmt.Errorf("webhook target resolves only to blocked addresses")
	}}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func newEventID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("evt_%020d_%x", time.Now().UnixNano(), b)
}
