package bot

import (
	"fmt"
	"strings"
	"time"

	mezon "mezon-bot-sdk"

	"omniscan/ocr"
)

// ────────────────────────────────────────────────────────────────────
// Help
// ────────────────────────────────────────────────────────────────────

// FormatHelpMessage returns the plain-text help banner, written in the tone
// of a short instruction manual: imperative, third-person, no chatty fillers.
// It lists every supported command with syntax and the exact file types the
// bot accepts, so a first-time user can act on it without guessing.
func FormatHelpMessage() string {
	return `🤖 OMNISCAN — TRỢ LÝ NHẬN DIỆN TÀI LIỆU AI
══════════════════════════════════════════

OmniScan trích xuất, phân loại và trả lời câu hỏi về nội dung tài liệu ngay trong Mezon. Mọi lệnh sử dụng tiền tố * và được gõ trực tiếp vào khung chat.

[1] *scan  —  Phân tích bằng AI
    Nhận diện loại tài liệu (hóa đơn, CCCD, hợp đồng, bài viết...) và trình bày lại dưới dạng Markdown có cấu trúc.
    Cách dùng:
      • *scan <url ảnh/PDF>              —  tải từ URL công khai
      • *scan "<yêu cầu riêng>" <url>    —  ví dụ: *scan "dịch sang tiếng Anh" <url>
      • Đính kèm ảnh/PDF rồi gõ *scan       —  nạp file trực tiếp

[2] *ocr  —  Trích xuất văn bản thô
    Bóc tách văn bản giữ nguyên bố cục hai chiều (bảng, cột), không qua AI; kết quả sát bản gốc.
    Cách dùng:
      • *ocr <url>                       —  tải từ URL công khai
      • Đính kèm file rồi gõ *ocr            —  nạp file trực tiếp

[3] Hỏi đáp tài liệu  —  Trích dẫn (Reply)
    Trích dẫn tin nhắn kết quả của bot và đặt câu hỏi bất kỳ về nội dung đã nhận diện.
    Giới hạn: tối đa 5 câu hỏi mỗi tài liệu.

[4] *quota  —  Xem hạn ngạch
    Kiểm tra số lượt scan còn lại trong ngày.

[5] *os / *omniscan  —  Xem hướng dẫn này

══════════════════════════════════════════
Định dạng hỗ trợ:  JPG · PNG · WEBP · TIFF · PDF
Hạn ngạch:  lượt *scan và *ocr đếm RIÊNG theo ngày; câu hỏi đếm riêng theo tài liệu  ·  reset 00:00 hằng ngày`
}

// BuildHelpContent returns the rich embed help card with action buttons. The
// copy mirrors FormatHelpMessage (instruction-manual tone) but is trimmed to
// fit the embed field width so the card stays compact on mobile.
func BuildHelpContent(scanLimit, ocrLimit, askLimit int) mezon.Content {
	embed := mezon.NewInteractiveBuilder("🤖 OMNISCAN — Trợ lý nhận diện tài liệu").
		SetDescription("Trích xuất, phân loại và trả lời về nội dung tài liệu ngay trong Mezon. Mọi lệnh dùng tiền tố `*` — gõ trực tiếp vào chat.").
		AddField("🚀 *scan — Phân tích AI", "Nhận diện loại tài liệu + định dạng Markdown cấu trúc.\n▸ *scan <url ảnh/PDF>\n▸ *scan \"<prompt riêng>\" <url>\n▸ đính kèm ảnh/PDF → gõ *scan", false).
		AddField("⚡ *ocr — Văn bản thô", "Bóc tách giữ bố cục hai chiều, không qua AI.\n▸ *ocr <url>\n▸ đính kèm file → gõ *ocr", false).
		AddField("💬 Hỏi đáp (Reply)", "Trích dẫn kết quả bot để hỏi thêm.\n▸ tối đa "+itoa(askLimit)+" câu/tài liệu", false).
		AddField("📊 *quota", "Xem lượt còn lại hôm nay", true).
		AddField("❓ *os / *omniscan", "Xem hướng dẫn này", true).
		Build()
	embed["footer"] = map[string]any{
		"text": fmt.Sprintf("JPG·PNG·WEBP·TIFF·PDF  |  %d scan + %d OCR/ngày · %d câu/tài liệu · reset 00:00", scanLimit, ocrLimit, askLimit),
	}

	buttons := mezon.NewButtonBuilder().
		AddButton("omniscan_quota", "📊 Lượt dùng", mezon.ButtonPrimary).
		AddButton("omniscan_scan_hint", "💡 Mẹo scan", mezon.ButtonSecondary).
		Build()

	c := mezon.InteractiveCard("", embed, buttons)
	return c
}

// ────────────────────────────────────────────────────────────────────
// Quota
// ────────────────────────────────────────────────────────────────────

func FormatQuotaMessage(scanUsed, scanTotal, scanRem, ocrUsed, ocrTotal, ocrRem, askLimit int) string {
	return fmt.Sprintf(
		"📊 **HẠN NGẠCH SỬ DỤNG OMNISCAN**\n"+
			"────────────────────────\n"+
			"• Scan AI: **%d/%d** lượt (còn %d)\n"+
			"• OCR thô: **%d/%d** lượt (còn %d)\n"+
			"• Hỏi đáp: **%d câu / tài liệu**\n"+
			"────────────────────────\n"+
			"> ⏰ *Hạn ngạch tự động làm mới vào 00:00 hàng ngày.*",
		scanUsed, scanTotal, scanRem, ocrUsed, ocrTotal, ocrRem, askLimit,
	)
}

// ────────────────────────────────────────────────────────────────────
// OCR Raw reply  —  *ocr command
// ────────────────────────────────────────────────────────────────────

// maxEmbedBody is the UTF-16 budget for the embed description field.
// Texts beyond this are delivered as a .txt file attachment instead.
const maxEmbedBody = 3500

// FormatOCRReply renders a plain-text OCR reply (fallback path when the caller
// cannot / does not use the embed variant).
func FormatOCRReply(rawText string, currentCount, maxQuota int) string {
	cleaned := strings.TrimSpace(rawText)
	if cleaned == "" {
		return fmt.Sprintf("ℹ️ **KẾT QUẢ OCR (Lượt %d/%d):** Không tìm thấy văn bản nào.", currentCount, maxQuota)
	}
	safeText := strings.ReplaceAll(cleaned, "```", "'''")
	const maxChars = 3000
	runes := []rune(safeText)
	truncated := false
	if len(runes) > maxChars {
		safeText = string(runes[:maxChars])
		truncated = true
	}
	if truncated {
		return fmt.Sprintf("⚡ **KẾT QUẢ RAW OCR (Lượt %d/%d):**\n```\n%s\n```\n⚠️ *(Cắt ngắn 3 000 ký tự — gửi file đầy đủ xem bên dưới)*", currentCount, maxQuota, safeText)
	}
	return fmt.Sprintf("⚡ **KẾT QUẢ RAW OCR (Lượt %d/%d):**\n```\n%s\n```", currentCount, maxQuota, safeText)
}

// OCROutput is returned by BuildOCRResultContent to tell the caller how to
// deliver the response: either as an embed-only message or as an embed + a
// plain-text file attachment when the body is too long.
type OCROutput struct {
	// Content is always set — the embed card (or fallback plain text).
	Content mezon.Content
	// FileBytes is set when the OCR text exceeded maxEmbedBody.
	// The caller should upload this as a .txt attachment alongside Content.
	FileBytes []byte
	// FileName is the suggested attachment name, e.g. "ocr_result.txt".
	FileName string
}

// BuildOCRResult creates the rich embed for a *ocr response and returns the
// full OCROutput (including optional file bytes for long texts).
func BuildOCRResult(result *ocr.ResultPayload, reconstructed string, currentCount, maxQuota int) OCROutput {
	body := strings.TrimSpace(reconstructed)
	safeBody := strings.ReplaceAll(body, "```", "'''")

	var fileBytes []byte
	var fileName string
	var truncNote string

	if mezon.UTF16Len(safeBody) > maxEmbedBody {
		fileBytes = []byte(body)
		fileName = fmt.Sprintf("ocr_%s.txt", time.Now().Format("20060102_150405"))
		safeBody = mezon.TruncateUTF16(safeBody, maxEmbedBody)
		truncNote = "\n\n\xf0\x9f\x93\x8e *\xe2\x80\x8b\xe2\x80\x8bV\u0103n b\u1ea3n \u0111\u1ea7y \u0111\u1ee7 \u0111\u01b0\u1ee3c g\u1eedi k\xe8m file b\xean d\u01b0\u1edbi.*"
	}

	description := "```\n" + safeBody + "\n```" + truncNote

	embed := mezon.NewInteractiveBuilder("\u26a1 K\u1ebeT QU\u1ea2 RAW OCR").
		SetDescription(description).
		Build()
	embed["footer"] = map[string]any{
		"text": fmt.Sprintf("L\u01b0\u1ee3t %d/%d  \u2022  \xf0\x9f\x92\xac Reply \u0111\u1ec3 h\u1ecfi AI  \u2022  %s",
			currentCount, maxQuota, time.Now().Format("15:04:05")),
	}
	embed["color"] = "#10B981"

	buttons := mezon.NewButtonBuilder().
		AddButton("omniscan_scan_more", "\xf0\x9f\xa7\xa0 Ph\u00e2n t\xedch AI", mezon.ButtonPrimary).
		AddButton("omniscan_quota", "\xf0\x9f\x93\x8a L\u01b0\u1ee3t d\xf9ng", mezon.ButtonSecondary).
		Build()

	return OCROutput{
		Content:   mezon.InteractiveCard("", embed, buttons),
		FileBytes: fileBytes,
		FileName:  fileName,
	}
}

// BuildOCRResultContent is a convenience wrapper that returns only the embed
// Content (for callers that handle file delivery separately).
func BuildOCRResultContent(result *ocr.ResultPayload, reconstructed string, currentCount, maxQuota int) mezon.Content {
	return BuildOCRResult(result, reconstructed, currentCount, maxQuota).Content
}



// ────────────────────────────────────────────────────────────────────
// AI Scan reply  —  *scan command
// ────────────────────────────────────────────────────────────────────

// ScanOutput mirrors OCROutput for the *scan flow.
type ScanOutput struct {
	Content   mezon.Content
	FileBytes []byte
	FileName  string
}

// embedDescriptionLimit is the cap for the AI scan result description field.
const embedDescriptionLimit = 4096

// BuildScanResult converts an AI ClassifyResult into a rich embed card and
// returns the full ScanOutput (including optional .md file bytes for long texts).
func BuildScanResult(docType, formatted string, currentCount, maxQuota, askLimit int) ScanOutput {
	docType = strings.ToUpper(strings.TrimSpace(docType))
	if docType == "" {
		docType = "TÀI LIỆU"
	}
	title := "🏷️ " + docType

	body := strings.TrimSpace(formatted)
	var fileBytes []byte
	var fileName string
	var truncNote string

	if mezon.UTF16Len(body) > embedDescriptionLimit {
		fileBytes = []byte(body)
		fileName = fmt.Sprintf("scan_%s.md", time.Now().Format("20060102_150405"))
		body = mezon.TruncateUTF16(body, embedDescriptionLimit)
		truncNote = "\n\n📎 *Nội dung đầy đủ được gửi kèm file Markdown bên dưới.*"
	}
	if body == "" {
		body = "_(Không có nội dung được trích xuất.)_"
	}

	embed := mezon.NewInteractiveBuilder(title).
		SetDescription(body + truncNote).
		Build()
	embed["footer"] = map[string]any{
		"text": fmt.Sprintf("Lượt %d/%d  •  💬 Reply để hỏi đáp (tối đa %d câu)", currentCount, maxQuota, askLimit),
	}

	buttons := mezon.NewButtonBuilder().
		AddButton("omniscan_scan_detail", "📄 Văn bản gốc", mezon.ButtonPrimary).
		AddButton("omniscan_quota", "📊 Lượt dùng", mezon.ButtonSecondary).
		Build()

	return ScanOutput{
		Content:   mezon.InteractiveCard("", embed, buttons),
		FileBytes: fileBytes,
		FileName:  fileName,
	}
}

// BuildScanResultContent is a convenience wrapper returning only the Content,
// keeping the existing test interface intact.
func BuildScanResultContent(docType, formatted string, currentCount, maxQuota, askLimit int) mezon.Content {
	return BuildScanResult(docType, formatted, currentCount, maxQuota, askLimit).Content
}

// FormatAIReply is the plain-text fallback for *scan (used for threaded Q&A
// replies where rich embeds are not needed).
func FormatAIReply(docType, formatted string, currentCount, maxQuota int) string {
	header := fmt.Sprintf(
		"🏷️ **[%s]** *(Lượt %d/%d)*\n────────────────────────\n",
		strings.ToUpper(docType), currentCount, maxQuota,
	)
	footer := "\n\n────────────────────────\n> 💬 *Reply tin nhắn này để hỏi đáp thêm với AI (tối đa 5 câu)*"
	budget := 8000 - mezon.UTF16Len(header) - mezon.UTF16Len(footer)
	if budget < 1 {
		budget = 1
	}
	return header + mezon.TruncateUTF16(strings.TrimSpace(formatted), budget) + footer
}

// ────────────────────────────────────────────────────────────────────
// Prompt Control — parse user-supplied inline prompt for *scan
// ────────────────────────────────────────────────────────────────────

// ScanArgs holds the parsed result of a *scan command line.
// Syntax:  *scan ["<custom prompt>"] [<url>]
// Examples:
//
//	*scan https://...
//	*scan "dịch ra tiếng Anh" https://...
//	*scan "chỉ tóm tắt điều khoản quan trọng"   (no URL → use attachment)
type ScanArgs struct {
	// CustomPrompt is the quoted string the user supplied, or "" if absent.
	CustomPrompt string
	// URL is the first http(s) token found after the command, or "".
	URL string
}

// ParseScanArgs extracts an optional quoted prompt and optional URL from the
// text following the *scan prefix token.
//
//	input: everything after "*scan" (already trimmed)
func ParseScanArgs(input string) ScanArgs {
	input = strings.TrimSpace(input)
	var args ScanArgs

	// 1. Try to extract a leading quoted prompt ("..." or '...')
	if len(input) > 0 && (input[0] == '"' || input[0] == '\'') {
		quote := rune(input[0])
		end := strings.IndexRune(input[1:], quote)
		if end >= 0 {
			args.CustomPrompt = strings.TrimSpace(input[1 : end+1])
			input = strings.TrimSpace(input[end+2:])
		}
	}

	// 2. Find first http(s) URL token in the remainder
	for _, tok := range strings.Fields(input) {
		if strings.HasPrefix(tok, "http://") || strings.HasPrefix(tok, "https://") {
			args.URL = tok
			break
		}
	}

	return args
}

// ────────────────────────────────────────────────────────────────────
// helpers
// ────────────────────────────────────────────────────────────────────

func itoa(n int) string { return fmt.Sprintf("%d", n) }
