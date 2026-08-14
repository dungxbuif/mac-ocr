package native

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"macocr/proxy/domain"
)

var ErrNativeBusy = errors.New("native OCR worker busy, capacity unavailable")

type Capabilities struct {
	CapabilityVersion  string              `json:"capabilityVersion"`
	DefaultRevision    int                 `json:"defaultRevision"`
	SupportedRevisions []int               `json:"supportedRevisions"`
	Languages          map[string][]string `json:"languages"`
}

type Client struct {
	baseURL    string
	authSecret string
	http       *http.Client
}

func NewClient(baseURL, authSecret string) *Client {
	return NewClientWithHTTPClient(baseURL, authSecret, &http.Client{Timeout: 10 * time.Second})
}

func NewClientWithHTTPClient(baseURL, authSecret string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		authSecret: authSecret,
		http:       httpClient,
	}
}

func (c *Client) DispatchOCR(ctx context.Context, req *domain.NativeOCRRequest) error {
	if c.baseURL == "" {
		return fmt.Errorf("native base URL not configured")
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal native request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/ocr", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build dispatch request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.authSecret != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.authSecret)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("dispatch to native: %w", err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
	if readErr != nil {
		return fmt.Errorf("read native response: %w", readErr)
	}
	if len(body) > 64*1024 {
		return fmt.Errorf("native response exceeds 64 KiB")
	}

	if resp.StatusCode == http.StatusServiceUnavailable {
		return ErrNativeBusy
	}

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("native returned unexpected status: %d", resp.StatusCode)
	}
	var accepted struct {
		AttemptID string `json:"attemptId"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(body, &accepted); err != nil || accepted.AttemptID != req.AttemptID || accepted.Status != "accepted" {
		return fmt.Errorf("native returned an invalid acceptance response")
	}

	return nil
}

func (c *Client) GetHealth(ctx context.Context) error {
	if c.baseURL == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("native unhealthy: status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) GetCapacity(ctx context.Context) (*domain.NativeCapacity, error) {
	if c.baseURL == "" {
		return &domain.NativeCapacity{State: "ready", OperatorLimit: 1, EffectiveLimit: 1, Available: 1}, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/capacity", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("native capacity returned status %d", resp.StatusCode)
	}

	var cap domain.NativeCapacity
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&cap); err != nil {
		return nil, err
	}
	return &cap, nil
}
