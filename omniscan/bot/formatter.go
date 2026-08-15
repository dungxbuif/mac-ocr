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
	return `🤖 OMNISCAN — TRỢ LÝ NHẬN DIỆN & PHÂN TÍCH TÀI LIỆU AI
═════════════════════════════════════════════════════════════

OmniScan kết hợp công nghệ OCR Apple Silicon tốc độ cao với Trí tuệ nhân tạo (LLM Reasoning) để đọc hiểu, trích xuất dữ liệu và giải đáp mọi thắc mắc về tài liệu trực tiếp trên Mezon.

📌 CÁC LỆNH CHÍNH (Tiền tố *):

[1] *scan  —  AI Phân Tích & Bóc Tách Thông Minh
    • Tự động nhận diện loại tài liệu (Hóa đơn, Hợp đồng, CCCD, Bảng biểu, Menu, Bài viết...).
    • Trích xuất thông tin trọng yếu thành bảng Markdown, tóm tắt nhanh và phân tích rủi ro.
    • Hỗ trợ Prompt tùy chỉnh theo ý muốn:
        ▸ *scan <url ảnh/PDF>
        ▸ *scan "Dịch toàn bộ sang tiếng Anh" <url>
        ▸ *scan "Chỉ trích xuất mã số thuế và số tài khoản" <url>
        ▸ Đính kèm ảnh/PDF trực tiếp rồi gõ *scan

[2] *ocr  —  Trích Xuất Văn Bản Thô
    • Bóc tách nguyên văn 100% ký tự giữ nguyên hàng, cột, bảng biểu.
    • Tốc độ siêu tốc (<0.5s), không tốn token AI, thích hợp copy mã nguồn, văn bản dài.
        ▸ *ocr <url ảnh/PDF>
        ▸ Đính kèm ảnh/PDF rồi gõ *ocr

[3] 💬 Hỏi Đáp Chuyên Sâu (Threaded Q&A)
    • Nhấn [Reply / Trích dẫn] vào BẤT KỲ tin nhắn nào trong luồng hội thoại để hỏi AI.
    • AI tự động ghi nhớ toàn bộ ngữ cảnh lịch sử các câu hỏi trước đó.
    • Mọi thành viên trong kênh đều có thể tham gia hỏi đáp (tính lượt riêng từng người).

[4] *quota  —  Kiểm Tra Lượt Dùng
    • Xem số lượt scan AI, OCR thô và hỏi đáp còn lại trong ngày (Reset lúc 00:00).

[5] *os / *omniscan  —  Mở Bảng Hướng Dẫn Này

═════════════════════════════════════════════════════════════
📁 Định dạng hỗ trợ: JPG · PNG · WEBP · TIFF · PDF (Tối đa 100MB)
💡 Mẹo: Với hóa đơn hoặc hợp đồng, hãy dùng *scan để AI lập bảng tóm tắt và chỉ ra các điểm cần lưu ý!`
}

// BuildHelpContent returns the rich embed help card with action buttons.
func BuildHelpContent(scanLimit, ocrLimit, askLimit int) mezon.Content {
	embed := mezon.NewInteractiveBuilder("🤖 OMNISCAN — Hướng dẫn sử dụng AI Document Assistant").
		SetDescription("Trích xuất, phân loại và hỏi đáp chuyên sâu về tài liệu trực tiếp trên Mezon. Mọi lệnh bắt đầu bằng tiền tố `*`.").
		AddField("🚀 *scan — Phân tích AI & Bóc tách", "Đọc hiểu tài liệu, lập bảng dữ liệu, tóm tắt & phân tích rủi ro.\n▸ `*scan <url>`\n▸ `*scan \"<prompt yêu cầu>\" <url>` *(ví dụ: *scan \"dịch sang tiếng Anh\" <url>)*\n▸ Đính kèm file ảnh/PDF → gõ `*scan`", false).
		AddField("⚡ *ocr — Bóc tách văn bản thô", "Trích xuất 100% ký tự nguyên bản giữ nguyên cột bảng hai chiều.\n▸ `*ocr <url>` hoặc đính kèm ảnh/PDF → gõ `*ocr`", false).
		AddField("💬 Hỏi đáp ngữ cảnh (Quote-Reply)", "Nhấn **Reply** vào bất kỳ tin nhắn nào trong luồng để hỏi tiếp AI.\n▸ Ghi nhớ toàn bộ lịch sử trò chuyện • Đa người dùng tham gia", false).
		AddField("📊 *quota", "Xem lượt còn lại hôm nay", true).
		AddField("❓ *os / *omniscan", "Xem bảng hướng dẫn này", true).
		Build()
	embed["author"] = map[string]any{
		"name": "🤖 OmniScan AI Assistant • Powered by Apple Silicon MacOCR",
	}
	embed["footer"] = map[string]any{
		"text": fmt.Sprintf("Hạn ngạch: %d scan + %d OCR/ngày • %d câu Q&A/tài liệu • Reset 00:00", scanLimit, ocrLimit, askLimit),
	}
	embed["color"] = "#8B5CF6" // Violet Purple

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

	description := "```text\n" + safeBody + "\n```" + truncNote

	embed := mezon.NewInteractiveBuilder("⚡ KẾT QUẢ RAW OCR").
		SetDescription(description).
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
