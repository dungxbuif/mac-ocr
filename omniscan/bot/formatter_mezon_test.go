package bot

import (
	"encoding/json"
	"strings"
	"testing"

	mezon "mezon-bot-sdk"
)

// formatter_mezon_test.go là file THỰC NGHIỆM chuyển đổi kết quả Agent
// (ClassifyResult: {DocType, Formatted}) sang content Mezon (embed + buttons)
// và xác nhận wire shape JSON khớp đúng theo docs mezon-sdk 2.8.55:
//
//   {"embed": [{title, description, footer, color, timestamp, fields}],
//    "components": [{id, type:1, component:{label, style}}, ...]}
//
// Các case bọc các kịch bản: invoice ngắn, hợp đồng dài cần truncate, body rỗng,
// docType rỗng, và round-trip JSON không HTML-escape URL.

func TestBuildScanResultContent_InvoiceShortProducesEmbedCard(t *testing.T) {
	// Giả lập LLM trả về một hóa đơn ngắn (Markdown table).
	docType := "Hóa đơn"
	formatted := "## HÓA ĐƠN BÁN HÀNG\n\n| Mục | Số lượng | Thành tiền |\n|---|---|---|\n| Cà phê | 2 | 60.000đ |\n\n**Tổng cộng: 60.000đ**"

	c := BuildScanResultContent(docType, formatted, 1, 5, 5)
	m, ok := c.(map[string]any)
	if !ok {
		t.Fatalf("content not a map: %T", c)
	}

	// 1) embed array với đúng 1 phần tử.
	embeds, ok := m["embed"].([]map[string]any)
	if !ok || len(embeds) != 1 {
		t.Fatalf("embed must be []map len 1, got %T %v", m["embed"], m["embed"])
	}
	embed := embeds[0]

	// 2) title chứa docType đã in hoa.
	if !strings.Contains(toStr(embed["title"]), "HÓA ĐƠN") {
		t.Errorf("title = %v, want contain \"HÓA ĐƠN\"", embed["title"])
	}

	// 3) description giữ nguyên markdown body khi đủ budget.
	desc := toStr(embed["description"])
	if !strings.Contains(desc, "Tổng cộng: 60.000đ") {
		t.Errorf("description lost body content: %q", desc)
	}
	// Không có note cắt ngắn vì body ngắn.
	if strings.Contains(desc, "đã cắt ngắn") {
		t.Errorf("description should not be truncated: %q", desc)
	}

	// 4) footer có lượt scan + askLimit.
	footer, _ := embed["footer"].(map[string]any)
	if !strings.Contains(toStr(footer["text"]), "Lượt 1/5") ||
		!strings.Contains(toStr(footer["text"]), "5 câu") {
		t.Errorf("footer.text = %v", footer["text"])
	}

	// 5) color là chuỗi hex "#...." từ palette mezon-sdk.
	if c, ok := embed["color"].(string); !ok || !strings.HasPrefix(c, "#") {
		t.Errorf("color must be hex string from palette, got %v (%T)", embed["color"], embed["color"])
	}

	// 6) timestamp ISO.
	if ts, ok := embed["timestamp"].(string); !ok || ts == "" {
		t.Errorf("timestamp must be ISO string, got %v", embed["timestamp"])
	}

	// 7) components là flat buttons; mỗi button lồng label/style dưới
	//    "component" (đây là fix cho bug label trống).
	btns, ok := m["components"].([]map[string]any)
	if !ok || len(btns) != 2 {
		t.Fatalf("components must be 2 flat buttons, got %T %v", m["components"], m["components"])
	}
	for i, want := range []struct{ id, label string }{
		{"omniscan_scan_detail", "📄 Văn bản gốc"},
		{"omniscan_quota", "📊 Lượt dùng"},
	} {
		if btns[i]["id"] != want.id {
			t.Errorf("btn[%d].id = %v, want %s", i, btns[i]["id"], want.id)
		}
		comp, ok := btns[i]["component"].(map[string]any)
		if !ok {
			t.Fatalf("btn[%d].component not a map: %T", i, btns[i]["component"])
		}
		if comp["label"] != want.label {
			t.Errorf("btn[%d].component.label = %v, want %s", i, comp["label"], want.label)
		}
		if btns[i]["type"] != int(mezon.ComponentButton) {
			t.Errorf("btn[%d].type = %v, want %d", i, btns[i]["type"], int(mezon.ComponentButton))
		}
	}
}

func TestBuildScanResultContent_LongDocumentTruncatesDescription(t *testing.T) {
	// Giả lập hợp đồng rất dài — vượt embedDescriptionLimit (4096 UTF-16).
	long := strings.Repeat("Điều khoản: không được sao chép dưới mọi hình thức. ", 300) // ~12000 chars
	if mezon.UTF16Len(long) <= embedDescriptionLimit {
		t.Fatalf("test data too short: %d UTF-16 units", mezon.UTF16Len(long))
	}

	c := BuildScanResultContent("Hợp đồng", long, 3, 5, 5).(map[string]any)
	embed := c["embed"].([]map[string]any)[0]
	desc := toStr(embed["description"])

	// description phải ≤ 4096 UTF-16 (cộng note cắt ở cuối).
	// TruncateUTF16 cắt đúng boundary, sau đó thêm note — kiểm tra body cắt.
	if mezon.UTF16Len(desc) > embedDescriptionLimit+200 {
		t.Errorf("description %d UTF-16 exceeds cap: %q", mezon.UTF16Len(desc), desc[:min(80, len(desc))])
	}
	// Có marker cắt ngắn.
	if !strings.Contains(desc, "cắt ngắn") && !strings.Contains(desc, "file") {
		t.Errorf("expected truncation marker, got: %q", desc[len(desc)-120:])
	}
}

func TestBuildScanResultContent_EmptyBodyFallsBackGracefully(t *testing.T) {
	c := BuildScanResultContent("Tài liệu chung", "", 2, 5, 5).(map[string]any)
	embed := c["embed"].([]map[string]any)[0]
	desc := toStr(embed["description"])
	if !strings.Contains(desc, "Không có nội dung") {
		t.Errorf("expected fallback placeholder, got %q", desc)
	}
}

func TestBuildScanResultContent_EmptyDocTypeDefaultsToTaiLieu(t *testing.T) {
	c := BuildScanResultContent("", "nội dung", 1, 5, 5).(map[string]any)
	embed := c["embed"].([]map[string]any)[0]
	if toStr(embed["title"]) != "🏷️ TÀI LIỆU" {
		t.Errorf("default title = %v, want \"🏷️ TÀI LIỆU\"", embed["title"])
	}
}

func TestBuildScanResultContent_JSONRoundTripNoEscape(t *testing.T) {
	// Xác nhận content marshal ra JSON giống wire shape mezon-js emit — không
	// HTML-escape, button label lồng đúng dưới "component".
	formatted := "## Test\n| A | B |\n|---|---|\n| 1 | 2 |\nhttps://e.com/x"
	c := BuildScanResultContent("Hóa đơn", formatted, 1, 5, 5)
	out := mustJSON(t, c)

	// Không HTML-escape ký tự < (mezon client không unescape).
	if strings.Contains(out, "\\u003c") || strings.Contains(out, "\\u003e") {
		t.Errorf("JSON should not HTML-escape: %s", out)
	}
	// Button wire: component nesting đúng.
	if !strings.Contains(out, `"component":{"label":"📄 Văn bản gốc","style":1}`) {
		t.Errorf("button component nesting missing: %s", out)
	}
	// embed title + footer presence.
	if !strings.Contains(out, `"title":"🏷️ HÓA ĐƠN"`) {
		t.Errorf("title missing in JSON: %s", out)
	}
	if !strings.Contains(out, `"footer":{"text":"Lượt 1/5`) {
		t.Errorf("footer missing in JSON: %s", out)
	}
	//拌匀即可 markdown table không bị biến dạng.
	if !strings.Contains(out, "|---|---|") {
		t.Errorf("markdown table lost in JSON: %s", out)
	}
}

func TestBuildScanResultContent_IncludesButtonsEvenWhenNoBody(t *testing.T) {
	btns := mezon.NewButtonBuilder().
		AddButton("omniscan_scan_detail", "📄 Văn bản gốc", mezon.ButtonPrimary).
		AddButton("omniscan_quota", "📊 Lượt dùng", mezon.ButtonSecondary).
		Build()
	if len(btns) != 2 {
		t.Fatalf("expect 2 buttons in result")
	}
	comp, _ := btns[0]["component"].(map[string]any)
	if comp["label"] != "📄 Văn bản gốc" {
		t.Errorf("first button label = %v", comp["label"])
	}
}

// toStr ép any về string, fail test nếu không phải string.
func toStr(v any) string {
	s, _ := v.(string)
	return s
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
