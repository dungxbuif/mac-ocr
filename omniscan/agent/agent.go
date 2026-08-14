package agent

import (
	"context"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

type Agent struct {
	client *openai.Client
	model  string
}

type ClassifyResult struct {
	DocType   string
	Formatted string
}

func New(baseURL, apiKey, model string) *Agent {
	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = strings.TrimRight(baseURL, "/")
		if !strings.HasSuffix(cfg.BaseURL, "/v1") {
			cfg.BaseURL += "/v1"
		}
	}

	return &Agent{
		client: openai.NewClientWithConfig(cfg),
		model:  model,
	}
}

// Health pings the LLM endpoint to confirm it is reachable and the API key is
// valid. It calls GET /v1/models (no tokens generated), so it is cheap and
// works against any OpenAI-compatible server (LM Studio, vLLM, OpenAI, ...).
// Call at bot startup so the operator sees immediately whether the *scan AI
// flow will work; the *ocr raw flow does not use the LLM and is unaffected.
func (a *Agent) Health(ctx context.Context) error {
	_, err := a.client.ListModels(ctx)
	return err
}

// healthy is the cached result of the startup health check, read by the bot
// to decide whether *scan should attempt the AI call or fail fast with a
// clear message. It is set once at startup; a later recovery is not detected
// automatically, which is acceptable for a dev/internal bot.
var healthy bool

// SetHealth records the startup LLM probe result so the bot can tailor its
// *scan error message instead of letting the first real call time out.
func (a *Agent) SetHealth(ok bool) { healthy = ok }

// IsHealthy reports whether the startup LLM probe succeeded.
func (a *Agent) IsHealthy() bool { return healthy }

func (a *Agent) ClassifyAndFormat(ctx context.Context, ocrText string) (*ClassifyResult, error) {
	systemPrompt := `Bạn là AI Agent trợ lý phân tích tài liệu chuyên nghiệp. 
Nhiệm vụ của bạn:
1. Đọc văn bản OCR được cung cấp.
2. Tự động nhận diện loại tài liệu:
   - Hóa đơn / Biên lai (Invoice)
   - Giấy tờ cá nhân / CCCD / Danh thiếp (ID Card / Business Card)
   - Hợp đồng / Chứng từ pháp lý (Contract)
   - Tài liệu kỹ thuật / Bài viết / Khác (General Document)
3. Chỉnh sửa lỗi chính tả OCR, nối dòng văn bản bị ngắt quãng, loại bỏ rác.
4. Trình bày lại kết quả dưới dạng Markdown đẹp mắt:
   - Với Hóa đơn/CCCD: Dùng bảng Markdown (Table) trích xuất rõ các trường thông tin (Tổng tiền, Ngày, Mã số, Danh sách hàng...).
   - Với Hợp đồng: Tóm tắt các điều khoản chính, số tiền, mốc thời gian và điểm lưu ý quan trọng.
   - Với Tài liệu chung: Định dạng bài viết chuẩn với tiêu đề (#, ##) và các mục rõ ràng.

Hãy bắt đầu bằng dòng đầu tiên chỉ ghi tên loại tài liệu trong ngoặc vuông, ví dụ: [Hóa đơn] hoặc [Hợp đồng] hoặc [CCCD] hoặc [Tài liệu chung], sau đó trình bày chi tiết.`

	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: a.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: "Dưới đây là văn bản OCR cần bóc tách và phân loại:\n\n" + ocrText},
		},
		Temperature: 0.2,
	})

	if err != nil {
		return nil, fmt.Errorf("LLM completion error: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("empty response from LLM agent")
	}

	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	docType := "Tài liệu"

	// Parse category tag from first line if present
	if strings.HasPrefix(content, "[") {
		idx := strings.Index(content, "]")
		if idx > 1 {
			docType = content[1:idx]
			content = strings.TrimSpace(content[idx+1:])
		}
	}

	return &ClassifyResult{
		DocType:   docType,
		Formatted: content,
	}, nil
}

func (a *Agent) AnswerQuestion(ctx context.Context, ocrText, question string) (string, error) {
	systemPrompt := `Bạn là AI Agent trợ lý hỏi đáp tài liệu. 
Dựa TRỰC TIẾP và CHÍNH XÁC vào nội dung tài liệu OCR được cung cấp dưới đây để trả lời câu hỏi của người dùng.
- Trả lời ngắn gọn, chính xác, nêu rõ căn cứ trong văn bản (nếu có).
- Nếu thông tin không có trong tài liệu, hãy trả lời rõ: "Thông tin này không xuất hiện trong tài liệu được cung cấp."`

	userMessage := fmt.Sprintf("📄 NỘI DUNG TÀI LIỆU OCR:\n```\n%s\n```\n\n❓ CÂU HỎI CỦA NGƯỜI DÙNG: %s", ocrText, question)

	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: a.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userMessage},
		},
		Temperature: 0.3,
	})

	if err != nil {
		return "", fmt.Errorf("LLM completion error: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty response from LLM agent")
	}

	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}
