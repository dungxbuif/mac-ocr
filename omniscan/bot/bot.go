package bot

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	mezon "mezon-bot-sdk"

	"omniscan/agent"
	"omniscan/config"
	ocrlib "omniscan/ocr"
	"omniscan/security"
	"omniscan/storage"
)

type OmniScanBot struct {
	client       *mezon.MezonClient
	ocrClient    *ocrlib.Client
	store        storage.QuotaStore
	sessionStore storage.SessionStore
	validator    *security.Validator
	agent        *agent.Agent
	dedup        storage.Deduplicator
	cfg          *config.Config
	pgQuota      *storage.PostgresQuotaStore // nil when not on PostgreSQL
	limits       *userLimitCache             // TTL cache for per-user limits
}

func New(cfg *config.Config, ocrClient *ocrlib.Client, store storage.QuotaStore, sessionStore storage.SessionStore, validator *security.Validator, agent *agent.Agent, dedup storage.Deduplicator, sharedStore mezon.SharedStore, pgQuota *storage.PostgresQuotaStore) (*OmniScanBot, error) {
	useSSL := true
	clientCfg := mezon.ClientConfig{
		BotID:   cfg.MezonBotID,
		Token:   cfg.MezonToken,
		Host:    cfg.MezonHost,
		Port:    cfg.MezonPort,
		UseSSL:  &useSSL,
		Timeout: cfg.MezonClientTimeout,
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
		limits:       newUserLimitCache(cfg.UserLimitCacheTTL),
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
			var sess *storage.ScanSession
			var foundRefID string
			for _, ref := range m.References {
				for _, candidateID := range []string{ref.MessageRefID, ref.MessageID} {
					if candidateID == "" {
						continue
					}
					s, err := b.sessionStore.GetSession(candidateID)
					if err == nil && s != nil {
						sess = s
						foundRefID = candidateID
						break
					}
				}
				if sess != nil {
					break
				}
			}

			if sess != nil {
				log.Printf("🧵 [Reply] found active Q&A session %s for user %s (%s)", foundRefID, sender, m.SenderID)
				b.handleThreadQuestion(channel, m, sess, text)
				return
			}

			refMsgID := m.RefMessageID()
			log.Printf("📩 [Reply] no session for ref %s — not a scan result", refMsgID)
			b.sendReply(channel, m, "⚠️ Tin nhắn này không có phiên hỏi đáp. Hãy `*scan` hoặc `*ocr` một tài liệu mới, rồi **reply** tin nhắn kết quả để hỏi AI.")
			return
		}

		lowerText := strings.ToLower(text)

		// Help command. Trigger on bare "*os" or "*omniscan" — short, memorable,
		// and distinct from other bots' *help. No "help" subword needed.
		if lowerText == "*os" || lowerText == "*omniscan" {
			b.sendReplyContent(channel, m, BuildHelpContent(b.cfg.DailyScanLimit, b.cfg.DailyOCRLimit, b.cfg.SessionAskLimit))
			return
		}

		// Quota command
		if lowerText == "*quota" || lowerText == "*me" {
			scanLimit, ocrLimit, askLimit := b.getUserLimits(m.SenderID)
			scanUsed, scanRem, ocrUsed, ocrRem, err := b.store.GetQuota(m.SenderID, scanLimit, ocrLimit)
			if err != nil {
				log.Printf("❌ Quota lookup error: %v", err)
				b.sendReply(channel, m, "⚠️ Có lỗi xảy ra khi kiểm tra lượt dùng.")
				return
			}
			b.sendReply(channel, m, FormatQuotaMessage(scanUsed, scanLimit, scanRem, ocrUsed, ocrLimit, ocrRem, askLimit))
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
		var customPrompt string

		if isScanCmd {
			// *scan ["custom prompt"] [url]
			rest := strings.TrimSpace(strings.TrimPrefix(text, "*scan"))
			args := ParseScanArgs(rest)
			customPrompt = args.CustomPrompt
			targetURL = args.URL
		} else if isOCRCmd {
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
		scanLimit, ocrLimit, _ := b.getUserLimits(m.SenderID)

		var allowed bool = true
		var currentCount int = 1
		if !isUnlimitedUser(m.SenderID) {
			if isOCRCmd {
				allowed, currentCount, err = b.store.CheckAndIncrementOCRQuota(m.SenderID, ocrLimit)
			} else {
				allowed, currentCount, err = b.store.CheckAndIncrementScanQuota(m.SenderID, scanLimit)
			}
			if err != nil {
				log.Printf("❌ Quota error for user %s: %v", m.SenderID, err)
				b.sendReply(channel, m, "⚠️ Có lỗi xảy ra khi kiểm tra lượt dùng.")
				return
			}

			if !allowed {
				var limitLabel string
				if isOCRCmd {
					limitLabel = fmt.Sprintf("⚠️ Bạn đã dùng hết **%d/%d** lượt OCR miễn phí hôm nay! Vui lòng quay lại vào ngày mai.", currentCount, ocrLimit)
				} else {
					limitLabel = fmt.Sprintf("⚠️ Bạn đã dùng hết **%d/%d** lượt scan miễn phí hôm nay! Vui lòng quay lại vào ngày mai.", currentCount, scanLimit)
				}
				b.sendReply(channel, m, limitLabel)
				return
			}
		}

		if isOCRCmd {
			// Raw OCR flow — uses SubmitAndPollFull so we get bbox blocks for 2D layout
			log.Printf("🔍 [Raw OCR %d/%d] Processing for %s (URL: %s)", currentCount, ocrLimit, sender, targetURL)
			b.sendReply(channel, m, fmt.Sprintf("⏳ Đang bóc tách OCR (Lượt %d/%d), vui lòng chờ...", currentCount, ocrLimit))

			go func(msg *mezon.ChannelMessage, urlToScan, userID string, asAttachment bool) {
				ctx, cancel := context.WithTimeout(context.Background(), b.cfg.OCRProcessTimeout)
				defer cancel()

				result, err := b.submitOCRFull(ctx, urlToScan, asAttachment)
				if err != nil {
					log.Printf("❌ OCR Error: %v. Refunded quota.", err)
					_ = b.store.RefundOCRQuota(userID)
					b.sendReply(channel, msg, fmt.Sprintf("❌ **Lỗi xử lý OCR:** %v (Đã hoàn 1 lượt)", err))
					return
				}

				reconstructed := ocrlib.ReconstructLayout(result)
				out := BuildOCRResult(result, reconstructed, currentCount, ocrLimit)

				// Deliver embed card
				sentMsg, sendErr := b.sendReplyContent(channel, msg, out.Content)

				// Save session for optional follow-up Q&A on the OCR result
				if sendErr == nil && sentMsg != nil && sentMsg.ID != "" {
					_ = b.sessionStore.CreateSession(sentMsg.ID, userID, "doc", "Raw OCR", reconstructed)
				}
				if msg.ID != "" {
					_ = b.sessionStore.CreateSession(msg.ID, userID, "doc", "Raw OCR", reconstructed)
				}
			}(m, targetURL, m.SenderID, isAttachment)
			return
		}

		// Smart AI Agent *scan flow.
		if !b.agent.IsHealthy() {
			log.Printf("⚠️ [*scan] LLM endpoint down — failing fast.")
			b.sendReply(channel, m, "🔴 **AI Agent chưa sẵn sàng** — `*ocr` vẫn dùng bình thường. Vui lòng báo admin kiểm tra LLM endpoint.")
			return
		}

		promptHint := ""
		if customPrompt != "" {
			promptHint = fmt.Sprintf(" (prompt: \"%s\")", customPrompt)
		}
		log.Printf("🤖 [AI *scan %d/%d] Processing for %s (URL: %s)%s", currentCount, scanLimit, sender, targetURL, promptHint)
		b.sendReply(channel, m, fmt.Sprintf("⏳ 🧠 AI đang phân tích tài liệu (Lượt %d/%d)%s, vui lòng chờ...", currentCount, scanLimit, promptHint))

		go func(msg *mezon.ChannelMessage, urlToScan, userID, prompt string, asAttachment bool) {
			ctx, cancel := context.WithTimeout(context.Background(), b.cfg.ScanProcessTimeout)
			defer cancel()

			// Full result for bbox reconstruction
			result, err := b.submitOCRFull(ctx, urlToScan, asAttachment)
			if err != nil {
				log.Printf("❌ OCR Error: %v. Refunded quota.", err)
				_ = b.store.RefundScanQuota(userID)
				b.sendReply(channel, msg, fmt.Sprintf("❌ **Lỗi xử lý OCR:** %v (Đã hoàn 1 lượt)", err))
				return
			}
			reconstructed := ocrlib.ReconstructLayout(result)

			_, _, askLimit := b.getUserLimits(userID)

			// Call LLM with optional custom prompt
			res, err := b.agent.ClassifyAndFormat(ctx, reconstructed, prompt)
			if err != nil {
				log.Printf("⚠️ LLM error: %v. Fallback to raw OCR embed.", err)
				out := BuildOCRResult(result, reconstructed, currentCount, scanLimit)
				sentFallback, _ := b.sendReplyContent(channel, msg, out.Content)
				if sentFallback != nil && sentFallback.ID != "" {
					_ = b.sessionStore.CreateSession(sentFallback.ID, userID, "doc", "Raw OCR", reconstructed)
				}
				return
			}

			out := BuildScanResult(res.DocType, res.Formatted, currentCount, scanLimit, askLimit)
			sentMsg, sendErr := b.sendReplyContent(channel, msg, out.Content)

			if sendErr == nil && sentMsg != nil && sentMsg.ID != "" {
				_ = b.sessionStore.CreateSession(sentMsg.ID, userID, "doc", res.DocType, reconstructed)
			}
			if msg.ID != "" {
				_ = b.sessionStore.CreateSession(msg.ID, userID, "doc", res.DocType, reconstructed)
			}
		}(m, targetURL, m.SenderID, customPrompt, isAttachment)
	})
}

func (b *OmniScanBot) handleThreadQuestion(channel *mezon.TextChannel, m *mezon.ChannelMessage, sess *storage.ScanSession, question string) {
	_, _, askLimit := b.getUserLimits(m.SenderID)

	var allowed bool = true
	var askCount int = 1
	var err error
	if !isUnlimitedUser(m.SenderID) {
		allowed, askCount, err = b.sessionStore.CheckAndIncrementAskQuota(sess.SessionID, m.SenderID, askLimit)
		if err != nil {
			log.Printf("❌ Ask quota error: %v", err)
			b.sendReply(channel, m, "⚠️ Có lỗi xảy ra khi kiểm tra số câu hỏi.")
			return
		}

		if !allowed {
			b.sendReply(channel, m, fmt.Sprintf("⚠️ Bạn đã dùng hết **%d/%d** câu hỏi cho tài liệu này! Vui lòng gửi `*scan` tài liệu mới.", askLimit, askLimit))
			return
		}
	}

	thinkingMsg, _ := b.sendReply(channel, m, fmt.Sprintf("💭 🧠 AI đang suy nghĩ câu trả lời (Câu %d/%d)...", askCount, askLimit))
	if thinkingMsg != nil && thinkingMsg.ID != "" {
		_ = b.sessionStore.CreateSession(thinkingMsg.ID, sess.UserID, sess.DocumentID, sess.DocType, sess.OCRText)
	}
	if m.ID != "" {
		_ = b.sessionStore.CreateSession(m.ID, sess.UserID, sess.DocumentID, sess.DocType, sess.OCRText)
	}

	go func(msg *mezon.ChannelMessage, s *storage.ScanSession, q string, currentAsk, userAskLimit int) {
		ctx, cancel := context.WithTimeout(context.Background(), b.cfg.QATimeout)
		defer cancel()

		var history []agent.QAPair
		for _, h := range s.History {
			history = append(history, agent.QAPair{Question: h.Question, Answer: h.Answer})
		}

		answer, err := b.agent.AnswerQuestion(ctx, s.OCRText, history, q)
		if err != nil {
			log.Printf("❌ Agent Q&A error: %v", err)
			b.sendReply(channel, msg, fmt.Sprintf("❌ **Lỗi AI Agent:** %v", err))
			return
		}

		_ = b.sessionStore.AppendQAHistory(s.SessionID, q, answer)

		replyText := fmt.Sprintf("💡 **TRẢ LỜI (Câu %d/%d):**\n%s", currentAsk, userAskLimit, answer)
		sentMsg, err := b.sendReply(channel, msg, replyText)
		if err == nil && sentMsg != nil && sentMsg.ID != "" {
			_ = b.sessionStore.CreateSession(sentMsg.ID, s.UserID, s.DocumentID, s.DocType, s.OCRText)
			_ = b.sessionStore.AppendQAHistory(sentMsg.ID, q, answer)
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
		scanLimit, ocrLimit, askLimit := b.getUserLimits(e.SenderID)
		scanUsed, scanRem, ocrUsed, ocrRem, qerr := b.store.GetQuota(e.SenderID, scanLimit, ocrLimit)
		if qerr != nil {
			b.sendText(channel, e.SenderID, "⚠️ Có lỗi xảy ra khi kiểm tra lượt dùng.")
			return
		}
		b.sendText(channel, e.SenderID, FormatQuotaMessage(scanUsed, scanLimit, scanRem, ocrUsed, ocrLimit, ocrRem, askLimit))
	case "omniscan_scan_detail":
		b.handleScanDetailButton(channel, e)
	case "omniscan_scan_more":
		b.handleScanMoreButton(channel, e)
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
// a leading mention of the sender. Returns the sent message and any error.
func (b *OmniScanBot) sendReplyContent(channel *mezon.TextChannel, m *mezon.ChannelMessage, content mezon.Content) (*mezon.Message, error) {
	handle := "@" + firstNonEmptyName(m)
	mention, ok := mezon.MentionUser(handle, m.SenderID, firstNonEmptyName(m))
	var opts *mezon.SendOptions
	if ok {
		opts = &mezon.SendOptions{Mentions: []mezon.Mention{mention}}
	}
	sentMsg, err := channel.Send(content, opts)
	if err != nil {
		log.Printf("❌ sendReplyContent to %s: %v", channel.ID, err)
		return nil, err
	}
	return sentMsg, nil
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
	if err := b.pgQuota.UpsertUser(ctx, userID, displayName, username, clanNick, b.cfg.DailyScanLimit, b.cfg.DailyOCRLimit, b.cfg.SessionAskLimit); err != nil {
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

// sendFileAttachment sends fileBytes as a file or inline code-block fallback.
// Mezon's current SDK does not expose a file-upload API, so we always fall back
// to sending the content inline (truncated to 3 000 runes to stay within limits).
func (b *OmniScanBot) sendFileAttachment(channel *mezon.TextChannel, _ *mezon.ChannelMessage, filename string, data []byte, caption string) (*mezon.Message, error) {
	if len(data) == 0 {
		return nil, nil
	}

	runes := []rune(string(data))
	truncated := false
	if len(runes) > 3000 {
		runes = runes[:3000]
		truncated = true
	}

	note := ""
	if truncated {
		note = fmt.Sprintf("\n*(Cắt ngắn — đầy đủ %d ký tự trong file `%s`)*", len([]rune(string(data))), filename)
	}

	body := fmt.Sprintf("%s\n```\n%s\n```%s", caption, string(runes), note)
	return channel.Send(mezon.Text(body), nil)
}

// downloadAttachmentBytes fetches the attachment URL on the bot host (same
// network that received the Mezon event), so it succeeds even when the OCR
// proxy host's network cannot reach cdn.komu.vn. It caps the download at the
// same 100 MiB the validator allows for attachments. Mezon CDN may return a
// transient 404 (edge node not synced yet at the instant the bot receives the
// upload event), so the download is retried with backoff.
func (b *OmniScanBot) downloadAttachmentBytes(ctx context.Context, url string) ([]byte, error) {
	maxBytes := b.cfg.MaxAttachmentBytes
	backoffs := []time.Duration{2 * time.Second, 3 * time.Second, 5 * time.Second, 8 * time.Second, 12 * time.Second}

	var lastErr error
	for attempt := 0; attempt <= len(backoffs); attempt++ {
		if attempt > 0 {
			log.Printf("⏳ [download] retry %d after %v for %s", attempt, backoffs[attempt-1], url)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoffs[attempt-1]):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "OmniScan-Bot/1.0")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			lastErr = fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
			// Transient CDN errors are worth retrying; 4xx other than 404 are not.
			if resp.StatusCode != 404 && resp.StatusCode != 408 && resp.StatusCode != 429 && resp.StatusCode < 500 {
				return nil, lastErr
			}
			continue
		}

		r := io.LimitReader(resp.Body, int64(maxBytes)+1)
		data, err := io.ReadAll(r)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(data) > maxBytes {
			return nil, fmt.Errorf("download %s: exceeds %d bytes limit", url, maxBytes)
		}
		if attempt > 0 {
			log.Printf("✅ [download] succeeded on retry %d", attempt)
		}
		return data, nil
	}
	return nil, lastErr
}

// submitOCRFull dispatches an OCR submit + poll, choosing the URL or base64
// path. For Mezon attachments the url points at cdn.komu.vn behind Cloudflare;
// the proxy host sometimes cannot fetch it (404 / geo-block), so the bot
// downloads the attachment itself and submits the raw bytes as base64,
// bypassing the proxy's URL fetcher entirely.
func (b *OmniScanBot) submitOCRFull(ctx context.Context, urlToScan string, asAttachment bool) (*ocrlib.ResultPayload, error) {
	start := time.Now()
	if !asAttachment {
		res, err := b.ocrClient.SubmitAndPollFull(ctx, urlToScan)
		latency := time.Since(start)
		if err != nil {
			log.Printf("❌ [OCR-PIPELINE] mode=url url=%s latency=%v error=%v", urlToScan, latency, err)
			return nil, err
		}
		log.Printf("✅ [OCR-PIPELINE] mode=url latency=%v pages=%d text_len=%d", latency, res.PageCount, len(res.Text))
		return res, nil
	}

	dlStart := time.Now()
	data, err := b.downloadAttachmentBytes(ctx, urlToScan)
	dlLatency := time.Since(dlStart)
	if err != nil {
		log.Printf("❌ [OCR-DOWNLOAD] url=%s latency=%v error=%v", urlToScan, dlLatency, err)
		return nil, fmt.Errorf("download attachment: %w", err)
	}
	log.Printf("✅ [OCR-DOWNLOAD] bytes=%d latency=%v", len(data), dlLatency)

	ocrStart := time.Now()
	res, err := b.ocrClient.SubmitAndPollBase64Full(ctx, data)
	ocrLatency := time.Since(ocrStart)
	if err != nil {
		log.Printf("❌ [OCR-PIPELINE] mode=base64 latency=%v error=%v", ocrLatency, err)
		return nil, err
	}
	log.Printf("✅ [OCR-PIPELINE] mode=base64 total_latency=%v (dl=%v ocr=%v) pages=%d text_len=%d",
		time.Since(start), dlLatency, ocrLatency, res.PageCount, len(res.Text))
	return res, nil
}
