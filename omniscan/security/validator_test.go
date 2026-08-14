package security

import (
	"testing"
)

func TestValidator_ValidateURL(t *testing.T) {
	v := NewValidator()

	validURLs := []string{
		"https://example.com/document.png",
		"http://files.sample.com/invoice.pdf",
		"https://images.unsplash.com/photo-123456",
	}

	for _, u := range validURLs {
		if err := v.ValidateURL(u); err != nil {
			t.Errorf("expected URL '%s' to be valid, got error: %v", u, err)
		}
	}

	invalidURLs := []string{
		"",
		"ftp://example.com/image.png",
		"file:///etc/passwd",
		"http://127.0.0.1/secret.png",
		"http://localhost/admin.png",
		"http://192.168.1.100/internal.pdf",
		"http://169.254.169.254/latest/meta-data/",
		"https://example.com/script.exe",
	}

	for _, u := range invalidURLs {
		if err := v.ValidateURL(u); err == nil {
			t.Errorf("expected URL '%s' to be rejected, but it passed", u)
		}
	}
}

func TestValidator_ValidateAttachment(t *testing.T) {
	v := NewValidator()

	if err := v.ValidateAttachment("invoice.pdf", 1024*1024); err != nil {
		t.Errorf("expected valid attachment, got: %v", err)
	}

	if err := v.ValidateAttachment("script.exe", 1024); err == nil {
		t.Errorf("expected invalid extension script.exe to be rejected")
	}

	if err := v.ValidateAttachment("large.png", 105*1024*1024); err == nil {
		t.Errorf("expected oversized attachment (>100MB) to be rejected")
	}
}
