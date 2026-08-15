package bot

import (
	"context"
	"fmt"
	"log"
	"strings"

	mezon "mezon-bot-sdk"

	"omniscan/storage"
)

// handleScanDetailButton serves the "📄 Văn bản gốc" button on an AI *scan
// result card. It looks up the scan session keyed by the bot message id (the
// message the button is attached to) and replays the raw OCR text in a code
// block, so the user can verify what the OCR engine actually saw before the
// AI reworked it.
func (b *OmniScanBot) handleScanDetailButton(channel *mezon.TextChannel, e *mezon.MessageButtonClick) {
	sess, err := b.sessionStore.GetSession(e.MessageID)
	if err != nil {
		log.Printf("❌ scan_detail %s: %v", e.MessageID, err)
		b.sendText(channel, e.SenderID, "⚠️ Không tìm thấy phiên tài liệu (lỗi đọc session).")
		return
	}
	if sess == nil {
		b.sendText(channel, e.SenderID, "⚠️ Phiên tài liệu đã hết hạn (24h). Hãy `*scan` lại để xem văn bản gốc.")
		return
	}
	raw := strings.TrimSpace(sess.OCRText)
	if raw == "" {
		b.sendText(channel, e.SenderID, "⚠️ Văn bản gốc trống.")
		return
	}
	// Escape any inner fence so we never break out of the code block, then
	// cap at a readable size; the full text is still available via *ocr.
	safe := strings.ReplaceAll(raw, "```", "'''")
	runes := []rune(safe)
	if len(runes) > 3000 {
		safe = string(runes[:3000]) + "\n\n*(cắt ngắn — dùng *ocr để xem đầy đủ)*"
	}
	b.sendText(channel, e.SenderID, fmt.Sprintf("📄 **Văn bản gốc (OCR):**\n```\n%s\n```", safe))
}

// handleScanMoreButton serves the "🧠 Phân tích AI" button on a raw *ocr result
// card. It "upgrades" the OCR text into a full AI analysis: it consumes one
// *scan quota (NOT ocr quota), checks the LLM is up, then runs ClassifyAndFormat
// on the stored OCR text and posts the AI embed in-thread. The result message
// replaces the session with a new one keyed to the AI output.
func (b *OmniScanBot) handleScanMoreButton(channel *mezon.TextChannel, e *mezon.MessageButtonClick) {
	sess, err := b.sessionStore.GetSession(e.MessageID)
	if err != nil {
		log.Printf("❌ scan_more %s: %v", e.MessageID, err)
		b.sendText(channel, e.SenderID, "⚠️ Không tìm thấy phiên tài liệu (lỗi đọc session).")
		return
	}
	if sess == nil {
		b.sendText(channel, e.SenderID, "⚠️ Phiên tài liệu đã hết hạn (24h). Hãy `*ocr` lại để phân tích AI.")
		return
	}
	b.upgradeOCRToScan(channel, e.SenderID, sess)
}

// upgradeOCRToScan runs the AI *scan flow on an existing session's OCR text.
// Used by the "🧠 Phân tích AI" button on a raw *ocr card. It consumes a *scan
// quota, runs ClassifyAndFormat, and posts the AI embed card in the channel
// with a mention. On failure the scan quota is refunded.
func (b *OmniScanBot) upgradeOCRToScan(channel *mezon.TextChannel, userID string, sess *storage.ScanSession) {
	scanLimit, _, askLimit := b.getUserLimits(userID)
	allowed, currentCount, err := b.store.CheckAndIncrementScanQuota(userID, scanLimit)
	if err != nil {
		b.sendText(channel, userID, "⚠️ Có lỗi khi kiểm tra lượt scan.")
		return
	}
	if !allowed {
		b.sendText(channel, userID, fmt.Sprintf("⚠️ Bạn đã dùng hết **%d/%d** lượt scan hôm nay. Vui lòng quay lại ngày mai.", currentCount, scanLimit))
		return
	}
	if !b.agent.IsHealthy() {
		_ = b.store.RefundScanQuota(userID)
		b.sendText(channel, userID, "🔴 **AI Agent chưa sẵn sàng** — vui lòng báo admin kiểm tra LLM endpoint, rồi thử lại. (Đã hoàn 1 lượt scan)")
		return
	}

	b.sendText(channel, userID, fmt.Sprintf("⏳ 🧠 AI đang phân tích tài liệu (Lượt %d/%d), vui lòng chờ...", currentCount, scanLimit))

	go func(s *storage.ScanSession, cur, scanLim, askLim int) {
		ctx, cancel := context.WithTimeout(context.Background(), b.cfg.ScanProcessTimeout)
		defer cancel()

		res, err := b.agent.ClassifyAndFormat(ctx, s.OCRText, "")
		if err != nil {
			log.Printf("⚠️ [upgrade] LLM error: %v", err)
			_ = b.store.RefundScanQuota(userID)
			b.sendText(channel, userID, fmt.Sprintf("❌ **Lỗi AI:** %v (Đã hoàn 1 lượt scan)", err))
			return
		}

		out := BuildScanResult(res.DocType, res.Formatted, cur, scanLim, askLim)
		opts := b.mentionOpts(userID)
		sentMsg, sendErr := channel.Send(out.Content, opts)
		if sendErr != nil {
			log.Printf("❌ [upgrade] send embed: %v", sendErr)
		}
		if len(out.FileBytes) > 0 {
			_ = b.sendFileAttachment(channel, nil, out.FileName, out.FileBytes, "📎 Kết quả AI đầy đủ (Markdown):")
		}
		if sendErr == nil && sentMsg != nil && sentMsg.ID != "" {
			_ = b.sessionStore.CreateSession(sentMsg.ID, userID, s.DocumentID, res.DocType, s.OCRText)
		}
	}(sess, currentCount, scanLimit, askLimit)
}

// mentionOpts builds SendOptions that mention userID, used when posting embed
// cards from button handlers (where there is no source ChannelMessage).
func (b *OmniScanBot) mentionOpts(userID string) *mezon.SendOptions {
	handle := "@" + userID
	if m, ok := mezon.MentionUser(handle, userID, userID); ok {
		return &mezon.SendOptions{Mentions: []mezon.Mention{m}}
	}
	return nil
}
