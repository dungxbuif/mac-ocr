package pdfinspector

import "testing"

func TestResultToOCRResultPreservesPageMarkers(t *testing.T) {
	r := Result{PDFType: "text_based", PageCount: 2, HasText: true, Markdown: "<!-- Page 1 -->\nFirst\n<!-- Page 2 -->\nSecond"}
	got := r.ToOCRResult()
	if got.Text != "First\n\n--- PAGE BREAK ---\n\nSecond" {
		t.Fatalf("unexpected full text: %q", got.Text)
	}
	if len(got.Pages) != 2 || got.Pages[0].PageNumber != 1 || got.Pages[1].Text != "Second" {
		t.Fatalf("unexpected pages: %#v", got.Pages)
	}
}

func TestResultUsableTextRequiresNativePages(t *testing.T) {
	base := Result{PageCount: 1, Markdown: "hello"}
	for _, tc := range []struct {
		name string
		typ  string
		ocr  []int
		want bool
	}{
		{"text", "text_based", nil, true},
		{"scanned", "scanned", nil, false},
		{"mixed-with-ocr", "mixed", []int{2}, false},
		{"mixed-native", "mixed", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := base
			got.PDFType, got.PagesNeedingOCR = tc.typ, tc.ocr
			if got.UsableText() != tc.want {
				t.Fatalf("usable=%v want=%v", got.UsableText(), tc.want)
			}
		})
	}
}
