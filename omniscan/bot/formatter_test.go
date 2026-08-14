package bot

import (
	"strings"
	"testing"
)

func TestFormatOCRReply(t *testing.T) {
	// Test empty text
	emptyReply := FormatOCRReply("", 1, 5)
	if !strings.Contains(emptyReply, "Không tìm thấy văn bản nào") {
		t.Errorf("expected empty text notice, got: %s", emptyReply)
	}

	// Test backtick escaping
	textWithBackticks := "Code block: ```python print('hello') ```"
	escapedReply := FormatOCRReply(textWithBackticks, 2, 5)
	if strings.Contains(escapedReply, "```python") {
		t.Errorf("expected inner backticks to be escaped, got: %s", escapedReply)
	}

	// Test character limit truncation
	longText := strings.Repeat("A", 4000)
	truncatedReply := FormatOCRReply(longText, 3, 5)
	if !strings.Contains(truncatedReply, "Văn bản đã được cắt ngắn 3,000 ký tự") {
		t.Errorf("expected truncation warning for 4000 chars, got: %s", truncatedReply)
	}
}
