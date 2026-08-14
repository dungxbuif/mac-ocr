# OmniScan — Intelligent Mezon Bot Service for MacOCR & AI Agent

[![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat-square&logo=go)](https://go.dev/)
[![Mezon SDK](https://img.shields.io/badge/Mezon-SDK--Go-0080FF?style=flat-square)](https://github.com/quangledang23/mezon-sdk-go)
[![Backend Engine](https://img.shields.io/badge/Backend-MacOCR--Proxy-FF6B6B?style=flat-square)](../docs/README.md)
[![LLM Model](https://img.shields.io/badge/LLM-qwen3.6--35b--a3b-722ED1?style=flat-square)](https://openai.com/)

**OmniScan** is an enterprise-grade AI chatbot service built in Go for the [Mezon Chat Platform](https://mezon.ai). It seamlessly integrates high-performance native OCR (`mac-ocr`) with advanced LLM Vision/Text AI Agents (`qwen/qwen3.6-35b-a3b`). 

OmniScan automatically detects document categories (Receipts, ID Cards, Contracts, General Documents), formats extracted data into structured Markdown tables, and enables users to conduct interactive follow-up Q&A directly in chat threads.

---

## 🛠️ Highlights & Core Features

- 🤖 **Smart `!scan` AI Agent**: Performs OCR and feeds extracted text to `qwen/qwen3.6-35b-a3b` for document classification (`[Hóa đơn]`, `[CCCD / Danh thiếp]`, `[Hợp đồng]`, `[Tài liệu chung]`) and structured Markdown rendering.
- 💬 **Interactive Threaded Q&A**: Simply reply (quote reply) to any bot response message to ask up to 5 follow-up questions per document session.
- ⚡ **Raw `!ocr` Mode**: Direct OCR extraction returning verbatim raw text preserved in clean code blocks.
- 🔒 **Pre-Call SSRF & Security Validator**: Scheme verification (`http`/`https`), private/loopback IP blocking (`127.0.0.1`, `10.x.x.x`, `192.168.x.x`, `169.254.169.254`), file format restriction (`.png`, `.jpg`, `.jpeg`, `.webp`, `.tiff`, `.pdf`), and 100 MiB payload cap.
- 👑 **Admin-Customizable Quota Engine**: User records auto-provision into `user_configs` on first interaction. Admins can directly adjust user scan and ask limits in SQLite/Redis without restarting the service.
- 🧹 **Privacy Data Purge**: Sessions and extracted OCR data are **immediately deleted** upon reaching the maximum 5 questions, with a 24-hour ticker purging inactive sessions.
- 🔒 **Single-Instance Process Lock**: OS file lock (`syscall.Flock` on `omniscan.lock`) prevents duplicate local process launches and eliminates duplicate bot responses.
- 🌐 **Multi-Replica Horizontal Scaling**: Auto-switches to Redis cluster mode (`REDIS_URL`) using atomic Lua scripts for quota management, `SETNX` message deduplication, and L2 shared caching.

---

## 📐 System Architecture

```text
                               ┌─────────────────────────┐
                               │    Mezon Gateway        │
                               └────────────┬────────────┘
                                            │
                     ┌──────────────────────┴──────────────────────┐
                     ▼                                             ▼
        ┌────────────────────────┐                    ┌────────────────────────┐
        │  OmniScan Replica 1    │                    │  OmniScan Replica 2    │
        └───────────┬────────────┘                    └───────────┬────────────┘
                    │                                             │
                    ├───────────────► Redis Cluster ◄─────────────┤
                    │  - Distributed Quota (Lua INCR)             │
                    │  - Message Deduplication (SETNX)            │
                    │  - Shared L2 Cache                      │
                    │                                             │
                    ├───────────────► mac-ocr Proxy ◄─────────────┤
                    │   (HTTP Polling Job Pattern)                │
                    │                                             │
                    └───────────────► LLM AI Agent ◄──────────────┘
                               (http://10.10.0.10:1234/v1)
```

---

## 💬 Command Reference

| Command | Subcommands & Aliases | Description |
| :--- | :--- | :--- |
| **`!scan <url>`** | `/scan` *(or attached file)* | AI Agent OCR: Auto-classifies document category & generates Markdown table |
| **`!ocr <url>`** | `/ocr` *(or attached file)* | Raw OCR: Direct verbatim text extraction |
| **Reply Tin Nhắn Bot** | Quote reply | Threaded Q&A: Ask follow-up questions directly on document (Default 5/session) |
| **`!omniscan help`** | `!omni`, `!help`, `/help` | Displays sleek Rich Card help guide |
| **`!omniscan quota`** | `!quota`, `/quota`, `!me` | Checks user's remaining scan quota and ask limits |
| **`!omniscan ping`** | `!ping`, `/ping` | Connectivity and service health check |

---

## ⚙️ Environment Reference (`.env`)

Copy `.env.example` to `.env` and fill in your credentials:

```bash
cp .env.example .env
```

| Variable | Required | Default | Description |
| :--- | :---: | :--- | :--- |
| `MEZON_BOT_ID` | **Yes** | — | Mezon Bot Client ID |
| `MEZON_BOT_TOKEN` | **Yes** | — | Mezon Bot Secret Token |
| `MEZON_HOST` | No | `gw.mezon.ai` | Mezon Gateway Host |
| `MEZON_PORT` | No | `443` | Mezon Gateway Port |
| `OCR_PROXY_URL` | No | `http://localhost:8080` | MacOCR Proxy REST endpoint |
| `OCR_API_KEY` | **Yes** | — | Bearer API key for MacOCR (generated via `macocr-admin create-key`) |
| `LLM_BASE_URL` | **Yes** | `http://10.10.0.10:1234/v1` | OpenAI-compatible LLM endpoint |
| `LLM_API_KEY` | **Yes** | — | API key for LLM Agent |
| `LLM_MODEL` | **Yes** | `qwen/qwen3.6-35b-a3b` | Target LLM model name |
| `DAILY_SCAN_LIMIT` | No | `5` | Default daily scans per user |
| `SESSION_ASK_LIMIT` | No | `5` | Default questions per scan session |
| `REDIS_URL` | No | — | Redis URL for multi-replica horizontal scaling (e.g. `redis://localhost:6379/0`) |

---

## 📦 Database & Admin Limits Customization

### SQLite Schemas (`omniscan.db` & `omniscan_sessions.db`)
OmniScan automatically provisions tables upon startup:
- `user_configs`: Stores `daily_scan_limit` and `session_ask_limit` per user.
- `user_daily_scans`: Tracks daily scan counters per user.
- `scan_sessions`: Stores active document session text for quote-reply Q&A.

### Adjusting User Limits Directly in DB
Administrators can grant custom quotas to specific users directly in SQLite:

```sql
UPDATE user_configs 
SET daily_scan_limit = 50, session_ask_limit = 10 
WHERE user_id = '1783704549828071424';
```
OmniScan immediately respects the updated limits **without restarting the service**.

---

## 🚀 Getting Started

1. **Verify Backend Services**:
   Ensure `mac-ocr` proxy server is running at `http://localhost:8080`.

2. **Run Unit Tests**:
   ```bash
   cd omniscan
   go test ./...
   ```

3. **Start OmniScan Service**:
   ```bash
   cd omniscan
   go run .
   ```

---

## 📚 Further Documentation

- **[Requirements Specification](docs/REQUIREMENTS_SPEC.md)**: Detailed functional (FR) & non-functional (NFR) requirements.
- **[Design Decisions & Architecture](docs/DESIGN_DECISIONS.md)**: Technical decisions, security policies, and action history.
- **[Docusaurus Web Documentation](../docs/integrations/mezon-bot.md)**: Static documentation embedded in the OCR platform.
