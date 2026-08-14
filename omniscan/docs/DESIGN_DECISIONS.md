# OmniScan Mezon Bot — Technical Architecture, Decisions & Action History

This document records all architectural decisions, design choices, database schemas, environmental configurations, and actions taken during the development of the **OmniScan** Mezon Bot service.

---

## 📋 Table of Contents
1. [Service Identity & Overview](#1-service-identity--overview)
2. [Architecture & Single-Instance Process Lock](#2-architecture--single-instance-process-lock)
3. [Environment Configuration Reference (.env)](#3-environment-configuration-reference-env)
4. [Database Schemas & Data Structures](#4-database-schemas--data-structures)
5. [Pre-Call Security & Validation Layer](#5-pre-call-security--validation-layer)
6. [User Management & Dynamic Quota Engine](#6-user-management--dynamic-quota-engine)
7. [Data Hygiene & Privacy Purge](#7-data-hygiene--privacy-purge)
8. [Horizontal Scaling & Multi-Replica Mode](#8-horizontal-scaling--multi-replica-mode)
9. [AI Agent Integration & Threaded Q&A](#9-ai-agent-integration--threaded-qa)
10. [UI/UX & Rich Card Formatting](#10-uiux--rich-card-formatting)
11. [Command Inventory](#11-command-inventory)
12. [Action History Log](#12-action-history-log)

---

## 1. Service Identity & Overview

- **Service Name**: `OmniScan`
- **Location**: `omniscan/` (workspace root)
- **Language**: Go 1.23+
- **Platform**: Mezon Chat Platform via local SDK (`./mezon-sdk-go`)
- **Backend OCR Engine**: `mac-ocr` Proxy REST API (`http://localhost:8080`) using Bearer API Key (`sk_ocr_...`)

---

## 2. Architecture & Single-Instance Process Lock

- **HTTP Polling Job Pattern**: `POST /v1/documents` -> poll `GET /v1/documents/{documentId}` loop. Outbound-only execution requiring zero inbound ports or public Webhook endpoints.
- **Single-Instance Process Lock Guard**: Implemented OS file locking (`syscall.Flock` on `omniscan.lock`) at main startup. If a second bot process is launched, it immediately halts with:
  `🛑 Another instance of OmniScan bot is already running (lock active). Exiting to prevent duplicate replies.`
  This prevents multiple bot instances from connecting to Mezon simultaneously and duplicate-replying to user messages.
- **Code Modularization**:
  ```text
  omniscan/
  ├── config/      # Environment loader (Strict env reading & default limits)
  ├── ocr/         # MacOCR API client
  ├── security/    # SSRF & input validator
  ├── storage/     # QuotaStore, UserConfigStore & SessionStore (SQLite & Redis)
  ├── agent/       # OpenAI-compatible LLM agent (qwen/qwen3.6-35b-a3b)
  ├── bot/         # Event handlers, routing & rich card formatters
  ├── docs/        # Architecture & decision documentation
  └── main.go      # Service entrypoint with process lock guard
  ```

---

## 3. Environment Configuration Reference (.env)

| Variable | Description | Example / Default | Required |
| :--- | :--- | :--- | :---: |
| `MEZON_BOT_ID` | Mezon Bot ID | `2088352969647984640` | **Yes** |
| `MEZON_BOT_TOKEN` | Mezon Bot Gateway Secret | `FijBrfNlDSXYOMvH` | **Yes** |
| `MEZON_HOST` | Gateway Host | `gw.mezon.ai` | No (default) |
| `MEZON_PORT` | Gateway Port | `443` | No (default) |
| `OCR_PROXY_URL` | MacOCR Proxy URL | `http://localhost:8080` | No (default) |
| `OCR_API_KEY` | Bearer API Key for MacOCR | `sk_ocr_93f85d...` | **Yes** |
| `LLM_BASE_URL` | AI Agent Base URL | `http://10.10.0.10:1234/v1` | **Yes** |
| `LLM_API_KEY` | AI Agent API Key | `sk-lm-xSAXb...` | **Yes** |
| `LLM_MODEL` | AI Agent Model Name | `qwen/qwen3.6-35b-a3b` | **Yes** |
| `DAILY_SCAN_LIMIT` | Default daily scans per user | `5` | No (default 5) |
| `SESSION_ASK_LIMIT` | Default asks per scan session | `5` | No (default 5) |
| `REDIS_URL` | Redis URL for Multi-Replica mode | `redis://localhost:6379/0` | Optional |

---

## 4. Database Schemas & Data Structures

### SQLite Tables (`omniscan.db` & `omniscan_sessions.db`)

#### `user_configs` Table (User Custom Limits)
```sql
CREATE TABLE IF NOT EXISTS user_configs (
    user_id TEXT PRIMARY KEY,
    daily_scan_limit INTEGER NOT NULL,
    session_ask_limit INTEGER NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
```

#### `user_daily_scans` Table (Daily Usage Counter)
```sql
CREATE TABLE IF NOT EXISTS user_daily_scans (
    user_id TEXT NOT NULL,
    scan_date TEXT NOT NULL,
    count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, scan_date)
);
```

#### `scan_sessions` Table (Active Document Q&A Sessions)
```sql
CREATE TABLE IF NOT EXISTS scan_sessions (
    session_id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    document_id TEXT NOT NULL,
    doc_type TEXT NOT NULL,
    ocr_text TEXT NOT NULL,
    ask_count INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL
);
```

### Redis Key Patterns (Multi-Replica Mode)
- **User Config**: `omniscan:userconfig:<userID>` (Hash: `daily_scan_limit`, `session_ask_limit`)
- **Daily Quota**: `omniscan:quota:<YYYY-MM-DD>:<userID>` (String counter, TTL 24h)
- **Active Session**: `omniscan:session:<sessionID>` (JSON ScanSession object, TTL 24h)
- **Deduplication Lock**: `omniscan:msg:<messageID>` (String "1", TTL 10m)

---

## 5. Pre-Call Security & Validation Layer

Enforces strict checks **before** dispatching requests to the backend:

1. **Scheme Check**: Only `http://` and `https://` allowed.
2. **SSRF & Private Network Blocking**: Blocks loopback (`127.0.0.1`, `localhost`), RFC 1918 private IPs (`10.x.x.x`, `172.16-31.x.x`, `192.168.x.x`), link-local (`169.254.x.x`), and cloud metadata IP (`169.254.169.254`).
3. **Format Checking**: Accepts `.png`, `.jpg`, `.jpeg`, `.webp`, `.tiff`, `.tif`, `.pdf`.
4. **Size Ceiling**: Caps attachment sizes at 100 MiB.

---

## 6. User Management & Dynamic Quota Engine

- **First-Encounter Provisioning**: When a user first interacts with OmniScan, a default user record is automatically created in `user_configs` (`user_id`, `daily_scan_limit`, `session_ask_limit`, `created_at`, `updated_at`).
- **Direct DB Limit Customization**: Quota limits are resolved dynamically per user from `user_configs`. Administrators can edit `daily_scan_limit` or `session_ask_limit` directly in the database (SQLite or Redis), and OmniScan will immediately apply the custom limits without restarting the bot.
- **Automatic Quota Refund**: If backend OCR processing fails or times out, 1 scan quota unit is automatically refunded to the user (`RefundQuota`).

---

## 7. Data Hygiene & Privacy Purge

- **Immediate Session Purge**: Once a scan session reaches its maximum allowed Q&A asks (default 5), the session data (including extracted OCR text and history) is **immediately deleted/purged** from storage for privacy and storage hygiene.
- **Background Auto-Cleanup**: A background ticker continuously purges any session records older than 24 hours.

---

## 8. Horizontal Scaling & Multi-Replica Mode

- **Auto-Switching Mode**:
  - If `REDIS_URL` is set -> Activates `RedisQuotaStore`, `RedisSessionStore`, `RedisDeduplicator` (`SETNX omniscan:msg:<messageID>`), and `RedisSharedStore` (`mezon.SharedStore`).
  - If `REDIS_URL` is empty -> Fallback to `SQLiteQuotaStore` + `SQLiteSessionStore` + `InMemoryDeduplicator`.

---

## 9. AI Agent Integration & Threaded Q&A

- **SDK**: `github.com/sashabaranov/go-openai`
- **Environment Credentials**:
  - `LLM_BASE_URL=http://10.10.0.10:1234/v1`
  - `LLM_API_KEY=sk-lm-xSAXbTjI:nEozedXNZyTMFLDgvovH`
  - `LLM_MODEL=qwen/qwen3.6-35b-a3b`
- **Smart `!scan` Flow**:
  1. OCR text extraction via `mac-ocr`.
  2. LLM Agent Auto-Classification (`[Hóa đơn]`, `[CCCD / Danh thiếp]`, `[Hợp đồng]`, `[Tài liệu chung]`).
  3. Structured Markdown output formatting.
  4. Stores session mapping `botReplyMessageID` -> `ScanSession` in `SessionStore`.
- **Interactive Threaded Q&A**: Users reply (quote reply `m.References`) to any bot response message to ask follow-up questions.

---

## 10. UI/UX & Rich Card Formatting

- **Rich Card Layout**: Structured using card borders `────────────────────────`, header badges, and blockquote tips `>`.
- **Character Capping**: Capped at 3,000 characters to protect Mezon chat payload limits.
- **Markdown Safety**: Inner triple backticks (```) escaped to (''') to prevent breaking markdown formatting.

---

## 11. Command Inventory

| Command | Description |
| :--- | :--- |
| **`!scan <url>`** | AI Agent OCR: Auto-detects document category & formats structured Markdown |
| **`!ocr <url>`** | Raw OCR: Bóc tách văn bản thô |
| **Reply Tin Nhắn Bot** | Hỏi - đáp trực tiếp trên tài liệu đính kèm (Tối đa 5 câu/tài liệu) |
| **`!omniscan help`** / **`!help`** | Hiển thị bảng hướng dẫn Card |
| **`!omniscan quota`** / **`!quota`** | Xem số lượt scan và hỏi đáp còn lại hôm nay |
| **`!omniscan ping`** / **`!ping`** | Kiểm tra kết nối Bot |

---

## 12. Action History Log

1. **Bootstrap Service**: Cloned `mezon-sdk-go`, added ignore rules to `.gitignore`, created Go module `omniscan`.
2. **Backend API Key Generation**: Issued dedicated unlimited API key (`sk_ocr_93f...`) via `macocr-admin create-key`.
3. **Security Layer**: Implemented SSRF private IP blocking, URL scheme, MIME, and 100MB size checks.
4. **Quota Engine**: Implemented `QuotaStore` with SQLite and Redis multi-replica implementations.
5. **Horizontal Scaling**: Added Redis distributed deduplication (`SETNX`), `RedisSessionStore`, and L2 cache store adapter.
6. **Command Refactoring**: Split raw OCR to `!ocr` and smart AI Agent to `!scan`.
7. **AI Agent Integration**: Configured OpenAI Go SDK connecting to `qwen/qwen3.6-35b-a3b` at `http://10.10.0.10:1234/v1`.
8. **User Provisioning & Direct DB Limits**: Added `user_configs` table with auto-provisioning defaults on first encounter and dynamic DB limit lookup.
9. **Privacy Purge**: Implemented immediate session deletion upon 5th Q&A completion and 24h background auto-cleanup.
10. **Rich Card UI & Namespacing**: Designed card formatters, added subcommands `!omniscan help`, `!omniscan quota`, `!omniscan ping`.
11. **Single-Instance Process Lock & Task Cleanup**: Added OS file lock (`syscall.Flock`) to `main.go` preventing duplicate bot instances, terminated background tasks, and verified zero duplicate replies.
