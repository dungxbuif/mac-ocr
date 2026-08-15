package ocr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL      string
	apiKey       string
	httpClient   *http.Client
	pollInterval time.Duration
	pollTimeout  time.Duration
}

type SubmitResponse struct {
	DocumentID string `json:"documentId"`
	Status     string `json:"status"`
}

type DocumentResponse struct {
	DocumentID  string          `json:"documentId"`
	Status      string          `json:"status"`
	Result      *ResultPayload  `json:"result,omitempty"`
	ErrorDetail string          `json:"errorDetail,omitempty"`
}

// BBox is a normalized bounding box [x, y, width, height] with Vision's
// lower-left origin (x=left, y=bottom, both in [0,1]).
type BBox [4]float64

// Block is a single Vision observation on a page.
type Block struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	BBox       BBox    `json:"bbox"`
}

// Page holds all observations for a single source page.
type Page struct {
	PageNumber int     `json:"pageNumber"`
	Text       string  `json:"text"`
	Blocks     []Block `json:"blocks,omitempty"`
}

// ResultPayload is the completed OCR result from the server.
type ResultPayload struct {
	Text      string  `json:"text"`
	PageCount int     `json:"pageCount"`
	Pages     []Page  `json:"pages,omitempty"`
}

// NewClient builds an OCR proxy client. httpTimeout caps a single HTTP call,
// pollInterval is the cadence between status checks, and pollTimeout is the
// total budget for waiting on one document. All three must be > 0; they come
// from env (OCR_HTTP_TIMEOUT, OCR_POLL_INTERVAL, OCR_POLL_TIMEOUT) so an
// operator can tune for prod latency without a rebuild.
func NewClient(baseURL, apiKey string, httpTimeout, pollInterval, pollTimeout time.Duration) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: httpTimeout},
		pollInterval: pollInterval,
		pollTimeout:  pollTimeout,
	}
}

// SubmitAndPoll submits a URL and returns the plain reconstructed text.
// It delegates to SubmitAndPollFull and applies 2D layout reconstruction.
func (c *Client) SubmitAndPoll(ctx context.Context, inputURL string) (string, error) {
	result, err := c.SubmitAndPollFull(ctx, inputURL)
	if err != nil {
		return "", err
	}
	return ReconstructLayout(result), nil
}

// SubmitAndPollFull submits a URL-based OCR request and returns the full
// *ResultPayload including page/block bounding-box data.
func (c *Client) SubmitAndPollFull(ctx context.Context, inputURL string) (*ResultPayload, error) {
	docID, err := c.SubmitDocument(ctx, inputURL)
	if err != nil {
		return nil, fmt.Errorf("submit OCR: %w", err)
	}
	return c.pollFull(ctx, docID)
}

// SubmitAndPollBase64 fetches nothing — the caller already has the raw bytes.
// It inlines them as base64, submits, then polls. Use this for Mezon
// attachments so the proxy's URL fetcher is bypassed (the proxy host may not
// reach cdn.komu.vn even though the bot host can). The proxy sniffs the MIME
// type from the bytes itself, so we never send a mimeType field (the proxy's
// strict decoder would 400 on any unknown field).
func (c *Client) SubmitAndPollBase64(ctx context.Context, data []byte) (string, error) {
	result, err := c.SubmitAndPollBase64Full(ctx, data)
	if err != nil {
		return "", err
	}
	return ReconstructLayout(result), nil
}

// SubmitAndPollBase64Full submits raw bytes and returns the full *ResultPayload.
func (c *Client) SubmitAndPollBase64Full(ctx context.Context, data []byte) (*ResultPayload, error) {
	docID, err := c.SubmitDocumentBase64(ctx, data)
	if err != nil {
		return nil, fmt.Errorf("submit OCR: %w", err)
	}
	return c.pollFull(ctx, docID)
}

// pollFull polls until the document is done and returns the full *ResultPayload.
func (c *Client) pollFull(ctx context.Context, docID string) (*ResultPayload, error) {
	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	timeout := time.After(c.pollTimeout)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			return nil, fmt.Errorf("OCR processing timed out after %s", c.pollTimeout)
		case <-ticker.C:
			doc, err := c.GetDocument(ctx, docID)
			if err != nil {
				return nil, fmt.Errorf("poll OCR status: %w", err)
			}

			switch doc.Status {
			case "completed":
				if doc.Result != nil {
					return doc.Result, nil
				}
				return nil, errors.New("OCR completed but result is empty")
			case "failed":
				if doc.ErrorDetail != "" {
					return nil, fmt.Errorf("OCR processing failed: %s", doc.ErrorDetail)
				}
				return nil, errors.New("OCR processing failed")
			case "queued", "processing":
				continue
			default:
				return nil, fmt.Errorf("unknown document status: %s", doc.Status)
			}
		}
	}
}

// SubmitDocument submits a URL-based OCR request.
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
	return c.submit(ctx, reqBody)
}

// SubmitDocumentBase64 submits raw bytes as a base64 data payload. The proxy
// stores them directly and queues OCR, so the request succeeds regardless of
// whether the proxy host can fetch URLs from cdn.komu.vn. The proxy sniffs the
// MIME type via DetectMIME, so we only send the "base64" field; sending any
// extra field (e.g. mimeType) triggers a 400 "unknown field" because the proxy
// uses DisallowUnknownFields.
func (c *Client) SubmitDocumentBase64(ctx context.Context, data []byte) (string, error) {
	reqBody, err := json.Marshal(map[string]any{
		"input": map[string]any{
			"base64": base64.StdEncoding.EncodeToString(data),
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
	return c.submit(ctx, reqBody)
}

// submit posts the JSON request body to /v1/documents and returns the new
// document id on success. Accepts either 200 OK or 202 Accepted.
func (c *Client) submit(ctx context.Context, reqBody []byte) (string, error) {
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
