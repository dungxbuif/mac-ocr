package domain

import "time"

type NativeCapacity struct {
	ConfigVersion  uint64 `json:"configVersion"`
	State          string `json:"state"`
	OperatorLimit  int    `json:"operatorLimit"`
	EffectiveLimit int    `json:"effectiveLimit"`
	Active         int    `json:"active"`
	Available      int    `json:"available"`
	ActiveUnits    int    `json:"activeUnits"`
	AvailableUnits int    `json:"availableUnits"`
	ImageJobUnits  int    `json:"imageJobUnits"`
	PDFJobUnits    int    `json:"pdfJobUnits"`
	Adaptive       bool   `json:"adaptive"`
	ResourceReason string `json:"resourceReason,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

type NativeEvent struct {
	EventID    string            `json:"eventId"`
	Type       string            `json:"type"`
	NodeID     string            `json:"nodeId"`
	BootID     string            `json:"bootId"`
	Sequence   uint64            `json:"sequence"`
	AttemptID  string            `json:"attemptId"`
	DocumentID string            `json:"documentId"`
	Result     *OCRResult        `json:"result,omitempty"`
	ScanResult *NativeScanResult `json:"scanResult,omitempty"`
	Error      string            `json:"error,omitempty"`
	Capacity   NativeCapacity    `json:"capacity"`
	OccurredAt time.Time         `json:"occurredAt"`
}

type NativeScanResult struct {
	MediaType string `json:"mediaType"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

type NativeOCRRequest struct {
	DocumentID string            `json:"documentId"`
	PageID     string            `json:"pageId"`
	AttemptID  string            `json:"attemptId"`
	Input      NativeInputRef    `json:"input"`
	Options    *OCROptions       `json:"options,omitempty"`
	Callback   NativeCallbackRef `json:"callback"`
}

type NativeScanRequest struct {
	DocumentID string              `json:"documentId"`
	AttemptID  string              `json:"attemptId"`
	Input      NativeInputRef      `json:"input"`
	Output     NativeScanOutputRef `json:"output"`
	Callback   NativeCallbackRef   `json:"callback"`
}

type NativeScanOutputRef struct {
	URL       string `json:"url"`
	MediaType string `json:"mediaType"`
}

type NativeInputRef struct {
	URL       string `json:"url"`
	MediaType string `json:"mediaType"`
	SHA256    string `json:"sha256,omitempty"`
}

type NativeCallbackRef struct {
	URL string `json:"url"`
}
