package pdfinspector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"macocr/proxy/domain"
)

// Result is the stable subset of pdf-inspector's JSON envelope used by the
// proxy. Markdown is emitted with page markers so the public page result shape
// remains compatible with OCR responses.
type Result struct {
	PDFType         string `json:"pdf_type"`
	PageCount       int    `json:"page_count"`
	HasText         bool   `json:"has_text"`
	Markdown        string `json:"markdown"`
	PagesNeedingOCR []int  `json:"pages_needing_ocr"`
}

func (r Result) UsableText() bool {
	return strings.TrimSpace(r.Markdown) != "" && r.PageCount > 0 &&
		(r.PDFType == "text_based" || (r.PDFType == "mixed" && len(r.PagesNeedingOCR) == 0))
}

func (r Result) ToOCRResult() *domain.OCRResult {
	text := strings.TrimSpace(r.Markdown)
	if text == "" {
		return &domain.OCRResult{PageCount: r.PageCount}
	}
	pages := splitPages(text, r.PageCount)
	pageResults := make([]domain.OCRPageResult, 0, len(pages))
	var full []string
	for i, pageText := range pages {
		pageText = strings.TrimSpace(pageText)
		if pageText == "" {
			continue
		}
		pageResults = append(pageResults, domain.OCRPageResult{PageNumber: i + 1, Text: pageText})
		full = append(full, pageText)
	}
	if len(full) == 0 {
		full = []string{text}
		pageResults = []domain.OCRPageResult{{PageNumber: 1, Text: text}}
	}
	return &domain.OCRResult{Text: strings.Join(full, "\n\n--- PAGE BREAK ---\n\n"), PageCount: r.PageCount, Pages: pageResults}
}

func splitPages(markdown string, pageCount int) []string {
	parts := strings.Split(markdown, "<!-- Page ")
	if len(parts) == 1 {
		return []string{markdown}
	}
	pages := make([]string, pageCount)
	for _, part := range parts[1:] {
		idx := strings.Index(part, " -->")
		if idx < 1 {
			continue
		}
		var page int
		if _, err := fmt.Sscanf(part[:idx], "%d", &page); err != nil || page < 1 || page > pageCount {
			continue
		}
		pages[page-1] = part[idx+4:]
	}
	return pages
}

// Processor extracts a PDF without using a Mac OCR slot.
type Processor interface {
	Extract(context.Context, *domain.Document) (Result, error)
}

// CommandProcessor invokes a pinned pdf2md binary. A missing binary is
// reported as ErrUnavailable so auto mode can safely fall back to OCR.
type CommandProcessor struct {
	Binary  string
	Objects domain.ObjectRepository
	Timeout time.Duration
}

var ErrUnavailable = errors.New("pdf text extractor unavailable")

func NewCommandProcessor(binary string, objects domain.ObjectRepository, timeout time.Duration) *CommandProcessor {
	if strings.TrimSpace(binary) == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return &CommandProcessor{Binary: binary, Objects: objects, Timeout: timeout}
}

func (p *CommandProcessor) Extract(ctx context.Context, doc *domain.Document) (Result, error) {
	if p == nil || p.Objects == nil || strings.TrimSpace(p.Binary) == "" {
		return Result{}, ErrUnavailable
	}
	if _, err := os.Stat(p.Binary); err != nil {
		return Result{}, fmt.Errorf("%w: %s", ErrUnavailable, p.Binary)
	}
	rc, err := p.Objects.Get(ctx, doc.InputKey)
	if err != nil {
		return Result{}, fmt.Errorf("read PDF input: %w", err)
	}
	defer rc.Close()
	tmp, err := os.CreateTemp("", "macocr-pdf-*.pdf")
	if err != nil {
		return Result{}, fmt.Errorf("create PDF temp file: %w", err)
	}
	path := tmp.Name()
	defer os.Remove(path)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return Result{}, fmt.Errorf("protect PDF temp file: %w", err)
	}
	if _, err := io.Copy(tmp, io.LimitReader(rc, 100*1024*1024+1)); err != nil {
		_ = tmp.Close()
		return Result{}, fmt.Errorf("stage PDF input: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Result{}, fmt.Errorf("close PDF temp file: %w", err)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, p.Binary, "--json", "--pages", "--compact", filepath.Clean(path))
	var stderr bytes.Buffer
	cmd.Stderr = &limitedBuffer{Buffer: &stderr, Limit: 64 * 1024}
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return Result{}, fmt.Errorf("pdf extraction timed out after %s", p.Timeout)
		}
		if strings.TrimSpace(stderr.String()) != "" {
			return Result{}, fmt.Errorf("pdf extraction failed: %s", strings.TrimSpace(stderr.String()))
		}
		return Result{}, fmt.Errorf("pdf extraction failed: %w", err)
	}
	var result Result
	if err := json.Unmarshal(out, &result); err != nil {
		return Result{}, fmt.Errorf("decode pdf-inspector result: %w", err)
	}
	return result, nil
}

type limitedBuffer struct {
	*bytes.Buffer
	Limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	total := len(p)
	if b.Buffer.Len() >= b.Limit {
		return total, nil
	}
	remaining := b.Limit - b.Buffer.Len()
	if len(p) > remaining {
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return total, nil
}
