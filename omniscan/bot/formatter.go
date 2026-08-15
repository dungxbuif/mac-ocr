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

// FormatHelpMessage returns the plain-text help banner.
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

// BuildHelpContent returns the rich embed help card with action buttons.
func BuildHelpContent(scanLimit, ocrLimit, askLimit int) mezon.Content {
	embed := mezon.NewInteractiveBuilder("🤖 OMNISCAN — Trợ lý nhận diện tài liệu AI").
		SetDescription("Trích xuất, phân loại và trả lời về nội dung tài liệu ngay trong Mezon. Mọi lệnh dùng tiền tố `*` — gõ trực tiếp vào chat.").
		AddField("🚀 *scan — Phân tích AI", "Nhận diện loại tài liệu + định dạng Markdown cấu trúc.\n▸ `*scan <url>`\n▸ `*scan \"<prompt riêng>\" <url>`\n▸ Đính kèm ảnh/PDF → gõ `*scan`", false).
		AddField("⚡ *ocr — Văn bản thô (2D Layout)", "Bóc tách giữ nguyên bố cục cột/bảng 2 chiều.\n▸ `*ocr <url>`\n▸ Đính kèm file → gõ `*ocr`", false).
		AddField("💬 Hỏi đáp chuyên sâu (Quote-Reply)", "Trích dẫn (Reply) kết quả bot để hỏi thêm.\n▸ Tối đa "+itoa(askLimit)+" câu hỏi/tài liệu", false).
		AddField("📊 *quota", "Xem lượt còn lại hôm nay", true).
		AddField("❓ *os / *omniscan", "Xem bảng hướng dẫn này", true).
		Build()
	embed["author"] = map[string]any{
		"name": "🤖 OmniScan AI Assistant • Mezon Platform",
	}
	embed["footer"] = map[string]any{
		"text": fmt.Sprintf("Hạn ngạch: %d scan + %d OCR/ngày • %d câu Q&A/tài liệu • Reset 00:00", scanLimit, ocrLimit, askLimit),
	}
	embed["color"] = "#8B5CF6" // Violet

	buttons := mezon.NewButtonBuilder().
		AddButton("omniscan_quota", "📊 Xem Quota", mezon.ButtonPrimary).
		AddButton("omniscan_scan_hint", "💡 Mẹo sử dụng", mezon.ButtonSecondary).
		Build()

	return mezon.InteractiveCard("", embed, buttons)
}

// ────────────────────────────────────────────────────────────────────
// Quota
// ────────────────────────────────────────────────────────────────────

func FormatQuotaMessage(scanUsed, scanTotal, scanRem, ocrUsed, ocrTotal, ocrRem, askLimit int) string {
	return fmt.Sprintf(
		"📊 **HẠN NGẠCH SỬ DỤNG OMNISCAN**\n"+
			"────────────────────────\n"+
			"• 🧠 Scan AI: **%d/%d** lượt (còn **%d**)\n"+
			"• ⚡ OCR thô: **%d/%d** lượt (còn **%d**)\n"+
			"• 💬 Hỏi đáp: **%d câu / tài liệu**\n"+
			"────────────────────────\n"+
			"> ⏰ *Hạn ngạch tự động làm mới vào 00:00 hàng ngày.*",
		scanUsed, scanTotal, scanRem, ocrUsed, ocrTotal, ocrRem, askLimit,
	)
}

// ────────────────────────────────────────────────────────────────────
// OCR Raw reply  —  *ocr command
// ────────────────────────────────────────────────────────────────────

const maxEmbedBody = 3500

// FormatOCRReply renders a plain-text OCR reply.
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
		return fmt.Sprintf("⚡ **KẾT QUẢ RAW OCR (Lượt %d/%d):**\n```text\n%s\n```\n⚠️ *(Cắt ngắn 3,000 ký tự — xem file đầy đủ bên dưới)*", currentCount, maxQuota, safeText)
	}
	return fmt.Sprintf("⚡ **KẾT QUẢ RAW OCR (Lượt %d/%d):**\n```text\n%s\n```", currentCount, maxQuota, safeText)
}

type OCROutput struct {
	Content   mezon.Content
	FileBytes []byte
	FileName  string
}

// docTypeColor returns a sleek hex color tailored to each document type.
func docTypeColor(docType string) string {
	dt := strings.ToLower(docType)
	switch {
	case strings.Contains(dt, "hóa đơn") || strings.Contains(dt, "biên lai") || strings.Contains(dt, "invoice") || strings.Contains(dt, "bill"):
		return "#10B981" // Emerald Green
	case strings.Contains(dt, "hợp đồng") || strings.Contains(dt, "contract") || strings.Contains(dt, "pháp lý"):
		return "#6366F1" // Indigo
	case strings.Contains(dt, "cccd") || strings.Contains(dt, "danh thiếp") || strings.Contains(dt, "giấy tờ") || strings.Contains(dt, "cmnd"):
		return "#3B82F6" // Electric Blue
	case strings.Contains(dt, "bảng") || strings.Contains(dt, "từ vựng") || strings.Contains(dt, "menu") || strings.Contains(dt, "table"):
		return "#F59E0B" // Amber
	case strings.Contains(dt, "y tế") || strings.Contains(dt, "đơn thuốc") || strings.Contains(dt, "bệnh án"):
		return "#EC4899" // Pink
	default:
		return "#8B5CF6" // Violet Purple
	}
}

// BuildOCRResult creates the rich embed for a *ocr response.
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
		truncNote = "\n\n📎 *Văn bản đầy đủ đã được gửi kèm file bên dưới.*"
	}

	stats := ocr.ComputeStats(result)
	statsLine := ocr.FormatStats(stats)

	description := "```text\n" + safeBody + "\n```" + truncNote

	embed := mezon.NewInteractiveBuilder("⚡ KẾT QUẢ RAW OCR (2D Layout)").
		SetDescription(description).
		AddField("📊 Độ chính xác", statsLine, false).
		Build()
	embed["author"] = map[string]any{
		"name": "⚡ MacOCR Native Engine (Apple Silicon)",
	}
	embed["footer"] = map[string]any{
		"text": fmt.Sprintf("Lượt OCR: %d/%d  •  💬 Reply để hỏi AI  •  %s",
			currentCount, maxQuota, time.Now().Format("15:04:05")),
	}
	embed["color"] = "#06B6D4" // Cyan Tech

	buttons := mezon.NewButtonBuilder().
		AddButton("omniscan_scan_more", "🧠 Phân tích AI", mezon.ButtonPrimary).
		AddButton("omniscan_quota", "📊 Xem Quota", mezon.ButtonSecondary).
		AddButton("omniscan_scan_hint", "💡 Mẹo dùng", mezon.ButtonSuccess).
		Build()

	return OCROutput{
		Content:   mezon.InteractiveCard("", embed, buttons),
		FileBytes: fileBytes,
		FileName:  fileName,
	}
}

// BuildOCRResultContent returns only the Content interface for test compatibility.
func BuildOCRResultContent(result *ocr.ResultPayload, reconstructed string, currentCount, maxQuota int) mezon.Content {
	return BuildOCRResult(result, reconstructed, currentCount, maxQuota).Content
}

// ────────────────────────────────────────────────────────────────────
// AI Scan reply  —  *scan command
// ────────────────────────────────────────────────────────────────────

type ScanOutput struct {
	Content   mezon.Content
	FileBytes []byte
	FileName  string
}

const embedDescriptionLimit = 4096

// BuildScanResult converts an AI ClassifyResult into a rich embed card.
func BuildScanResult(docType, formatted string, currentCount, maxQuota, askLimit int) ScanOutput {
	docTypeClean := strings.ToUpper(strings.TrimSpace(docType))
	if docTypeClean == "" {
		docTypeClean = "TÀI LIỆU"
	}
	title := "🏷️ " + docTypeClean

	body := strings.TrimSpace(formatted)
	var fileBytes []byte
	var fileName string
	var truncNote string

	if mezon.UTF16Len(body) > embedDescriptionLimit {
		fileBytes = []byte(body)
		fileName = fmt.Sprintf("scan_%s.md", time.Now().Format("20060102_150405"))
		body = mezon.TruncateUTF16(body, embedDescriptionLimit)
		truncNote = "\n\n📎 *Nội dung chi tiết đã được gửi kèm file Markdown bên dưới.*"
	}
	if body == "" {
		body = "_(Không có nội dung được trích xuất.)_"
	}

	embed := mezon.NewInteractiveBuilder(title).
		SetDescription(body + truncNote).
		Build()
	embed["author"] = map[string]any{
		"name": "🤖 OmniScan AI Document Intelligence",
	}
	embed["footer"] = map[string]any{
		"text": fmt.Sprintf("Lượt %d/%d  •  💬 Reply để hỏi đáp (tối đa %d câu)  •  %s",
			currentCount, maxQuota, askLimit, time.Now().Format("15:04:05")),
	}
	embed["color"] = docTypeColor(docType)

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

// BuildScanResultContent returns only the Content interface for test compatibility.
func BuildScanResultContent(docType, formatted string, currentCount, maxQuota, askLimit int) mezon.Content {
	return BuildScanResult(docType, formatted, currentCount, maxQuota, askLimit).Content
}

// FormatAIReply is the plain-text fallback for *scan.
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
// Prompt Control
// ────────────────────────────────────────────────────────────────────

type ScanArgs struct {
	CustomPrompt string
	URL          string
}

func ParseScanArgs(input string) ScanArgs {
	input = strings.TrimSpace(input)
	var args ScanArgs

	if len(input) > 0 && (input[0] == '"' || input[0] == '\'') {
		quote := rune(input[0])
		end := strings.IndexRune(input[1:], quote)
		if end >= 0 {
			args.CustomPrompt = strings.TrimSpace(input[1 : end+1])
			input = strings.TrimSpace(input[end+2:])
		}
	}

	for _, tok := range strings.Fields(input) {
		if strings.HasPrefix(tok, "http://") || strings.HasPrefix(tok, "https://") {
			args.URL = tok
			break
		}
	}

	return args
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
