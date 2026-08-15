---
title: OmniScan Bot Integration
sidebar_position: 3
---

# OmniScan Bot Integration (Mezon)

**OmniScan** is the official AI-powered bot service for the **Mezon Chat Platform**, connecting native Apple Silicon MacOCR with LLM reasoning agents.

---

## 🌟 Capabilities

- **2D Bounding-Box Layout Reconstruction:** Automatically recovers tabular and multi-column layouts without column misalignment.
- **Smart AI Classification & Formatting (`*scan`):** Auto-identifies invoices, receipts, ID cards, and contracts; presents structured Markdown tables.
- **Custom Prompt Steering:** Supports natural language instructions:
  ```text
  *scan "Dịch sang tiếng Việt và lập bảng từ vựng" https://example.com/vocab.png
  ```
- **Verbatim Raw OCR (`*ocr`):** Direct text extraction with confidence statistics.
- **Interactive Threaded Q&A:** Reply (quote-reply) directly to the bot to ask follow-up questions about the document.
- **Rich Embeds & File Attachments:** Clean card presentation on Mezon with fallback `.txt` / `.md` file uploads for long texts.

---

## 🚀 Quick Setup

### 1. Environment Configuration

Create `omniscan/.env`:
```ini
MEZON_BOT_ID=your_bot_id
MEZON_BOT_TOKEN=your_bot_token
MEZON_HOST=gw.mezon.ai
MEZON_PORT=443

OCR_PROXY_URL=https://ocr.dungxbuif.com
OCR_API_KEY=sk_ocr_your_api_key

LLM_BASE_URL=http://10.10.0.10:1234/v1
LLM_API_KEY=your_key
LLM_MODEL=qwen/qwen3.6-35b-a3b

DATABASE_URL=postgres://user:pass@localhost:5432/macocr?sslmode=disable
REDIS_URL=redis://localhost:6379/0
```

### 2. Run OmniScan

```bash
cd omniscan
go build -o omniscan .
./omniscan
```

---

## 💬 Command Reference

| Command | Usage | Result |
|---|---|---|
| `*scan` | `*scan ["prompt"] <url>` *(or attach file)* | AI OCR analysis, category detection & Markdown formatting |
| `*ocr` | `*ocr <url>` *(or attach file)* | 2D Bounding-Box Raw OCR text extraction |
| `Reply` | Trích dẫn tin nhắn của Bot | Threaded Q&A trên tài liệu (Tối đa 5 câu) |
| `*quota` | `*quota` | Xem lượt scan & hỏi đáp còn lại trong ngày |
| `*os` | `*os` *(hoặc `*os help`)* | Mở bảng hướng dẫn tương tác dạng Rich Card |
