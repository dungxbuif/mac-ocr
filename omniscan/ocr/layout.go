package ocr

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// rowTolerance is the maximum difference between two blocks' normalised top
// coordinate for them to be considered part of the same visual row.
// 0.015 ≈ 1.5% of page height — works for most A4 documents at standard font sizes.
// Tighter values preserve small superscripts; looser values merge nearby header
// lines. Adjust if your documents use very small leading.
const rowTolerance = 0.015

// columnGapThreshold is the minimum normalised horizontal gap (relative to page
// width) that triggers a tab stop / column separator between adjacent blocks in
// the same row. 0.02 ≈ 2% of page width. Blocks closer than this are joined
// with a single space; wider gaps produce a tab character.
const columnGapThreshold = 0.02

// ReconstructLayout converts a *ResultPayload into a human-readable string with
// correct 2D reading order: left-to-right within a row, top-to-bottom across
// rows. If the payload has no page/block data it falls back to the server-built
// result.text verbatim — so old integrations never regress.
//
// Algorithm:
//  1. For each page, convert each Block's Vision bbox [x, y, w, h] (lower-left
//     origin, normalised) to a (top, left) pair with a top-left origin.
//  2. Sort blocks by top ascending, then left ascending.
//  3. Group blocks whose |top_i - top_j| ≤ rowTolerance into the same row.
//  4. Within each row sort blocks left→right.
//  5. Join neighbouring blocks with " " or "\t" depending on the horizontal gap.
//  6. Join rows with "\n"; join pages with "\n\n--- PAGE BREAK ---\n\n".
func ReconstructLayout(result *ResultPayload) string {
	if result == nil {
		return ""
	}
	if len(result.Pages) == 0 {
		// No structural data — return server text unchanged.
		return result.Text
	}

	var pageTexts []string
	for _, page := range result.Pages {
		if len(page.Blocks) == 0 {
			// Use server-computed page text if no blocks present.
			pageTexts = append(pageTexts, page.Text)
			continue
		}
		pageTexts = append(pageTexts, reconstructPage(page.Blocks))
	}

	return strings.Join(pageTexts, "\n\n--- PAGE BREAK ---\n\n")
}

// reconstructPage rebuilds the reading order for a single page's blocks.
func reconstructPage(blocks []Block) string {
	if len(blocks) == 0 {
		return ""
	}

	type positioned struct {
		top  float64 // normalised, 0 = page top
		left float64 // normalised, 0 = page left
		text string
		w    float64 // block width (for gap computation)
	}

	items := make([]positioned, 0, len(blocks))
	for _, b := range blocks {
		if strings.TrimSpace(b.Text) == "" {
			continue
		}
		x, y, w, h := b.BBox[0], b.BBox[1], b.BBox[2], b.BBox[3]
		// Vision: y is bottom of block, origin lower-left.
		top := 1.0 - y - h
		// Clamp to [0,1] to handle any floating-point edge cases.
		top = math.Max(0, math.Min(1, top))
		items = append(items, positioned{top: top, left: x, text: b.Text, w: w})
	}

	// Sort top→bottom, left→right as tiebreaker.
	sort.Slice(items, func(i, j int) bool {
		if math.Abs(items[i].top-items[j].top) > rowTolerance {
			return items[i].top < items[j].top
		}
		return items[i].left < items[j].left
	})

	// Group into rows.
	type row []positioned
	var rows []row
	for _, item := range items {
		if len(rows) == 0 {
			rows = append(rows, row{item})
			continue
		}
		lastRow := &rows[len(rows)-1]
		refTop := (*lastRow)[0].top
		if math.Abs(item.top-refTop) <= rowTolerance {
			*lastRow = append(*lastRow, item)
		} else {
			rows = append(rows, row{item})
		}
	}

	// Build output lines.
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		// Ensure left→right order within the row.
		sort.Slice(r, func(i, j int) bool { return r[i].left < r[j].left })

		var sb strings.Builder
		for i, item := range r {
			if i == 0 {
				sb.WriteString(item.text)
				continue
			}
			prev := r[i-1]
			gap := item.left - (prev.left + prev.w)
			if gap > columnGapThreshold {
				sb.WriteString("\t")
			} else {
				sb.WriteString(" ")
			}
			sb.WriteString(item.text)
		}
		lines = append(lines, sb.String())
	}

	return strings.Join(lines, "\n")
}

// OCRStats summarises the quality of an OCR result for display in a bot embed.
type OCRStats struct {
	PageCount     int
	BlockCount    int
	AvgConfidence float64
	LowConfBlocks int // blocks with confidence < 0.80
}

// ComputeStats returns aggregate metrics from a ResultPayload.
func ComputeStats(result *ResultPayload) OCRStats {
	if result == nil {
		return OCRStats{}
	}
	s := OCRStats{PageCount: result.PageCount}
	if s.PageCount == 0 {
		s.PageCount = len(result.Pages)
	}
	var total float64
	for _, p := range result.Pages {
		for _, b := range p.Blocks {
			s.BlockCount++
			total += b.Confidence
			if b.Confidence < 0.80 {
				s.LowConfBlocks++
			}
		}
	}
	if s.BlockCount > 0 {
		s.AvgConfidence = total / float64(s.BlockCount)
	}
	return s
}

// ConfidenceBadge returns an emoji badge based on average confidence.
func ConfidenceBadge(avg float64) string {
	switch {
	case avg >= 0.95:
		return "🟢"
	case avg >= 0.80:
		return "🟡"
	default:
		return "🔴"
	}
}

// FormatStats returns a one-line summary string for use in an embed field.
func FormatStats(s OCRStats) string {
	if s.BlockCount == 0 {
		return "Không có dữ liệu khối (blocks)"
	}
	badge := ConfidenceBadge(s.AvgConfidence)
	return fmt.Sprintf("%s Độ chính xác %.1f%%  •  %d trang  •  %d khối chữ",
		badge, s.AvgConfidence*100, s.PageCount, s.BlockCount)
}
