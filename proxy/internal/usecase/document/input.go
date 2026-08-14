package document

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"

	"macocr/proxy/domain"
)

type ProcessedInput struct {
	ObjectKey   string
	ContentType string
	SizeBytes   int64
	SHA256      string
}

type ownedObjectRepository interface {
	KeyFromOwnURL(rawURL string, userID int64) (key string, owned bool, err error)
	Stat(ctx context.Context, key string) (*domain.ObjectInfo, error)
}

const MaxUploadedObjectBytes = MaxURLDownloadBytes

func ProcessBase64(ctx context.Context, userID int64, rawB64 string, objects domain.ObjectRepository) (*ProcessedInput, error) {
	if rawB64 != strings.TrimSpace(rawB64) || strings.IndexFunc(rawB64, unicode.IsSpace) >= 0 {
		return nil, fmt.Errorf("%w: whitespace is not allowed", domain.ErrInvalidBase64)
	}
	if rawB64 == "" {
		return nil, fmt.Errorf("%w: base64 payload is empty", domain.ErrBadParamInput)
	}

	decodedSizeUpperBound := base64.StdEncoding.DecodedLen(len(rawB64))
	if strings.HasSuffix(rawB64, "==") {
		decodedSizeUpperBound -= 2
	} else if strings.HasSuffix(rawB64, "=") {
		decodedSizeUpperBound--
	}
	if decodedSizeUpperBound > MaxBase64Bytes {
		return nil, fmt.Errorf("%w: maximum is %d decoded bytes", domain.ErrBase64TooLarge, MaxBase64Bytes)
	}
	data, err := base64.StdEncoding.Strict().DecodeString(rawB64)
	if err != nil {
		return nil, fmt.Errorf("%w: encoding or padding is invalid", domain.ErrInvalidBase64)
	}

	if int64(len(data)) > MaxBase64Bytes {
		return nil, fmt.Errorf("%w: maximum is %d decoded bytes", domain.ErrBase64TooLarge, MaxBase64Bytes)
	}

	contentType, err := DetectMIME(data)
	if err != nil {
		return nil, err
	}
	if err := ValidateFile(data, contentType); err != nil {
		return nil, err
	}

	sum := sha256.Sum256(data)
	key := makeInputKey(userID, "base64.bin")

	if err := objects.Put(ctx, key, bytes.NewReader(data), contentType); err != nil {
		return nil, fmt.Errorf("save to object store: %w", err)
	}

	return &ProcessedInput{
		ObjectKey:   key,
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
		SHA256:      hex.EncodeToString(sum[:]),
	}, nil
}

func ProcessURL(ctx context.Context, userID int64, rawURL string, objects domain.ObjectRepository) (*ProcessedInput, error) {
	return ProcessURLWithUploadLimit(ctx, userID, rawURL, objects, MaxUploadedObjectBytes)
}

func ProcessURLWithUploadLimit(ctx context.Context, userID int64, rawURL string, objects domain.ObjectRepository, maxUploadedBytes int64) (*ProcessedInput, error) {
	if ownedObjects, ok := objects.(ownedObjectRepository); ok {
		key, owned, err := ownedObjects.KeyFromOwnURL(rawURL, userID)
		if err != nil {
			return nil, err
		}
		if owned {
			return ProcessOwnedObject(ctx, key, objects, ownedObjects, maxUploadedBytes)
		}
	}

	if err := ValidateURL(rawURL); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "OCR-Platform-Fetcher/1.0")

	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("invalid upstream address: %w", err)
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil || len(ips) == 0 {
				return nil, fmt.Errorf("resolve upstream host: %w", err)
			}
			for _, resolved := range ips {
				if IsBlockedIP(resolved.IP) {
					continue
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
			}
			return nil, fmt.Errorf("all resolved addresses are blocked")
		},
		TLSHandshakeTimeout: 5 * time.Second,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return ValidateURL(req.URL.String())
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: HTTPS fetch failed", domain.ErrInvalidURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: upstream returned HTTP %d", domain.ErrInvalidURL, resp.StatusCode)
	}
	if resp.ContentLength > MaxURLDownloadBytes {
		return nil, fmt.Errorf("%w: maximum is %d bytes", domain.ErrURLContentTooLarge, MaxURLDownloadBytes)
	}

	headerBuf := make([]byte, 512)
	n, err := io.ReadFull(resp.Body, headerBuf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("read URL header: %w", err)
	}
	if n < 3 {
		return nil, fmt.Errorf("%w: fetched file too short or empty", domain.ErrBadParamInput)
	}

	contentType, err := DetectMIME(headerBuf[:n])
	if err != nil {
		return nil, err
	}

	combined := io.MultiReader(bytes.NewReader(headerBuf[:n]), resp.Body)
	hasher := sha256.New()
	tee := io.TeeReader(combined, hasher)

	limited := io.LimitReader(tee, MaxURLDownloadBytes+1)
	buf := new(bytes.Buffer)
	written, err := io.Copy(buf, limited)
	if err != nil {
		return nil, fmt.Errorf("buffer url file: %w", err)
	}
	if written > MaxURLDownloadBytes {
		return nil, fmt.Errorf("%w: maximum is %d bytes", domain.ErrURLContentTooLarge, MaxURLDownloadBytes)
	}
	if err := ValidateFile(buf.Bytes(), contentType); err != nil {
		return nil, err
	}

	key := makeInputKey(userID, "url_snapshot.bin")
	if err := objects.Put(ctx, key, bytes.NewReader(buf.Bytes()), contentType); err != nil {
		return nil, fmt.Errorf("save snapshot to object store: %w", err)
	}

	return &ProcessedInput{
		ObjectKey:   key,
		ContentType: contentType,
		SizeBytes:   written,
		SHA256:      hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func ProcessOwnedObject(ctx context.Context, key string, objects domain.ObjectRepository, ownedObjects ownedObjectRepository, maxUploadedBytes int64) (*ProcessedInput, error) {
	if maxUploadedBytes <= 0 {
		maxUploadedBytes = MaxUploadedObjectBytes
	}
	info, err := ownedObjects.Stat(ctx, key)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("%w: uploaded object does not exist or is not readable", domain.ErrInvalidURL)
		}
		return nil, fmt.Errorf("%w: stat uploaded object: %v", domain.ErrStorageUnavailable, err)
	}
	if info.SizeBytes <= 0 {
		return nil, fmt.Errorf("%w: uploaded object is empty", domain.ErrBadParamInput)
	}
	if info.SizeBytes > maxUploadedBytes {
		return nil, fmt.Errorf("%w: uploaded object maximum is %d bytes", domain.ErrURLContentTooLarge, maxUploadedBytes)
	}

	rc, err := objects.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("%w: read uploaded object: %v", domain.ErrStorageUnavailable, err)
	}
	defer rc.Close()

	hasher := sha256.New()
	tee := io.TeeReader(rc, hasher)
	limited := io.LimitReader(tee, maxUploadedBytes+1)
	buf := new(bytes.Buffer)
	written, err := io.Copy(buf, limited)
	if err != nil {
		return nil, fmt.Errorf("buffer uploaded object: %w", err)
	}
	if written > maxUploadedBytes {
		return nil, fmt.Errorf("%w: uploaded object maximum is %d bytes", domain.ErrURLContentTooLarge, maxUploadedBytes)
	}
	if written != info.SizeBytes {
		return nil, fmt.Errorf("%w: uploaded object changed while being validated", domain.ErrConflict)
	}
	contentType, err := DetectMIME(buf.Bytes())
	if err != nil {
		return nil, err
	}
	if err := ValidateFile(buf.Bytes(), contentType); err != nil {
		return nil, err
	}

	return &ProcessedInput{
		ObjectKey:   key,
		ContentType: contentType,
		SizeBytes:   written,
		SHA256:      hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func makeInputKey(userID int64, filename string) string {
	ts := time.Now().UTC().Format("20060102T150405.000")
	safe := sanitizeFilename(filename)
	random := make([]byte, 6)
	_, _ = rand.Read(random)
	return fmt.Sprintf("inputs/%d/%s_%x_%s", userID, ts, random, safe)
}

func MakeUploadKey(userID int64, filename string) string {
	ts := time.Now().UTC().Format("20060102T150405.000")
	safe := sanitizeFilename(filename)
	random := make([]byte, 12)
	_, _ = rand.Read(random)
	return fmt.Sprintf("uploads/%d/%s_%x_%s", userID, ts, random, safe)
}

func sanitizeFilename(name string) string {
	base := path.Base(name)
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, base)
	if base == "" || base == "." {
		return "file.bin"
	}
	return base
}
