package domain

import "time"

type NativeCapacity struct {
	ConfigVersion  uint64 `json:"configVersion"`
	State          string `json:"state"`
	OperatorLimit  int    `json:"operatorLimit"`
	EffectiveLimit int    `json:"effectiveLimit"`
	Active         int    `json:"active"`
	Available      int    `json:"available"`
	Reason         string `json:"reason,omitempty"`
}

type NativeEvent struct {
	EventID    string         `json:"eventId"`
	Type       string         `json:"type"`
	NodeID     string         `json:"nodeId"`
	BootID     string         `json:"bootId"`
	Sequence   uint64         `json:"sequence"`
	AttemptID  string         `json:"attemptId"`
	DocumentID string         `json:"documentId"`
	Result     *OCRResult     `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
	Capacity   NativeCapacity `json:"capacity"`
	OccurredAt time.Time      `json:"occurredAt"`
}

type NativeOCRRequest struct {
	DocumentID string            `json:"documentId"`
	PageID     string            `json:"pageId"`
	AttemptID  string            `json:"attemptId"`
	Input      NativeInputRef    `json:"input"`
	Options    *OCROptions       `json:"options,omitempty"`
	Callback   NativeCallbackRef `json:"callback"`
}

type NativeInputRef struct {
	URL       string `json:"url"`
	MediaType string `json:"mediaType"`
	SHA256    string `json:"sha256,omitempty"`
}

type NativeCallbackRef struct {
	URL string `json:"url"`
}
