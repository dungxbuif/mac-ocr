package bot

import (
	"fmt"
	"strings"
)

func FormatHelpMessage() string {
	return `🤖 **OMNISCAN AI ASSISTANT — HƯỚNG DẪN SỬ DỤNG**
────────────────────────────────────────

🚀 **TÍNH NĂNG CHÍNH:**

• **!scan <url>** *(hoặc gửi kèm ảnh/PDF)*
  └─ AI tự nhận diện loại tài liệu (Hóa đơn, CCCD, Hợp đồng...) & định dạng Markdown chuyên nghiệp.

• **!ocr <url>** *(hoặc gửi kèm ảnh/PDF)*
  └─ Bóc tách văn bản thô (Raw OCR) giữ nguyên định dạng gốc.

• **💬 Hỏi - Đáp trực tiếp (Threaded Q&A)**
  └─ Trích dẫn (Reply) tin nhắn kết quả của Bot để hỏi bất kỳ thông tin nào về tài liệu.

────────────────────────────────────────

📌 **LỆNH QUẢN LÝ:**
• **!omniscan help** (hoặc **!omni**, **!help**) ──► Hiển thị bảng hướng dẫn này.
• **!omniscan quota** (hoặc **!quota**) ──► Kiểm tra lượt scan & hỏi đáp còn lại hôm nay.
• **!omniscan ping** (hoặc **!ping**) ──► Kiểm tra trạng thái hoạt động của Bot.

> 💡 **Hạn ngạch mặc định**: 5 lượt scan/ngày per user. Mỗi lượt scan hỗ trợ hỏi đáp 5 câu.`
}

func FormatQuotaMessage(used, total, remaining int) string {
	return fmt.Sprintf(
		"📊 **HẠN NGẠCH SỬ DỤNG OMNISCAN**\n"+
			"────────────────────────\n"+
			"• Đã sử dụng: **%d/%d** lượt scan hôm nay\n"+
			"• Còn lại: **%d** lượt\n"+
			"• Lượt hỏi đáp: **5 câu hỏi / tài liệu**\n"+
			"────────────────────────\n"+
			"> ⏰ *Hạn ngạch scan tự động làm mới vào 00:00 hàng ngày.*",
		used, total, remaining,
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

func FormatAIReply(docType, formatted string, currentCount, maxQuota int) string {
	header := fmt.Sprintf(
		"🏷️ **[%s]** *(Lượt %d/%d)*\n"+
			"────────────────────────\n",
		strings.ToUpper(docType), currentCount, maxQuota,
	)

	footer := "\n\n────────────────────────\n> 💬 *Trích dẫn (Reply) tin nhắn này để hỏi đáp thêm với AI (Tối đa 5 câu)*"

	return header + strings.TrimSpace(formatted) + footer
}
