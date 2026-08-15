package ocr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Client struct {
	baseURL      string
	apiKey       string
	httpClient   *http.Client
	pollInterval time.Duration
	pollTimeout  time.Duration

	// SSE Real-time event streaming
	sseMu      sync.RWMutex
	listeners  map[string][]chan sseDocumentEvent
	sseRunning bool
	sseCancel  context.CancelFunc
}

type sseDocumentEvent struct {
	EventID    string
	EventType  string
	DocumentID string
	Status     string
	ErrorDetail string
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

// NewClient builds an OCR proxy client with SSE background listener and polling fallback.
func NewClient(baseURL, apiKey string, httpTimeout, pollInterval, pollTimeout time.Duration) *Client {
	c := &Client{
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		httpClient:   &http.Client{Timeout: httpTimeout},
		pollInterval: pollInterval,
		pollTimeout:  pollTimeout,
		listeners:    make(map[string][]chan sseDocumentEvent),
	}
	c.startSSEListener()
	return c
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

type PresignResponse struct {
	UploadURL string            `json:"uploadUrl"`
	SourceURL string            `json:"sourceUrl"`
	Headers   map[string]string `json:"headers"`
}

// SubmitAndPollPresignedFull requests an authenticated presigned PUT upload URL
// from the OCR proxy (/v1/uploads/presign), uploads the raw bytes directly to
// object storage with exact Content-Length, and then submits the owned sourceUrl.
// This is optimal for large files (>5MB) where base64 inlining exceeds limits or consumes excess RAM.
func (c *Client) SubmitAndPollPresignedFull(ctx context.Context, filename, contentType string, data []byte) (*ResultPayload, error) {
	if filename == "" {
		filename = "upload.pdf"
	}
	if contentType == "" {
		contentType = "application/pdf"
	}

	// 1. Request presigned upload URL
	presignReqBody, err := json.Marshal(map[string]any{
		"filename":    filename,
		"sizeBytes":   len(data),
		"contentType": contentType,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/uploads/presign", bytes.NewReader(presignReqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request presign: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("presign failed status %d: %s", resp.StatusCode, string(body))
	}

	var presign PresignResponse
	if err := json.Unmarshal(body, &presign); err != nil {
		return nil, fmt.Errorf("decode presign response: %w", err)
	}
	if presign.UploadURL == "" || presign.SourceURL == "" {
		return nil, fmt.Errorf("invalid presign response: missing uploadUrl or sourceUrl")
	}
	log.Printf("📦 [OCR-PRESIGN] Got uploadUrl, streaming %d bytes to S3...", len(data))

	// 2. Direct PUT to uploadUrl
	putStart := time.Now()
	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, presign.UploadURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create PUT request: %w", err)
	}
	for k, v := range presign.Headers {
		putReq.Header.Set(k, v)
	}

	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		return nil, fmt.Errorf("upload to presigned URL: %w", err)
	}
	defer putResp.Body.Close()
	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		putBody, _ := io.ReadAll(putResp.Body)
		return nil, fmt.Errorf("upload PUT failed with HTTP %d: %s", putResp.StatusCode, string(putBody))
	}
	log.Printf("✅ [OCR-PRESIGN] S3 upload completed in %v, submitting sourceUrl to OCR...", time.Since(putStart))

	// 3. Submit OCR with the returned app-owned sourceUrl
	return c.SubmitAndPollFull(ctx, presign.SourceURL)
}

// subscribe registers a channel for SSE document events.
func (c *Client) subscribe(docID string) chan sseDocumentEvent {
	c.sseMu.Lock()
	defer c.sseMu.Unlock()
	ch := make(chan sseDocumentEvent, 2)
	c.listeners[docID] = append(c.listeners[docID], ch)
	return ch
}

// unsubscribe removes a listener channel for a document.
func (c *Client) unsubscribe(docID string, ch chan sseDocumentEvent) {
	c.sseMu.Lock()
	defer c.sseMu.Unlock()
	list := c.listeners[docID]
	for i, listener := range list {
		if listener == ch {
			c.listeners[docID] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(c.listeners[docID]) == 0 {
		delete(c.listeners, docID)
	}
}

// broadcastEvent delivers an SSE event to any awaiting listeners for this document.
func (c *Client) broadcastEvent(evt sseDocumentEvent) {
	c.sseMu.RLock()
	defer c.sseMu.RUnlock()
	if listeners, ok := c.listeners[evt.DocumentID]; ok {
		for _, ch := range listeners {
			select {
			case ch <- evt:
			default:
			}
		}
	}
}

// startSSEListener maintains a persistent SSE stream to /v1/events in the background.
func (c *Client) startSSEListener() {
	if c.apiKey == "" {
		return
	}
	c.sseMu.Lock()
	if c.sseRunning {
		c.sseMu.Unlock()
		return
	}
	c.sseRunning = true
	ctx, cancel := context.WithCancel(context.Background())
	c.sseCancel = cancel
	c.sseMu.Unlock()

	go func() {
		defer func() {
			c.sseMu.Lock()
			c.sseRunning = false
			c.sseMu.Unlock()
		}()

		cursor := ""
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			sseURL := c.baseURL + "/v1/events"
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, sseURL, nil)
			if err != nil {
				time.Sleep(3 * time.Second)
				continue
			}
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
			req.Header.Set("Accept", "text/event-stream")
			if cursor != "" {
				req.Header.Set("Last-Event-ID", cursor)
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				time.Sleep(3 * time.Second)
				continue
			}

			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				time.Sleep(5 * time.Second)
				continue
			}

			log.Printf("📡 [OCR-SSE] Connected real-time event stream to %s", sseURL)
			reader := bufio.NewReader(resp.Body)
			var currentID, currentEvent, currentData string

			for {
				lineBytes, err := reader.ReadBytes('\n')
				if err != nil {
					resp.Body.Close()
					break
				}
				line := strings.TrimRight(string(lineBytes), "\r\n")

				if line == "" {
					// Dispatch event
					if currentData != "" {
						var parsed struct {
							DocumentID  string `json:"documentId"`
							Status      string `json:"status"`
							ErrorDetail string `json:"errorDetail"`
						}
						if err := json.Unmarshal([]byte(currentData), &parsed); err == nil && parsed.DocumentID != "" {
							log.Printf("⚡ [OCR-SSE] Push event: doc=%s status=%s", parsed.DocumentID, parsed.Status)
							c.broadcastEvent(sseDocumentEvent{
								EventID:     currentID,
								EventType:   currentEvent,
								DocumentID:  parsed.DocumentID,
								Status:      parsed.Status,
								ErrorDetail: parsed.ErrorDetail,
							})
						}
					}
					currentID = ""
					currentEvent = ""
					currentData = ""
					continue
				}

				if strings.HasPrefix(line, "id:") {
					currentID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
					cursor = currentID
				} else if strings.HasPrefix(line, "event:") {
					currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				} else if strings.HasPrefix(line, "data:") {
					dataVal := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
					if currentData == "" {
						currentData = dataVal
					} else {
						currentData += "\n" + dataVal
					}
				}
			}
			time.Sleep(2 * time.Second)
		}
	}()
}

// pollFull waits for document completion using Real-Time SSE push events with polling fallback.
func (c *Client) pollFull(ctx context.Context, docID string) (*ResultPayload, error) {
	sseChan := c.subscribe(docID)
	defer c.unsubscribe(docID, sseChan)

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	timeout := time.After(c.pollTimeout)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			return nil, fmt.Errorf("OCR processing timed out after %s", c.pollTimeout)
		case evt := <-sseChan:
			// Instant real-time push event from SSE!
			if evt.Status == "completed" {
				doc, err := c.GetDocument(ctx, docID)
				if err == nil && doc.Result != nil {
					return doc.Result, nil
				}
			} else if evt.Status == "failed" {
				if evt.ErrorDetail != "" {
					return nil, fmt.Errorf("OCR processing failed: %s", evt.ErrorDetail)
				}
				return nil, errors.New("OCR processing failed")
			}
		case <-ticker.C:
			// Fallback polling
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

// SubmitDocument submits a URL-based OCR request with SSE notification.
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
		"notification": map[string]string{
			"type": "sse",
		},
	})
	if err != nil {
		return "", err
	}
	return c.submit(ctx, reqBody)
}

// SubmitDocumentBase64 submits raw bytes as a base64 data payload with SSE notification.
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
		"notification": map[string]string{
			"type": "sse",
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
