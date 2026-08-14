package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"macocr/proxy/domain"
	"macocr/proxy/internal/native"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNativeClientAndWebhookSignature(t *testing.T) {
	secret := "test-secret-12345"
	nodeID := "mac-test"
	eventID := "evt_001"
	tsStr := fmt.Sprintf("%d", time.Now().Unix())

	body := []byte(`{"eventId":"evt_001","type":"attempt.completed"}`)
	sig := native.SignWebhook(secret, nodeID, tsStr, eventID, body)

	if err := native.VerifyWebhook(secret, nodeID, tsStr, eventID, body, sig); err != nil {
		t.Fatalf("VerifyWebhook failed: %v", err)
	}

	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := http.StatusOK
		body := `{}`
		switch r.URL.Path {
		case "/ocr":
			if got := r.Header.Get("Authorization"); got != "Bearer "+secret {
				return nil, fmt.Errorf("unexpected authorization header %q", got)
			}
			var req domain.NativeOCRRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				return nil, err
			}
			status = http.StatusAccepted
			body = `{"attemptId":"att_test_1","status":"accepted"}`
		case "/health":
		case "/capacity":
			body = `{"state":"ready","available":1}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}

	client := native.NewClientWithHTTPClient("http://native.test", secret, httpClient)
	ctx := context.Background()

	err := client.GetHealth(ctx)
	if err != nil {
		t.Fatalf("GetHealth failed: %v", err)
	}

	cap, err := client.GetCapacity(ctx)
	if err != nil || cap.Available != 1 {
		t.Fatalf("GetCapacity failed: %v", err)
	}

	err = client.DispatchOCR(ctx, &domain.NativeOCRRequest{
		DocumentID: "doc_test_1",
		AttemptID:  "att_test_1",
		Input:      domain.NativeInputRef{URL: "https://storage.test/input.png", MediaType: "image/png", SHA256: strings.Repeat("a", 64)},
	})
	if err != nil {
		t.Fatalf("DispatchOCR failed: %v", err)
	}
}

func TestNativeClientRejectsMismatchedAcceptance(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"attemptId":"wrong","status":"accepted"}`)), Request: r}, nil
	})}
	client := native.NewClientWithHTTPClient("http://native.test/", "secret", httpClient)
	err := client.DispatchOCR(context.Background(), &domain.NativeOCRRequest{DocumentID: "doc_test_1", AttemptID: "att_test_1"})
	if err == nil || !strings.Contains(err.Error(), "invalid acceptance") {
		t.Fatalf("expected invalid acceptance error, got %v", err)
	}
}
