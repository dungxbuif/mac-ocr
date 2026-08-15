package agent

import (
	"context"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

type Agent struct {
	client        *openai.Client
	model         string
	scanTemperature float32
	qaTemperature   float32
}

type ClassifyResult struct {
	DocType   string
	Formatted string
}

// New builds an LLM agent. scanTemp and qaTemp are the sampling temperatures for
// the *scan classify/format call and the Q&A call respectively, sourced from env
// (LLM_SCAN_TEMPERATURE, LLM_QA_TEMPERATURE) so they can be tuned per deployment.
func New(baseURL, apiKey, model string, scanTemp, qaTemp float32) *Agent {
	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = strings.TrimRight(baseURL, "/")
		if !strings.HasSuffix(cfg.BaseURL, "/v1") {
			cfg.BaseURL += "/v1"
		}
	}

	return &Agent{
		client:          openai.NewClientWithConfig(cfg),
		model:           model,
		scanTemperature: scanTemp,
		qaTemperature:   qaTemp,
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

// ClassifyAndFormat analyses ocrText and returns a structured, markdown-formatted
// result. When customPrompt is non-empty it is appended to the system instructions
// so the user can steer the output (e.g. "dịch ra tiếng Anh", "chỉ lấy số tiền").
func (a *Agent) ClassifyAndFormat(ctx context.Context, ocrText, customPrompt string) (*ClassifyResult, error) {
	systemPrompt := `Bạn là OmniScan — trợ lý AI phân tích tài liệu chuyên nghiệp.
Nhiệm vụ KHÔNG phải copy lại văn bản OCR, mà là ĐỌC HIỂU, trích xuất thông tin trọng yếu và phân tích rủi ro/hành động.

BƯỚC 1 — Nhận diện loại tài liệu, ghi ở dòng ĐẦU TIÊN chỉ tên trong ngoặc vuông, một trong:
[Hóa đơn] | [CCCD/Danh thiếp] | [Hợp đồng] | [Tài liệu kỹ thuật] | [Bài viết] | [Bảng biểu] | [Khác]

BƯỚC 2 — Trình bày kết quả theo ĐÚNG 4 PHẦN với tiêu đề Markdown (bắt buộc, không bỏ phần nào, giữ thứ tự):

## 📌 Tóm tắt nhanh
2-4 câu ngắn gọn (TL;DR): tài liệu là gì, bên nào với bên nào, trị giá/mục đích, thời hạn cốt lõi. Người đọc chỉ cần 3 giây để nắm bức tranh tổng quát.

## 📊 Thông tin quan trọng
Bảng Markdown (| Trường | Giá trị |) liệt kê mọi thực thể/dữ liệu chính: bên A, bên B, MST, số tiền, ngày ký, ngày hiệu lực, đợt thanh toán, ký hạn, điều khoản then chốt. Với CCCD/danh thiếp: họ tên, ngày sinh, số CMND/CCCD, nơi cấp, giới hạn. Với bài viết/bảng biểu: tiêu đề + các điểm số liệu chính.

## ⚠️ Điểm cần lưu ý & Rủi ro
Liệt kê (dùng "- ") các điểm cần chú ý: điều khoản phạt, đơn phương chấm dứt, bảo hành, bảo mật, tính hợp lệ, mâu thuẫn số liệu, rủi ro pháp lý/tài chính. Nếu KHÔNG có rủi ro rõ rệt, ghi: "Không phát hiện rủi ro nổi bật ngoài các điều khoản tiêu chuẩn."

## 💡 Bạn có thể hỏi thêm
Đưa ra đúng 3 câu hỏi gợi ý (đánh số 1. 2. 3.), gắn trực tiếp với nội dung tài liệu để giúp người dùng khai thác nhanh. Ví dụ: "Bên A có quyền đơn phương hủy hợp đồng không?", "Tổng tiền đã thanh toán theo hợp đồng là bao nhiêu?"

NGUYÊN TẮC:
- KHÔNG copy lại toàn bộ nội dung — trích xuất và phân tích.
- Sửa lỗi chính tả OCR, nối dòng bị ngắt, bỏ rác/ký tự thừa.
- Viết bằng tiếng Việt, ngắn gọn, súc tích, đúng trọng tâm.
- Loại tài liệu phải ở dòng 1, trước 4 phần.`

	// Append user-supplied prompt override (e.g. "dịch ra tiếng Anh")
	if strings.TrimSpace(customPrompt) != "" {
		systemPrompt += "\n\n📝 YÊU CẦU ĐẶC BIỆT TỪ NGƯỜI DÙNG: " + strings.TrimSpace(customPrompt)
	}

	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: a.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: "Dưới đây là văn bản OCR cần phân tích:\n\n" + ocrText},
		},
		Temperature: a.scanTemperature,
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
		Temperature: a.qaTemperature,
	})

	if err != nil {
		return "", fmt.Errorf("LLM completion error: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty response from LLM agent")
	}

	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}
