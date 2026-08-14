package ocr

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
)

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

type SubmitResponse struct {
	DocumentID string `json:"documentId"`
	Status     string `json:"status"`
}

type DocumentResponse struct {
	DocumentID  string         `json:"documentId"`
	Status      string         `json:"status"`
	Result      *ResultPayload `json:"result,omitempty"`
	ErrorDetail string         `json:"errorDetail,omitempty"`
}

type ResultPayload struct {
	Text string `json:"text"`
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) SubmitAndPoll(ctx context.Context, inputURL string) (string, error) {
	docID, err := c.SubmitDocument(ctx, inputURL)
	if err != nil {
		return "", fmt.Errorf("submit OCR: %w", err)
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	timeout := time.After(60 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout:
			return "", errors.New("OCR processing timed out after 60 seconds")
		case <-ticker.C:
			doc, err := c.GetDocument(ctx, docID)
			if err != nil {
				return "", fmt.Errorf("poll OCR status: %w", err)
			}

			switch doc.Status {
			case "completed":
				if doc.Result != nil {
					return doc.Result.Text, nil
				}
				return "", errors.New("OCR completed but result text is empty")
			case "failed":
				if doc.ErrorDetail != "" {
					return "", fmt.Errorf("OCR processing failed: %s", doc.ErrorDetail)
				}
				return "", errors.New("OCR processing failed")
			case "queued", "processing":
				continue
			default:
				return "", fmt.Errorf("unknown document status: %s", doc.Status)
			}
		}
	}
}

func (c *Client) SubmitDocument(ctx context.Context, inputURL string) (string, error) {
	reqBody, err := json.Marshal(map[string]any{
		"input": map[string]string{
			"url": inputURL,
		},
		"options": map[string]any{
			"recognitionLevel":             "accurate",
			"languages":                    []string{"vi-VN", "en-US"},
			"automaticallyDetectsLanguage": true,
			"usesLanguageCorrection":       true,
		},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/documents", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var sub SubmitResponse
	if err := json.Unmarshal(body, &sub); err != nil {
		return "", fmt.Errorf("decode submit response: %w", err)
	}

	if sub.DocumentID == "" {
		return "", errors.New("document ID missing from submit response")
	}

	return sub.DocumentID, nil
}

func (c *Client) GetDocument(ctx context.Context, documentID string) (*DocumentResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/documents/"+documentID, nil)
	if err != nil {
		return nil, err
	}

	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var doc DocumentResponse
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decode document response: %w", err)
	}

	return &doc, nil
}
