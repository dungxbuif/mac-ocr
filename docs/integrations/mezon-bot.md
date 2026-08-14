---
title: OmniScan Mezon Bot
sidebar_position: 5
---

# OmniScan Mezon Bot

**OmniScan** is an automated AI-powered Mezon platform bot written in Go that connects to the `mac-ocr` proxy backend and LLM AI Agent (`qwen/qwen3.6-35b-a3b`). It listens to Mezon chat channels, detects document/image URLs or file attachments, extracts text via OCR, automatically classifies and formats documents, and supports interactive follow-up Q&A directly in chat threads.

---

## 🚀 Key Capabilities

- **Smart `!scan` AI Agent**: Auto-detects document categories (`[Hóa đơn]`, `[CCCD / Danh thiếp]`, `[Hợp đồng]`, `[Tài liệu chung]`) and formats output into structured Markdown tables or summaries.
- **Interactive Threaded Q&A**: Users can ask follow-up questions about any document by simply **replying (quote reply)** to the bot's message in Mezon.
- **Pre-Call Security & Validation Layer**: Scheme checking, SSRF/Private IP blocking, file format validation (`.png`, `.jpg`, `.jpeg`, `.webp`, `.tiff`, `.pdf`), and 100 MiB attachment size limit.
- **Horizontal Scaling & Redis Cluster**: Multi-replica ready using Redis atomic Lua scripts for daily quota enforcement, `SETNX` message deduplication, and L2 shared caching across replicas.
- **Dynamic User Quotas & DB Administration**: Auto-provisions default limits into database on first encounter (`user_configs` table). Administrators can directly modify user limits in DB (`daily_scan_limit`, `session_ask_limit`).
- **Privacy Data Hygiene**: Automatically purges session data immediately after the maximum allowed follow-up Q&A asks are reached, with 24-hour background ticker cleanup.

---

## 💬 Bot Command Reference

| Command | Subcommands / Aliases | Description |
| :--- | :--- | :--- |
| **`!scan <url>`** | `/scan` *(or attach image/PDF)* | Smart AI Agent OCR: Auto-detects document category & formats structured Markdown |
| **`!ocr <url>`** | `/ocr` *(or attach image/PDF)* | Raw OCR: Bóc tách văn bản thô |
| **Reply Tin Nhắn Bot** | Quote reply | Hỏi - đáp trực tiếp trên tài liệu đính kèm (Tối đa 5 câu/tài liệu) |
| **`!omniscan help`** | `!omni`, `!help`, `/help` | Hiển thị bảng hướng dẫn Card |
| **`!omniscan quota`** | `!quota`, `/quota`, `!me` | Xem số lượt scan và hỏi đáp còn lại hôm nay |
| **`!omniscan ping`** | `!ping`, `/ping` | Kiểm tra kết nối Bot |

---

## ⚙️ Environment Configuration

Set the following environment variables in `omniscan/.env`:

```env
# Mezon Gateway Configuration
MEZON_BOT_ID=2088352969647984640
MEZON_BOT_TOKEN=FijBrfNlDSXYOMvH
MEZON_HOST=gw.mezon.ai
MEZON_PORT=443

# MacOCR Proxy Configuration
OCR_PROXY_URL=http://localhost:8080
OCR_API_KEY=sk_ocr_93f85d5be0929d6ea1cd31020bb847c910f230ebe9160518

# AI Agent LLM Endpoint Configuration
LLM_BASE_URL=http://10.10.0.10:1234/v1
LLM_API_KEY=sk-lm-xSAXbTjI:nEozedXNZyTMFLDgvovH
LLM_MODEL=qwen/qwen3.6-35b-a3b

# Optional Limit & Scaling Configuration
DAILY_SCAN_LIMIT=5
SESSION_ASK_LIMIT=5
REDIS_URL=redis://localhost:6379/0
```

---

## 🏗️ Architecture & Component Topology

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
                    │  - Event Deduplication (SETNX messageID)    │
                    │  - mezon-sdk-go L2 Shared Cache             │
                    │                                             │
                    ├───────────────► mac-ocr Proxy ◄─────────────┤
                    │                                             │
                    └───────────────► LLM Agent ◄─────────────────┘
                               (http://10.10.0.10:1234/v1)
```
