# OmniScan Release Notes — v1.0.0

**Release Date:** August 15, 2026  
**Build Status:** Production Ready 🟢 (`10/10 tests passed`)  
**Core Stack:** Go 1.23+ • Mezon SDK Go • MacOCR Native Engine (Apple Silicon) • LLM Agent (`qwen3.6-35b-a3b` / OpenAI-compatible) • PostgreSQL / Redis / SQLite

---

## 🚀 Version 1.0.0 Highlights

OmniScan v1.0.0 marks the first official production-ready release of the AI-powered document intelligence bot for the **Mezon Chat Platform**, backed by Apple Silicon MacOCR native engine and local/remote LLM reasoning.

### 🌟 Major Features

#### 1. 📐 2D Geometric Bounding-Box Layout Reconstruction (`ocr/layout.go`)
- **Fixes Multi-Column / Table OCR Column Collapsing**: Replaces raw vertical text dumps with an intelligent 2D reading order algorithm.
- Normalizes bounding box coordinates `[x, y, width, height]` with Vision's lower-left origin.
- Groups text blocks within `rowTolerance (1.5%)` and sorts horizontally by `left (x)` ascending.
- Automatically inserts tab stops (`\t`) for tabular data when horizontal column gaps exceed `2%`, preserving multi-column invoices, vocabulary lists, and financial statements without requiring LLM tokens.

#### 2. 🧠 Smart AI Scan with Inline Prompt Steering (`*scan`)
- Performs native OCR and feeds the 2D-reconstructed text to the LLM Agent.
- **Document Categorization**: Auto-detects `[Hóa đơn]` (Invoices), `[CCCD / Danh thiếp]` (ID cards), `[Hợp đồng]` (Contracts), and `[Tài liệu chung]` (General).
- **Inline Custom Prompt**: Supports custom user instructions directly on the command line:
  ```text
  *scan "Dịch toàn bộ sang tiếng Anh và lập bảng từ vựng" https://example.com/vocab.jpg
  *scan "Chỉ trích xuất mã số thuế và tổng thanh toán" https://example.com/bill.png
  ```

#### 3. 💬 Interactive Threaded Q&A (Quote-Reply)
- Quote-reply (Trích dẫn tin nhắn) to any previous bot response to ask follow-up questions.
- Retains full OCR context in an active session (default: 5 questions/document).
- Instant session destruction upon hitting quota or 24-hour TTL expiration.

#### 4. 🎨 Rich Mezon Embeds & Smart File Attachments
- Renders rich interactive card embeds on Mezon with dynamic status colors, accuracy badges (`🟢 98.5%`), and interactive action buttons (`🔄 Scan tiếp`, `📊 Lượt dùng`).
- **Oversized Text Handling**: When OCR or AI text exceeds chat display limits (3,500–4,096 UTF-16 units), the bot automatically generates and uploads companion `.txt` / `.md` file attachments.

#### 5. 🛡️ Enterprise Security & SSRF Protection
- Strict URL validation blocking loopback, link-local (`169.254.169.254`), and private RFC 1918 subnets (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`).
- MIME-type sniffing and file size caps (max 100 MiB).
- Base64 upload fallback for secure proxy communication across network boundaries.

#### 6. 🌐 Dual-Storage Quota Engine (PostgreSQL / SQLite / Redis)
- Seamless dual-storage support:
  - **Local/Stand-Alone**: SQLite (`omniscan.db`, `omniscan_sessions.db`) with `syscall.Flock` process locking.
  - **Enterprise Multi-Replica**: PostgreSQL + Redis cluster with atomic Lua script increments, user auto-provisioning (`user_configs`), and `SETNX` message deduplication.

---

## 📋 Command Reference

| Command | Syntax | Description |
|---|---|---|
| `*scan` | `*scan ["prompt"] <url>` *(or attach file)* | AI OCR analysis, classification & Markdown table formatting |
| `*ocr` | `*ocr <url>` *(or attach file)* | 2D Bounding-Box Raw OCR text extraction |
| `Reply` | Trích dẫn tin nhắn kết quả | Threaded Q&A trực tiếp trên tài liệu (Tối đa 5 câu) |
| `*quota` | `*quota` (hoặc `*me`) | Kiểm tra hạn ngạch scan & hỏi đáp còn lại trong ngày |
| `*os` | `*os` (hoặc `*os help`) | Hiển thị menu hướng dẫn tương tác dạng Rich Card |

---

## ⚙️ Configuration Cheat Sheet (`.env`)

```ini
# Mezon Bot Credentials
MEZON_BOT_ID=your_bot_id
MEZON_BOT_TOKEN=your_bot_token
MEZON_HOST=gw.mezon.ai
MEZON_PORT=443

# MacOCR Proxy Connection
OCR_PROXY_URL=https://ocr.example.com
OCR_API_KEY=sk_ocr_xxxxxxxxxxxxxxxxxxxxxxxxxxxx

# LLM AI Agent Configuration
LLM_BASE_URL=http://10.10.0.10:1234/v1
LLM_API_KEY=your_llm_api_key
LLM_MODEL=qwen/qwen3.6-35b-a3b

# Storage & Quotas
DAILY_SCAN_LIMIT=5
SESSION_ASK_LIMIT=5
DATABASE_URL=postgres://user:pass@localhost:5432/macocr?sslmode=disable
REDIS_URL=redis://localhost:6379/0
```

---

## 🧪 Verification & Test Results

```bash
cd omniscan && go test -v ./...
# Result: 10 passed in 7 packages (100% PASS)
```
- `omniscan/agent`: Auto-classification & Q&A prompt pipeline verified.
- `omniscan/bot`: Embed wire shape, button component hierarchy, quota calculation verified.
- `omniscan/ocr`: 2D geometric reading order reconstruction verified with synthetic bounding boxes.
- `omniscan/security`: SSRF validator & private IP filter verified.
- `omniscan/storage`: SQLite & PostgreSQL schema provisioning, quota increment & refund verified.
