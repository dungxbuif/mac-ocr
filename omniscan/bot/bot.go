package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	mezon "mezon-bot-sdk"

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
	pgQuota      *storage.PostgresQuotaStore // nil when not on PostgreSQL
}

func New(cfg *config.Config, ocrClient *ocr.Client, store storage.QuotaStore, sessionStore storage.SessionStore, validator *security.Validator, agent *agent.Agent, dedup storage.Deduplicator, sharedStore mezon.SharedStore, pgQuota *storage.PostgresQuotaStore) (*OmniScanBot, error) {
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
		pgQuota:      pgQuota,
	}

	b.setupHandlers()
	return b, nil
}

func (b *OmniScanBot) setupHandlers() {
	b.client.OnMessageButtonClicked(func(e *mezon.MessageButtonClick) {
		b.handleButtonClicked(e)
	})

	b.client.OnReady(func() {
		log.Printf("==================================================")
		log.Printf("🤖 OmniScan Bot connected successfully!")
		log.Printf("📌 Client ID: %s", b.client.ClientID)
		log.Printf("📌 Host: %s:%s", b.client.Host, b.client.Port)
		log.Printf("📌 Clans Cached: %d", b.client.Clans.Size())
		log.Printf("🧠 AI Agent Endpoint: %s (Model: %s)", b.cfg.LLMBaseURL, b.cfg.LLMModel)
		if b.cfg.RedisURL != "" {
			log.Printf("🚀 Mode: Multi-Replica (Redis dedup/L2 active)")
		} else {
			log.Printf("🏠 Mode: Single-Instance Local")
		}
		if b.pgQuota != nil {
			log.Printf("🗄️ Quota/Session store: PostgreSQL (event-driven user upsert ON)")
		} else {
			log.Printf("📦 Quota/Session store: SQLite")
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

		// Event-driven upsert: record this user in PostgreSQL so their per-user
		// limit row exists before CheckAndIncrementQuota, and so the admin can
		// audit/tune limits per user. Fire-and-forget; failures are logged but
		// never block the message path.
		b.upsertSeenUser(m.SenderID, m.DisplayName, m.Username, m.ClanNick)

		channel, err := b.client.Channels.Fetch(m.ChannelID)
		if err != nil {
			log.Printf("❌ Failed to fetch channel %s: %v", m.ChannelID, err)
			return
		}

		// Check if message is a Reply / Quote to a previous bot message. A
		// leading command prefix ("*") marks a real command, not a follow-up
		// question, so route it to the command dispatcher instead.
		if len(m.References) > 0 && text != "" && !strings.HasPrefix(text, "*") {
			refMsgID := m.References[0].MessageID
			sess, err := b.sessionStore.GetSession(refMsgID)
			if err == nil && sess != nil {
				b.handleThreadQuestion(channel, m, sess, text)
				return
			}
		}

		lowerText := strings.ToLower(text)

		// Help command: prefix-independent name to avoid clashing with other
		// bots that use the common *help. Trigger on bare "*os" or "*os help".
		if lowerText == "*os" || lowerText == "*os help" || lowerText == "*os -h" {
			b.sendReplyContent(channel, m, BuildHelpContent(b.cfg.DailyScanLimit, b.cfg.SessionAskLimit))
			return
		}

		// Quota command
		if lowerText == "*quota" || lowerText == "*me" {
			scanLimit, askLimit, _ := b.store.GetOrCreateUserConfig(m.SenderID, b.cfg.DailyScanLimit, b.cfg.SessionAskLimit)
			used, remaining, err := b.store.GetQuota(m.SenderID, scanLimit)
			if err != nil {
				log.Printf("❌ Quota lookup error: %v", err)
				b.sendReply(channel, m, "⚠️ Có lỗi xảy ra khi kiểm tra lượt dùng.")
				return
			}
			b.sendReply(channel, m, FormatQuotaMessage(used, scanLimit, remaining, askLimit))
			return
		}

		isScanCmd := strings.HasPrefix(text, "*scan")
		isOCRCmd := strings.HasPrefix(text, "*ocr")

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
			b.sendReply(channel, m, "⚠️ Vui lòng cung cấp URL ảnh/PDF hoặc đính kèm file cùng câu lệnh `*scan` hoặc `*ocr`!")
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

		// Smart AI Agent *scan flow.
		if !b.agent.IsHealthy() {
			log.Printf("⚠️ [*scan] LLM endpoint down — failing fast instead of letting the call time out.")
			b.sendReply(channel, m, "🔴 **AI Agent chưa sẵn sàng** — server model LLM không kết nối được lúc khởi động, nên luồng `*scan` (AI) đang tắt.\\nBạn vẫn dùng `*ocr` (bóc tách Raw OCR) bình thường. Vui lòng báo admin kiểm tra LLM endpoint.")
			return
		}
		log.Printf("🤖 [AI Agent *scan %d/%d] Processing for %s (URL: %s)", currentCount, scanLimit, sender, targetURL)
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
		b.sendReply(channel, m, fmt.Sprintf("⚠️ Bạn đã dùng hết **%d/%d** câu hỏi cho tài liệu này! Vui lòng gửi `*scan` tài liệu mới.", askLimit, askLimit))
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

// handleButtonClicked reacts to the interactive buttons attached to the help
// message. It fetches the channel from the event payload, looks the sender up
// (it is NOT a ChannelMessage, so we cannot reuse sendReply's mention helper
// directly) and answers with the button's command output.
func (b *OmniScanBot) handleButtonClicked(e *mezon.MessageButtonClick) {
	if e == nil || e.ChannelID == "" || e.SenderID == "" {
		return
	}
	// Event-driven upsert: a click is also a sighting of this user.
	b.upsertSeenUser(e.SenderID, "", "", "")
	channel, err := b.client.Channels.Fetch(e.ChannelID)
	if err != nil {
		log.Printf("❌ button %s: fetch channel %s: %v", e.ButtonID, e.ChannelID, err)
		return
	}
	switch e.ButtonID {
	case "omniscan_quota":
		scanLimit, askLimit, _ := b.store.GetOrCreateUserConfig(e.SenderID, b.cfg.DailyScanLimit, b.cfg.SessionAskLimit)
		used, remaining, qerr := b.store.GetQuota(e.SenderID, scanLimit)
		if qerr != nil {
			b.sendText(channel, e.SenderID, "⚠️ Có lỗi xảy ra khi kiểm tra lượt dùng.")
			return
		}
		b.sendText(channel, e.SenderID, FormatQuotaMessage(used, scanLimit, remaining, askLimit))
	case "omniscan_scan_hint":
		b.sendText(channel, e.SenderID, "💡 Gửi `*scan <url>` hoặc đính kèm ảnh/PDF kèm `*scan`. AI sẽ tự nhận diện & định dạng Markdown.")
	default:
		log.Printf("📩 unknown button click: %s from %s", e.ButtonID, e.SenderID)
	}
}

// sendText posts a text message mentioning userID. Used by button-click
// handlers where there is no source ChannelMessage to reply to.
func (b *OmniScanBot) sendText(channel *mezon.TextChannel, userID, text string) {
	handle := "@" + userID
	reply := handle + " " + text
	if m, ok := mezon.MentionUser(reply, userID, userID); ok {
		if _, err := channel.Send(mezon.Text(reply), &mezon.SendOptions{Mentions: []mezon.Mention{m}}); err != nil {
			log.Printf("❌ sendText to %s: %v", channel.ID, err)
		}
		return
	}
	if _, err := channel.Send(mezon.Text(reply), nil); err != nil {
		log.Printf("❌ sendText to %s: %v", channel.ID, err)
	}
}

// sendReplyContent sends an arbitrary Content (e.g. an interactive embed) with
// a leading mention of the sender, used for the help card.
func (b *OmniScanBot) sendReplyContent(channel *mezon.TextChannel, m *mezon.ChannelMessage, content mezon.Content) {
	handle := "@" + firstNonEmptyName(m)
	mention, ok := mezon.MentionUser(handle, m.SenderID, firstNonEmptyName(m))
	// Prepend the handle to the plain-text portion so the chip renders. We bake
	// the mention into a leading text node by wrapping: many Mezon clients show
	// the embed's author/footer instead, so keep the text short.
	if ok {
		if _, err := channel.Send(content, &mezon.SendOptions{Mentions: []mezon.Mention{mention}}); err != nil {
			log.Printf("❌ sendReplyContent to %s: %v", channel.ID, err)
		}
		return
	}
	if _, err := channel.Send(content, nil); err != nil {
		log.Printf("❌ sendReplyContent to %s: %v", channel.ID, err)
	}
}

// upsertSeenUser records (or refreshes) a user in PostgreSQL when the bot sees
// them, seeding their per-user quota limits from the config defaults. It is a
// no-op when not running on PostgreSQL. Runs on a short timeout and never
// returns an error: the quota path still self-heals via GetOrCreateUserConfig,
// so a transient DB blip must never block a reply.
func (b *OmniScanBot) upsertSeenUser(userID, displayName, username, clanNick string) {
	if b.pgQuota == nil || userID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := b.pgQuota.UpsertUser(ctx, userID, displayName, username, clanNick, b.cfg.DailyScanLimit, b.cfg.SessionAskLimit); err != nil {
		log.Printf("⚠️ UpsertUser %s: %v", userID, err)
	}
}

func (b *OmniScanBot) Start() error {
	log.Printf("🔌 Connecting OmniScan Bot to Mezon (%s)...", b.cfg.MezonHost)
	return b.client.Login()
}

func (b *OmniScanBot) sendReply(channel *mezon.TextChannel, m *mezon.ChannelMessage, text string) (*mezon.Message, error) {
	handle := "@" + firstNonEmptyName(m)
	fullMessage := fmt.Sprintf("%s %s", handle, text)

	mention, ok := mezon.MentionUser(fullMessage, m.SenderID, firstNonEmptyName(m))
	var opts *mezon.SendOptions
	if ok {
		opts = &mezon.SendOptions{Mentions: []mezon.Mention{mention}}
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
