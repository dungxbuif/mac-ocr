package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	mezon "github.com/quangledang23/mezon-sdk-go"

	"omniscan/agent"
	"omniscan/config"
	"omniscan/ocr"
	"omniscan/security"
	"omniscan/storage"
)



type OmniScanBot struct {
	client       *mezon.MezonClient
	ocrClient    *ocr.Client
	store        storage.QuotaStore
	sessionStore storage.SessionStore
	validator    *security.Validator
	agent        *agent.Agent
	dedup        storage.Deduplicator
	cfg          *config.Config
}

func New(cfg *config.Config, ocrClient *ocr.Client, store storage.QuotaStore, sessionStore storage.SessionStore, validator *security.Validator, agent *agent.Agent, dedup storage.Deduplicator, sharedStore mezon.SharedStore) (*OmniScanBot, error) {
	useSSL := true
	clientCfg := mezon.ClientConfig{
		BotID:   cfg.MezonBotID,
		Token:   cfg.MezonToken,
		Host:    cfg.MezonHost,
		Port:    cfg.MezonPort,
		UseSSL:  &useSSL,
		Timeout: 10 * time.Second,
		Store:   sharedStore,
	}

	client, err := mezon.NewMezonClient(clientCfg)
	if err != nil {
		return nil, fmt.Errorf("create Mezon client: %w", err)
	}

	b := &OmniScanBot{
		client:       client,
		ocrClient:    ocrClient,
		store:        store,
		sessionStore: sessionStore,
		validator:    validator,
		agent:        agent,
		dedup:        dedup,
		cfg:          cfg,
	}

	b.setupHandlers()
	return b, nil
}

func (b *OmniScanBot) setupHandlers() {
	b.client.OnReady(func() {
		log.Printf("==================================================")
		log.Printf("🤖 OmniScan Bot connected successfully!")
		log.Printf("📌 Client ID: %s", b.client.ClientID)
		log.Printf("📌 Host: %s:%s", b.client.Host, b.client.Port)
		log.Printf("📌 Clans Cached: %d", b.client.Clans.Size())
		log.Printf("🧠 AI Agent Endpoint: %s (Model: %s)", b.cfg.LLMBaseURL, b.cfg.LLMModel)
		if b.cfg.RedisURL != "" {
			log.Printf("🚀 Mode: Multi-Replica Horizontal Scaling (Redis active)")
		} else {
			log.Printf("🏠 Mode: Single-Instance Local (SQLite active)")
		}
		log.Printf("==================================================")
	})

	b.client.OnChannelMessage(func(m *mezon.ChannelMessage) {
		if m.SenderID == b.client.ClientID {
			return
		}

		if b.dedup != nil && m.ID != "" {
			acquired, err := b.dedup.TryAcquire(m.ID)
			if err != nil {
				log.Printf("⚠️ Deduplication check error for msg %s: %v", m.ID, err)
			} else if !acquired {
				return
			}
		}

		text := strings.TrimSpace(m.ContentText())
		sender := firstNonEmptyName(m)
		log.Printf("📩 [Msg Received] Channel: %s | Sender: %s (%s) | Content: %s | Attachments: %d | Refs: %d",
			m.ChannelID, sender, m.SenderID, text, len(m.Attachments), len(m.References))

		channel, err := b.client.Channels.Fetch(m.ChannelID)
		if err != nil {
			log.Printf("❌ Failed to fetch channel %s: %v", m.ChannelID, err)
			return
		}

		// Check if message is a Reply / Quote to a previous bot message
		if len(m.References) > 0 && text != "" && !strings.HasPrefix(text, "!") && !strings.HasPrefix(text, "/") {
			refMsgID := m.References[0].MessageID
			sess, err := b.sessionStore.GetSession(refMsgID)
			if err == nil && sess != nil {
				b.handleThreadQuestion(channel, m, sess, text)
				return
			}
		}

		lowerText := strings.ToLower(text)

		// Help command variants
		if lowerText == "!help" || lowerText == "/help" || lowerText == "!omniscan" || lowerText == "!omni" ||
			strings.HasPrefix(lowerText, "!omniscan help") || strings.HasPrefix(lowerText, "!omni help") ||
			strings.HasPrefix(lowerText, "!omniscan -h") || strings.HasPrefix(lowerText, "!omni -h") {
			b.sendReply(channel, m, FormatHelpMessage())
			return
		}

		// Ping command variants
		if lowerText == "!ping" || lowerText == "/ping" ||
			strings.HasPrefix(lowerText, "!omniscan ping") || strings.HasPrefix(lowerText, "!omni ping") {
			b.sendReply(channel, m, "🏓 **Pong!** OmniScan AI Bot đang hoạt động bình thường.")
			return
		}

		// Quota command variants
		if lowerText == "!quota" || lowerText == "/quota" || lowerText == "!me" ||
			strings.HasPrefix(lowerText, "!omniscan quota") || strings.HasPrefix(lowerText, "!omni quota") ||
			strings.HasPrefix(lowerText, "!omniscan me") || strings.HasPrefix(lowerText, "!omni me") {
			scanLimit, _, _ := b.store.GetOrCreateUserConfig(m.SenderID, b.cfg.DailyScanLimit, b.cfg.SessionAskLimit)
			used, remaining, err := b.store.GetQuota(m.SenderID, scanLimit)
			if err != nil {
				log.Printf("❌ Quota lookup error: %v", err)
				b.sendReply(channel, m, "⚠️ Có lỗi xảy ra khi kiểm tra lượt dùng.")
				return
			}
			b.sendReply(channel, m, FormatQuotaMessage(used, scanLimit, remaining))
			return
		}

		isScanCmd := strings.HasPrefix(text, "!scan") || strings.HasPrefix(text, "/scan")
		isOCRCmd := strings.HasPrefix(text, "!ocr") || strings.HasPrefix(text, "/ocr")

		if !isScanCmd && !isOCRCmd && len(m.Attachments) == 0 {
			return
		}

		var targetURL string
		var isAttachment bool
		var attachmentName string
		var attachmentSize int

		if isScanCmd || isOCRCmd {
			parts := strings.Fields(text)
			if len(parts) > 1 {
				targetURL = parts[1]
			}
		}

		if targetURL == "" && len(m.Attachments) > 0 {
			att := m.Attachments[0]
			targetURL = att.URL
			attachmentName = att.Filename
			attachmentSize = att.Size
			isAttachment = true
		}

		if targetURL == "" {
			b.sendReply(channel, m, "⚠️ Vui lòng cung cấp URL ảnh/PDF hoặc đính kèm file cùng câu lệnh `!scan` hoặc `!ocr`!")
			return
		}

		if isAttachment {
			if err := b.validator.ValidateAttachment(attachmentName, attachmentSize); err != nil {
				b.sendReply(channel, m, fmt.Sprintf("🚫 **File không hợp lệ:** %v", err))
				return
			}
		}

		if err := b.validator.ValidateURL(targetURL); err != nil {
			b.sendReply(channel, m, fmt.Sprintf("🚫 **URL không hợp lệ hoặc bị chặn bảo mật:** %v", err))
			return
		}

		// Dynamically fetch user-specific limits from DB (auto-provisions defaults on first encounter)
		scanLimit, _, _ := b.store.GetOrCreateUserConfig(m.SenderID, b.cfg.DailyScanLimit, b.cfg.SessionAskLimit)

		allowed, currentCount, err := b.store.CheckAndIncrementQuota(m.SenderID, scanLimit)
		if err != nil {
			log.Printf("❌ Quota error for user %s: %v", m.SenderID, err)
			b.sendReply(channel, m, "⚠️ Có lỗi xảy ra khi kiểm tra lượt dùng.")
			return
		}

		if !allowed {
			b.sendReply(channel, m, fmt.Sprintf("⚠️ Bạn đã dùng hết **%d/%d** lượt scan miễn phí hôm nay! Vui lòng quay lại vào ngày mai.", scanLimit, scanLimit))
			return
		}

		if isOCRCmd {
			// Raw OCR flow
			log.Printf("🔍 [Raw OCR %d/%d] Processing OCR for %s (URL: %s)", currentCount, scanLimit, sender, targetURL)
			b.sendReply(channel, m, fmt.Sprintf("⏳ Đang bóc tách OCR (Lượt %d/%d), vui lòng chờ...", currentCount, scanLimit))

			go func(chanID string, msg *mezon.ChannelMessage, urlToScan, userID string) {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()

				extractedText, err := b.ocrClient.SubmitAndPoll(ctx, urlToScan)
				if err != nil {
					log.Printf("❌ OCR Error: %v. Refunded quota.", err)
					_ = b.store.RefundQuota(userID)
					b.sendReply(channel, msg, fmt.Sprintf("❌ **Lỗi xử lý OCR:** %v (Đã hoàn 1 lượt)", err))
					return
				}

				reply := FormatOCRReply(extractedText, currentCount, scanLimit)
				b.sendReply(channel, msg, reply)
			}(m.ChannelID, m, targetURL, m.SenderID)
			return
		}

		// Smart AI Agent !scan flow
		log.Printf("🤖 [AI Agent !scan %d/%d] Processing for %s (URL: %s)", currentCount, scanLimit, sender, targetURL)
		b.sendReply(channel, m, fmt.Sprintf("⏳ 🧠 AI Agent đang phân tích & định dạng tài liệu (Lượt %d/%d), vui lòng chờ...", currentCount, scanLimit))

		go func(chanID string, msg *mezon.ChannelMessage, urlToScan, userID string) {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			extractedText, err := b.ocrClient.SubmitAndPoll(ctx, urlToScan)
			if err != nil {
				log.Printf("❌ OCR Error: %v. Refunded quota.", err)
				_ = b.store.RefundQuota(userID)
				b.sendReply(channel, msg, fmt.Sprintf("❌ **Lỗi xử lý OCR:** %v (Đã hoàn 1 lượt)", err))
				return
			}

			// Call LLM Agent for Auto-Classification & Structured Formatting
			res, err := b.agent.ClassifyAndFormat(ctx, extractedText)
			if err != nil {
				log.Printf("⚠️ LLM formatting error: %v. Falling back to raw OCR.", err)
				reply := FormatOCRReply(extractedText, currentCount, scanLimit)
				b.sendReply(channel, msg, reply)
				return
			}

			fullReply := FormatAIReply(res.DocType, res.Formatted, currentCount, scanLimit)

			sentMsg, err := b.sendReply(channel, msg, fullReply)
			if err == nil && sentMsg != nil && sentMsg.ID != "" {
				// Save session for follow-up Q&A
				_ = b.sessionStore.CreateSession(sentMsg.ID, userID, "doc", res.DocType, extractedText)
			}
		}(m.ChannelID, m, targetURL, m.SenderID)
	})
}

func (b *OmniScanBot) handleThreadQuestion(channel *mezon.TextChannel, m *mezon.ChannelMessage, sess *storage.ScanSession, question string) {
	_, askLimit, _ := b.store.GetOrCreateUserConfig(m.SenderID, b.cfg.DailyScanLimit, b.cfg.SessionAskLimit)
	allowed, askCount, err := b.sessionStore.CheckAndIncrementAskQuota(sess.SessionID, askLimit)
	if err != nil {
		log.Printf("❌ Ask quota error: %v", err)
		b.sendReply(channel, m, "⚠️ Có lỗi xảy ra khi kiểm tra số câu hỏi.")
		return
	}

	if !allowed {
		b.sendReply(channel, m, fmt.Sprintf("⚠️ Bạn đã dùng hết **%d/%d** câu hỏi cho tài liệu này! Vui lòng gửi `!scan` tài liệu mới.", askLimit, askLimit))
		return
	}

	b.sendReply(channel, m, fmt.Sprintf("💭 🧠 AI đang suy nghĩ câu trả lời (Câu %d/%d)...", askCount, askLimit))

	go func(msg *mezon.ChannelMessage, s *storage.ScanSession, q string, currentAsk, userAskLimit int) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		answer, err := b.agent.AnswerQuestion(ctx, s.OCRText, q)
		if err != nil {
			log.Printf("❌ Agent Q&A error: %v", err)
			b.sendReply(channel, msg, fmt.Sprintf("❌ **Lỗi AI Agent:** %v", err))
			return
		}

		replyText := fmt.Sprintf("💡 **TRẢ LỜI (Câu %d/%d):**\n%s", currentAsk, userAskLimit, answer)
		sentMsg, err := b.sendReply(channel, msg, replyText)
		if err == nil && sentMsg != nil && sentMsg.ID != "" {
			if currentAsk < userAskLimit {
				_ = b.sessionStore.CreateSession(sentMsg.ID, s.UserID, s.DocumentID, s.DocType, s.OCRText)
			} else {
				_ = b.sessionStore.DeleteSession(s.SessionID)
			}
		}
	}(m, sess, question, askCount, askLimit)
}

func (b *OmniScanBot) Start() error {
	log.Printf("🔌 Connecting OmniScan Bot to Mezon (%s)...", b.cfg.MezonHost)
	return b.client.Login()
}

func (b *OmniScanBot) sendReply(channel *mezon.TextChannel, m *mezon.ChannelMessage, text string) (*mezon.Message, error) {
	handle := "@" + firstNonEmptyName(m)
	fullMessage := fmt.Sprintf("%s %s", handle, text)

	s, e, ok := mezon.MentionSpan(fullMessage, handle)
	var opts *mezon.SendOptions
	if ok {
		opts = &mezon.SendOptions{
			Mentions: []mezon.Mention{{
				UserID:   m.SenderID,
				Username: firstNonEmptyName(m),
				S:        s,
				E:        e,
			}},
		}
	}

	sentMsg, err := channel.Send(mezon.Text(fullMessage), opts)
	if err != nil {
		log.Printf("❌ Failed to send reply to channel %s: %v", m.ChannelID, err)
		return nil, err
	}
	return sentMsg, nil
}

func firstNonEmptyName(m *mezon.ChannelMessage) string {
	for _, v := range []string{m.ClanNick, m.DisplayName, m.Username} {
		if v != "" {
			return v
		}
	}
	return m.SenderID
}
