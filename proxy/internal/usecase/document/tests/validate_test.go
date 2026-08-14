package tests

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"macocr/proxy/domain"
	"macocr/proxy/internal/usecase/document"
)

type mockObjectRepo struct {
	stored map[string][]byte
}

func newMockObjectRepo() *mockObjectRepo {
	return &mockObjectRepo{stored: make(map[string][]byte)}
}

func (m *mockObjectRepo) Put(ctx context.Context, key string, body io.Reader, contentType string) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.stored[key] = data
	return nil
}

func (m *mockObjectRepo) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	data, ok := m.stored[key]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *mockObjectRepo) Exists(ctx context.Context, key string) (bool, error) {
	_, ok := m.stored[key]
	return ok, nil
}

func (m *mockObjectRepo) PresignGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return "http://mock-s3/" + key, nil
}

func (m *mockObjectRepo) Ping(ctx context.Context) error {
	return nil
}

func (m *mockObjectRepo) Delete(_ context.Context, key string) error {
	delete(m.stored, key)
	return nil
}

func TestDetectMIME(t *testing.T) {
	tests := []struct {
		name    string
		header  []byte
		want    string
		wantErr bool
	}{
		{"JPEG", []byte{0xFF, 0xD8, 0xFF, 0xE0}, "image/jpeg", false},
		{"PNG", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, "image/png", false},
		{"PDF", []byte("%PDF-1.7..."), "application/pdf", false},
		{"TIFF-LE", []byte{0x49, 0x49, 0x2A, 0x00}, "image/tiff", false},
		{"Unknown", []byte("plain text"), "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := document.DetectMIME(tt.header)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DetectMIME() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("DetectMIME() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProcessBase64(t *testing.T) {
	repo := newMockObjectRepo()
	ctx := context.Background()

	pngHeader := validPNGBytes
	b64Data := base64.StdEncoding.EncodeToString(pngHeader)

	res, err := document.ProcessBase64(ctx, 100, b64Data, repo)
	if err != nil {
		t.Fatalf("ProcessBase64 failed: %v", err)
	}
	if res.ContentType != "image/png" {
		t.Errorf("expected image/png, got %s", res.ContentType)
	}
	if len(repo.stored) != 1 {
		t.Errorf("expected 1 stored object, got %d", len(repo.stored))
	}

}

func TestValidateURL_SSRF(t *testing.T) {
	err := document.ValidateURL("http://127.0.0.1:8080/secret")
	if err == nil {
		t.Error("expected SSRF error on 127.0.0.1, got nil")
	}

	err = document.ValidateURL("http://169.254.169.254/latest/meta-data")
	if err == nil {
		t.Error("expected SSRF error on cloud metadata IP, got nil")
	}
}

func TestProcessBase64ValidationErrors(t *testing.T) {
	repo := newMockObjectRepo()
	_, err := document.ProcessBase64(context.Background(), 1, "not-valid***", repo)
	if !errors.Is(err, domain.ErrInvalidBase64) {
		t.Fatalf("expected ErrInvalidBase64, got %v", err)
	}

	tooLarge := strings.Repeat("A", base64.StdEncoding.EncodedLen(document.MaxBase64Bytes+1))
	_, err = document.ProcessBase64(context.Background(), 1, tooLarge, repo)
	if !errors.Is(err, domain.ErrBase64TooLarge) {
		t.Fatalf("expected ErrBase64TooLarge, got %v", err)
	}

	validWithWhitespace := base64.StdEncoding.EncodeToString(validPNGBytes)
	validWithWhitespace = validWithWhitespace[:8] + "\n" + validWithWhitespace[8:]
	_, err = document.ProcessBase64(context.Background(), 1, validWithWhitespace, repo)
	if !errors.Is(err, domain.ErrInvalidBase64) {
		t.Fatalf("expected whitespace to produce ErrInvalidBase64, got %v", err)
	}
	if len(repo.stored) != 0 {
		t.Fatalf("invalid Base64 stored %d objects", len(repo.stored))
	}
}

func TestValidateFileRejectsActivePDF(t *testing.T) {
	data := []byte("%PDF-1.7\n1 0 obj << /OpenAction 2 0 R /JavaScript (alert) >>\nendobj\n%%EOF")
	err := document.ValidateFile(data, "application/pdf")
	if !errors.Is(err, domain.ErrFileValidation) {
		t.Fatalf("expected ErrFileValidation, got %v", err)
	}
}

func TestValidateNotification(t *testing.T) {
	cfg, err := document.ValidateNotification(&domain.NotificationConfig{Type: "sse"})
	if err != nil || cfg.Type != "sse" {
		t.Fatalf("valid SSE notification rejected: %v", err)
	}
	if _, err := document.ValidateNotification(&domain.NotificationConfig{Type: "sse", Secret: "must-not-be-here"}); err == nil {
		t.Fatal("SSE secret should be rejected")
	}
	if _, err := document.ValidateNotification(&domain.NotificationConfig{Type: "webhook", URL: "http://127.0.0.1/callback", Secret: "1234567890123456"}); err == nil {
		t.Fatal("insecure/private webhook should be rejected")
	}
}

func TestValidateOptionsPreservesExplicitFalseBooleans(t *testing.T) {
	var requested domain.OCROptions
	if err := json.Unmarshal([]byte(`{"automaticallyDetectsLanguage":false,"usesLanguageCorrection":false}`), &requested); err != nil {
		t.Fatalf("decode options: %v", err)
	}

	validated, err := document.ValidateOptions(&requested)
	if err != nil {
		t.Fatalf("validate options: %v", err)
	}
	if validated.AutomaticallyDetectsLanguage == nil || *validated.AutomaticallyDetectsLanguage {
		t.Fatal("explicit automaticallyDetectsLanguage=false was not preserved")
	}
	if validated.UsesLanguageCorrection == nil || *validated.UsesLanguageCorrection {
		t.Fatal("explicit usesLanguageCorrection=false was not preserved")
	}

	encoded, err := json.Marshal(validated)
	if err != nil {
		t.Fatalf("encode options: %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"automaticallyDetectsLanguage":false`)) || !bytes.Contains(encoded, []byte(`"usesLanguageCorrection":false`)) {
		t.Fatalf("explicit false options disappeared from worker payload: %s", encoded)
	}
}

func TestValidateOptionsAppliesBooleanDefaultsWhenOmitted(t *testing.T) {
	validated, err := document.ValidateOptions(&domain.OCROptions{})
	if err != nil {
		t.Fatalf("validate options: %v", err)
	}
	if validated.AutomaticallyDetectsLanguage == nil || !*validated.AutomaticallyDetectsLanguage {
		t.Fatal("automaticallyDetectsLanguage default should be true")
	}
	if validated.UsesLanguageCorrection == nil || !*validated.UsesLanguageCorrection {
		t.Fatal("usesLanguageCorrection default should be true")
	}
	if validated.RecognitionLevel != "accurate" {
		t.Fatalf("recognitionLevel default should be accurate, got %q", validated.RecognitionLevel)
	}
	if !reflect.DeepEqual(validated.Languages, []string{"vi-VN", "en-US"}) {
		t.Fatalf("languages defaults mismatch: %#v", validated.Languages)
	}
}

func TestValidateOptionsPartialObjectKeepsUnspecifiedDefaults(t *testing.T) {
	validated, err := document.ValidateOptions(&domain.OCROptions{RecognitionLevel: "fast"})
	if err != nil {
		t.Fatalf("validate partial options: %v", err)
	}
	if validated.RecognitionLevel != "fast" {
		t.Fatalf("explicit recognitionLevel was not preserved: %q", validated.RecognitionLevel)
	}
	if !reflect.DeepEqual(validated.Languages, []string{"vi-VN", "en-US"}) {
		t.Fatalf("omitted languages did not receive defaults: %#v", validated.Languages)
	}
	if validated.AutomaticallyDetectsLanguage == nil || !*validated.AutomaticallyDetectsLanguage {
		t.Fatal("omitted automaticallyDetectsLanguage should default to true")
	}
	if validated.UsesLanguageCorrection == nil || !*validated.UsesLanguageCorrection {
		t.Fatal("omitted usesLanguageCorrection should default to true")
	}
}
