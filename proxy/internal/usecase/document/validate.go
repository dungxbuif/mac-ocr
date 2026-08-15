package document

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"golang.org/x/image/tiff"
	"golang.org/x/image/webp"
	"macocr/proxy/domain"
)

const (
	MaxURLDownloadBytes = 100 * 1024 * 1024 // deployment safety ceiling for remote URL fetches
	MaxBase64Bytes      = 25 * 1024 * 1024  // 25 MiB decoded
	MaxLanguagesCount   = 10
	MaxCustomWordsCount = 100
	MaxImagePixels      = 40_000_000
	MaxCustomWordBytes  = 128
	MaxCustomWordsBytes = 8 * 1024
	MaxPDFPages         = 500
)

var languagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)
var pdfConfigOnce sync.Once

func DetectMIME(header []byte) (string, error) {
	if len(header) >= 3 && header[0] == 0xFF && header[1] == 0xD8 && header[2] == 0xFF {
		return "image/jpeg", nil
	}
	if len(header) >= 8 && header[0] == 0x89 && header[1] == 0x50 && header[2] == 0x4E && header[3] == 0x47 &&
		header[4] == 0x0D && header[5] == 0x0A && header[6] == 0x1A && header[7] == 0x0A {
		return "image/png", nil
	}
	if len(header) >= 4 && string(header[0:4]) == "%PDF" {
		return "application/pdf", nil
	}
	if len(header) >= 4 && ((header[0] == 0x49 && header[1] == 0x49 && header[2] == 0x2A && header[3] == 0x00) ||
		(header[0] == 0x4D && header[1] == 0x4D && header[2] == 0x00 && header[3] == 0x2A)) {
		return "image/tiff", nil
	}
	if len(header) >= 12 && string(header[0:4]) == "RIFF" && string(header[8:12]) == "WEBP" {
		return "image/webp", nil
	}
	return "", fmt.Errorf("%w: allowed types are JPEG, PNG, TIFF, WebP, and PDF", domain.ErrUnsupportedMediaType)
}

func ValidateURL(rawURL string) error {
	if rawURL != strings.TrimSpace(rawURL) || len(rawURL) > 2048 {
		return fmt.Errorf("%w: URL must not contain surrounding whitespace and cannot exceed 2048 characters", domain.ErrInvalidURL)
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: invalid URL format", domain.ErrInvalidURL)
	}

	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("%w: only HTTPS URLs are allowed", domain.ErrInvalidURL)
	}
	if u.User != nil {
		return fmt.Errorf("%w: credentials in URLs are not allowed", domain.ErrInvalidURL)
	}

	hostname := u.Hostname()
	if hostname == "" {
		return fmt.Errorf("%w: empty host in URL", domain.ErrInvalidURL)
	}

	ips, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("%w: cannot resolve host %q", domain.ErrInvalidURL, hostname)
	}

	for _, ip := range ips {
		if IsBlockedIP(ip) {
			return fmt.Errorf("%w: private, local, or metadata address %s", domain.ErrSSRFBlocked, ip.String())
		}
	}

	return nil
}

func IsBlockedIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}

	metaIP := net.ParseIP("169.254.169.254")
	if ip.Equal(metaIP) {
		return true
	}

	return false
}

func ValidateFile(data []byte, contentType string) error {
	if len(data) < 8 {
		return fmt.Errorf("%w: file is truncated", domain.ErrFileValidation)
	}
	switch contentType {
	case "image/jpeg", "image/png", "image/tiff", "image/webp":
		var cfg image.Config
		var err error
		switch contentType {
		case "image/tiff":
			cfg, err = tiff.DecodeConfig(bytes.NewReader(data))
		case "image/webp":
			cfg, err = webp.DecodeConfig(bytes.NewReader(data))
		default:
			cfg, _, err = image.DecodeConfig(bytes.NewReader(data))
		}
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
			return fmt.Errorf("%w: image is malformed or truncated", domain.ErrFileValidation)
		}
		pixels := int64(cfg.Width) * int64(cfg.Height)
		if pixels > MaxImagePixels {
			return fmt.Errorf("%w: image exceeds %d pixels", domain.ErrFileValidation, MaxImagePixels)
		}
	case "application/pdf":
		if !bytes.Contains(data[max(0, len(data)-2048):], []byte("%%EOF")) {
			return fmt.Errorf("%w: PDF is truncated or missing EOF marker", domain.ErrFileValidation)
		}
		// Note: naive substring checks for /JavaScript, /Launch etc. were removed
		// because they cause false positives when PDF text content contains those
		// words (e.g. a URL like "blog.heroku.com/javascript_in_your_postgres").
		// The pdfcpu structural validator below provides sufficient security.
		pdfConfigOnce.Do(api.DisableConfigDir)
		conf := model.NewDefaultConfiguration()
		conf.ValidationMode = model.ValidationRelaxed
		conf.Optimize = false
		conf.Offline = true
		conf.Limits.MaxStreamBytes = MaxURLDownloadBytes
		conf.Limits.MaxDecodeBytes = 256 * 1024 * 1024
		conf.Limits.MaxImagePixels = MaxImagePixels
		conf.Limits.MaxImageBytes = 160 * 1024 * 1024
		conf.Limits.MaxObjectCount = 500_000
		conf.Limits.MaxObjectStreamCount = 100_000
		conf.Limits.MaxObjectStreamFirst = 8 * 1024 * 1024
		conf.Limits.MaxXRefEntries = 500_000
		conf.Limits.MaxRecursionDepth = 50
		pageCount, err := api.PageCount(bytes.NewReader(data), conf)
		if err != nil || pageCount <= 0 {
			return fmt.Errorf("%w: PDF parser rejected malformed or protected content", domain.ErrFileValidation)
		}
		if pageCount > MaxPDFPages {
			return fmt.Errorf("%w: PDF exceeds %d pages", domain.ErrFileValidation, MaxPDFPages)
		}
	default:
		return fmt.Errorf("%w: unsupported media type", domain.ErrUnsupportedMediaType)
	}
	return nil
}

func ValidateOptions(opts *domain.OCROptions) (*domain.OCROptions, error) {
	defaultTrue := true
	if opts == nil {
		return &domain.OCROptions{
			RecognitionLevel:             "accurate",
			Languages:                    []string{"vi-VN", "en-US"},
			AutomaticallyDetectsLanguage: &defaultTrue,
			UsesLanguageCorrection:       &defaultTrue,
		}, nil
	}

	out := *opts
	if out.Languages == nil {
		out.Languages = []string{"vi-VN", "en-US"}
	}
	if out.AutomaticallyDetectsLanguage == nil {
		value := true
		out.AutomaticallyDetectsLanguage = &value
	}
	if out.UsesLanguageCorrection == nil {
		value := true
		out.UsesLanguageCorrection = &value
	}
	if out.RecognitionLevel == "" {
		out.RecognitionLevel = "accurate"
	} else if out.RecognitionLevel != "fast" && out.RecognitionLevel != "accurate" {
		return nil, errors.New("recognitionLevel must be 'fast' or 'accurate'")
	}

	if len(out.Languages) > MaxLanguagesCount {
		return nil, fmt.Errorf("languages array exceeds max items (%d)", MaxLanguagesCount)
	}
	seenLanguages := make(map[string]struct{}, len(out.Languages))
	for _, language := range out.Languages {
		if !languagePattern.MatchString(language) {
			return nil, fmt.Errorf("invalid language identifier %q", language)
		}
		normalized := strings.ToLower(language)
		if _, exists := seenLanguages[normalized]; exists {
			return nil, fmt.Errorf("duplicate language identifier %q", language)
		}
		seenLanguages[normalized] = struct{}{}
	}

	if len(out.CustomWords) > MaxCustomWordsCount {
		return nil, fmt.Errorf("customWords array exceeds max items (%d)", MaxCustomWordsCount)
	}
	totalCustomWordBytes := 0
	for _, word := range out.CustomWords {
		wordBytes := len([]byte(word))
		if strings.TrimSpace(word) == "" || wordBytes > MaxCustomWordBytes {
			return nil, fmt.Errorf("customWords entries must contain 1-%d UTF-8 bytes", MaxCustomWordBytes)
		}
		totalCustomWordBytes += wordBytes
	}
	if totalCustomWordBytes > MaxCustomWordsBytes {
		return nil, fmt.Errorf("customWords exceeds %d total UTF-8 bytes", MaxCustomWordsBytes)
	}

	if out.MinimumTextHeight < 0 || out.MinimumTextHeight > 1.0 {
		return nil, errors.New("minimumTextHeight must be between 0.0 and 1.0")
	}

	if len(out.Languages) == 0 && !*out.AutomaticallyDetectsLanguage {
		out.Languages = []string{"vi-VN", "en-US"}
	}

	return &out, nil
}

func ValidateNotification(cfg *domain.NotificationConfig) (*domain.NotificationConfig, error) {
	if cfg == nil {
		return nil, nil
	}
	out := *cfg
	out.Type = strings.ToLower(strings.TrimSpace(out.Type))
	switch out.Type {
	case "webhook":
		out.URL = strings.TrimSpace(out.URL)
		if out.URL == "" {
			return nil, errors.New("notification.url is required for webhook")
		}
		if len(out.URL) > 2048 {
			return nil, errors.New("notification.url exceeds 2048 characters")
		}
		if len(out.Secret) < 16 || len(out.Secret) > 256 {
			return nil, errors.New("notification.secret must contain 16-256 characters")
		}
		if err := ValidateURL(out.URL); err != nil {
			return nil, fmt.Errorf("invalid notification webhook: %w", err)
		}
	case "sse":
		if strings.TrimSpace(out.URL) != "" || out.Secret != "" {
			return nil, errors.New("SSE notification must not include url or secret")
		}
		out.URL, out.Secret = "", ""
	default:
		return nil, errors.New("notification.type must be 'webhook' or 'sse'")
	}
	return &out, nil
}

func IsMatchPrefix(data, prefix []byte) bool {
	return bytes.HasPrefix(data, prefix)
}
