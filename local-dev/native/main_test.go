package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"macocr/proxy/domain"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestOCRRejectsUnknownAndTrailingJSON(t *testing.T) {
	state := &serverState{operatorLimit: 1, authSecret: "secret"}
	for _, body := range []string{
		`{"documentId":"d","attemptId":"a","input":{"url":"http://input"},"callback":{"url":"http://callback"},"unexpected":true}`,
		`{"documentId":"d","attemptId":"a","input":{"url":"http://input"},"callback":{"url":"http://callback"}} {}`,
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/ocr", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Content-Type", "application/json")
		state.handleOCR(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %q, got %d", body, w.Code)
		}
		var response map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("invalid error JSON: %v", err)
		}
	}
}

func TestFetchAndRecognizeDownloadsInput(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00}
	state := &serverState{client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.String() != "http://storage.local/input.png" {
			t.Fatalf("unexpected input request: %s %s", req.Method, req.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(png)),
			Request:    req,
		}, nil
	})}}

	result, err := state.fetchAndRecognize(domain.NativeOCRRequest{
		DocumentID: "doc_test",
		Input: domain.NativeInputRef{
			URL:       "http://storage.local/input.png",
			MediaType: "image/png",
		},
	})
	if err != nil {
		t.Fatalf("fetchAndRecognize: %v", err)
	}
	if result.PageCount != 1 || !strings.Contains(result.Text, "Input bytes: 9") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCapacityReflectsRuntimeState(t *testing.T) {
	state := &serverState{operatorLimit: 2, active: 1, configVersion: 3, delay: time.Millisecond}
	capacity := state.capacityLocked()
	if capacity.State != "ready" || capacity.Available != 1 || capacity.ConfigVersion != 3 {
		t.Fatalf("unexpected capacity: %+v", capacity)
	}
}
