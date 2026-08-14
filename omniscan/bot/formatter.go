package bot

import (
	"fmt"
	"strings"

	mezon "mezon-bot-sdk"
)

// FormatHelpMessage returns the plain-text help banner, used as a fallback or
// for channels that do not render interactive embeds.
func FormatHelpMessage() string {
	return `🤖 **OMNISCAN AI ASSISTANT — HƯỚNG DẪN SỬ DỤNG**
────────────────────────────────────────

🚀 **TÍNH NĂNG CHÍNH:**

• ***scan** <url> *(hoặc gửi kèm ảnh/PDF)*
  └─ AI tự nhận diện loại tài liệu (Hóa đơn, CCCD, Hợp đồng...) & định dạng Markdown chuyên nghiệp.

• ***ocr** <url> *(hoặc gửi kèm ảnh/PDF)*
  └─ Bóc tách văn bản thô (Raw OCR) giữ nguyên định dạng gốc.

• **💬 Hỏi - Đáp trực tiếp (Threaded Q&A)**
  └─ Trích dẫn (Reply) tin nhắn kết quả của Bot để hỏi bất kỳ thông tin nào về tài liệu.

────────────────────────────────────────

📌 **LỆNH QUẢN LÝ:**
• ***os** (hoặc ***os help**) ──► Hiển thị bảng hướng dẫn này.
• ***quota** ──► Kiểm tra lượt scan & hỏi đáp còn lại hôm nay.

> 💡 **Hạn ngạch mặc định**: 5 lượt scan/ngày per user. Mỗi lượt scan hỗ trợ hỏi đáp 5 câu.`
}

// BuildHelpContent returns an interactive embed + buttons content for the help
// command, using the SDK's InteractiveBuilder and ButtonBuilder. It mirrors
// the JSON shape the mezon-js SDK emits, so the Mezon client renders it as a
// rich card with clickable buttons. askLimit is the per-document Q&A quota to
// show in the footer.
func BuildHelpContent(scanLimit, askLimit int) mezon.Content {
	embed := mezon.NewInteractiveBuilder("🤖 OMNISCAN AI ASSISTANT").
		SetDescription("AI nhận diện & định dạng tài liệu qua Mezon. Prefix lệnh: `*`").
		AddField("🚀 *scan", "Phân tích AI + Markdown (url/ảnh)", false).
		AddField("⚡ *ocr", "Bóc tách Raw OCR (url/ảnh)", false).
		AddField("💬 Reply", "Hỏi đáp về tài liệu đã scan", false).
		AddField("📊 *quota", "Xem lượt dùng còn lại", true).
		AddField("❓ *os help", "Hiện bảng này", true).
		Build()
	embed["footer"] = map[string]any{
		"text": fmt.Sprintf("Prefix: *  |  %d scan/ngày  |  %d câu hỏi/tài liệu", scanLimit, askLimit),
	}

	buttons := mezon.NewButtonBuilder().
		AddButton("omniscan_quota", "📊 Lượt dùng", mezon.ButtonPrimary).
		AddButton("omniscan_scan_hint", "💡 Mẹo scan", mezon.ButtonSecondary).
		Build()

	c := map[string]any{"embed": []map[string]any{embed}}
	if len(buttons) > 0 {
		c["components"] = buttons
	}
	return c
}

// FormatQuotaMessage renders the quota card. askLimit is the per-document Q&A
// quota (passed in so it is not hardcoded).
func FormatQuotaMessage(used, total, remaining, askLimit int) string {
	return fmt.Sprintf(
		"📊 **HẠN NGẠCH SỬ DỤNG OMNISCAN**\n"+
			"────────────────────────\n"+
			"• Đã sử dụng: **%d/%d** lượt scan hôm nay\n"+
			"• Còn lại: **%d** lượt\n"+
			"• Lượt hỏi đáp: **%d câu hỏi / tài liệu**\n"+
			"────────────────────────\n"+
			"> ⏰ *Hạn ngạch scan tự động làm mới vào 00:00 hàng ngày.*",
		used, total, remaining, askLimit,
	)
}

func FormatOCRReply(rawText string, currentCount, maxQuota int) string {
	cleaned := strings.TrimSpace(rawText)
	if cleaned == "" {
		return fmt.Sprintf("ℹ️ **KẾT QUẢ OCR (Lượt %d/%d):** Không tìm thấy văn bản nào trong hình ảnh/file đính kèm của bạn.", currentCount, maxQuota)
	}

	safeText := strings.ReplaceAll(cleaned, "```", "'''")

	const maxChars = 3000
	runes := []rune(safeText)
	isTruncated := false
	if len(runes) > maxChars {
		safeText = string(runes[:maxChars])
		isTruncated = true
	}

	if isTruncated {
		return fmt.Sprintf("⚡ **KẾT QUẢ RAW OCR (Lượt %d/%d):**\n```\n%s\n```\n⚠️ *(Văn bản đã được cắt ngắn 3,000 ký tự đầu do giới hạn khung chat)*", currentCount, maxQuota, safeText)
	}

	return fmt.Sprintf("⚡ **KẾT QUẢ RAW OCR (Lượt %d/%d):**\n```\n%s\n```", currentCount, maxQuota, safeText)
}

// FormatAIReply renders the AI-formatted document reply. formatted is
// truncated to the SDK's UTF-16 budget if it would overflow the 8000-unit
// server limit (minus the header/footer overhead), so a huge document never
// fails the send.
func FormatAIReply(docType, formatted string, currentCount, maxQuota int) string {
	header := fmt.Sprintf(
		"🏷️ **[%s]** *(Lượt %d/%d)*\n"+
			"────────────────────────\n",
		strings.ToUpper(docType), currentCount, maxQuota,
	)

	footer := "\n\n────────────────────────\n> 💬 *Trích dẫn (Reply) tin nhắn này để hỏi đáp thêm với AI (Tối đa 5 câu)*"

	// Reserve room for header + footer in UTF-16 units, then truncate the body.
	budget := 8000 - mezon.UTF16Len(header) - mezon.UTF16Len(footer)
	if budget < 1 {
		budget = 1
	}
	body := mezon.TruncateUTF16(strings.TrimSpace(formatted), budget)
	return header + body + footer
}

// embedDescriptionLimit is the cap on embed.description we apply before the
// mezon server limit kicks in. mezon-sdk does not declare a per-field limit in
// its interfaces, so we use 4096 UTF-16 units (a conservative value that also
// matches what most Discord-style embed renderers display without truncating
// on the client). Documents longer than that are truncated with a marker.
const embedDescriptionLimit = 4096

// BuildScanResultContent converts an AI Agent ClassifyResult into a Mezon
// embed card + action buttons, the rich counterpart of FormatAIReply.
//
// Wire shape:
//   {
//     "embed": [{
//       "title": "🏷️ HÓA ĐƠN", "description": <markdown body, ≤4096 UTF-16>,
//       "color": <#hex>, "timestamp": <iso>, "footer": {text}, "fields": []
//     }],
//     "components": [{id, type:1, component:{label, style}}, ...]
//   }
//
// docType is the category the LLM tagged (e.g. "Hóa đơn", "CCCD"); formatted is
// the LLM's markdown body. currentCount/maxQuota are the per-day scan stats for
// the footer; askLimit is the per-document Q&A quota advertised in the footer.
//
// Buttons:
//   - omniscan_scan_more → *scan hint
//   - omniscan_quota     → *quota card
// Button handlers must be wired in handleButtonClicked (see scan_result.go).
//
// Returns a content ready for sendReplyContent (mention is added by the caller
// via SendOptions). Falls back gracefully when body is empty.
func BuildScanResultContent(docType, formatted string, currentCount, maxQuota, askLimit int) mezon.Content {
	docType = strings.ToUpper(strings.TrimSpace(docType))
	if docType == "" {
		docType = "TÀI LIỆU"
	}
	title := "🏷️ " + docType

	body := strings.TrimSpace(formatted)
	var truncNote string
	if mezon.UTF16Len(body) > embedDescriptionLimit {
		body = mezon.TruncateUTF16(body, embedDescriptionLimit)
		truncNote = "\n\n*…(đã cắt ngắn — Reply tin nhắn để hỏi phần còn lại)*"
	}
	if body == "" {
		body = "_(Không có nội dung được trích xuất.)_"
	}
	description := body + truncNote

	embed := mezon.NewInteractiveBuilder(title).
		SetDescription(description).
		Build()
	embed["footer"] = map[string]any{
		"text": fmt.Sprintf("Lượt %d/%d  •  💬 Reply để hỏi đáp (tối đa %d câu/tài liệu)", currentCount, maxQuota, askLimit),
	}

	buttons := mezon.NewButtonBuilder().
		AddButton("omniscan_scan_more", "🔄 Scan tiếp", mezon.ButtonPrimary).
		AddButton("omniscan_quota", "📊 Lượt dùng", mezon.ButtonSecondary).
		Build()

	return mezon.InteractiveCard("", embed, buttons)
}
