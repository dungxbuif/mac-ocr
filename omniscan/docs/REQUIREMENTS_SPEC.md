# OmniScan Service — Functional & Technical Requirements Specification

This document details all **functional requirements (FRs)**, **non-functional requirements (NFRs)**, and **business logic specifications** implemented in the **OmniScan** Mezon Bot service.

---

## 📌 Table of Contents
1. [Executive Summary](#1-executive-summary)
2. [Functional Requirements (FR)](#2-functional-requirements-fr)
   - [FR-1: Dual Scan Modes (`!scan` AI Agent vs `!ocr` Raw OCR)](#fr-1-dual-scan-modes-scan-ai-agent-vs-ocr-raw-ocr)
   - [FR-2: LLM Auto-Classification & Markdown Formatting](#fr-2-llm-auto-classification--markdown-formatting)
   - [FR-3: Interactive Threaded Q&A (Quote-Reply Context)](#fr-3-interactive-threaded-qa-quote-reply-context)
   - [FR-4: Personalized & Admin-Customizable Quota Engine](#fr-4-personalized--admin-customizable-quota-engine)
   - [FR-5: Privacy Data Hygiene & Session Purge](#fr-5-privacy-data-hygiene--session-purge)
   - [FR-6: Command Namespacing & Subcommands](#fr-6-command-namespacing--subcommands)
   - [FR-7: Rich Card UI & Presentation Formatting](#fr-7-rich-card-ui--presentation-formatting)
3. [Non-Functional Requirements (NFR)](#3-non-functional-requirements-nfr)
   - [NFR-1: Pre-Call Security & SSRF Protection](#nfr-1-pre-call-security--ssrf-protection)
   - [NFR-2: Single-Instance Process Lock Guard](#nfr-2-single-instance-process-lock-guard)
   - [NFR-3: Horizontal Scaling & Redis Cluster Mode](#nfr-3-horizontal-scaling--redis-cluster-mode)
   - [NFR-4: Automatic Quota Refund on Faults](#nfr-4-automatic-quota-refund-on-faults)
   - [NFR-5: Strict Environment Credentials Loading](#nfr-5-strict-environment-credentials-loading)
4. [User Flow & State Machine](#4-user-flow--state-machine)
5. [Traceability Matrix](#5-traceability-matrix)

---

## 1. Executive Summary

**OmniScan** is an enterprise-grade AI chatbot integrated into the Mezon platform. It bridges Mezon channels with the high-performance `mac-ocr` OCR Proxy engine and OpenAI-compatible Vision/Text LLM Models (`qwen/qwen3.6-35b-a3b`). OmniScan empowers users to extract text from images/PDFs, classify documents automatically, get structured Markdown tables, and interact with documents via quote-reply threads.

---

## 2. Functional Requirements (FR)

### FR-1: Dual Scan Modes (`!scan` AI Agent vs `!ocr` Raw OCR)

- **FR-1.1**: The bot MUST support two distinct scanning modes:
  - **`!scan <url>`** *(or file attachment)*: AI Agent mode. Performs OCR, auto-classifies document category, and formats data into structured Markdown.
  - **`!ocr <url>`** *(or file attachment)*: Raw OCR mode. Performs direct OCR extraction and returns verbatim raw text inside code blocks.
- **FR-1.2**: The bot MUST support inputs provided either as a URL parameter in the message or as an attached file (image/PDF).
- **FR-1.3**: The bot MUST display immediate progress feedback (e.g. `⏳ 🧠 AI Agent đang phân tích...`) upon command receipt.

### FR-2: LLM Auto-Classification & Markdown Formatting

- **FR-2.1**: The LLM Agent MUST classify extracted OCR text into one of the following document categories:
  - `[Hóa đơn]` (Receipts / Invoices)
  - `[CCCD / Danh thiếp]` (ID Cards / Business Cards)
  - `[Hợp đồng]` (Contracts / Agreements)
  - `[Tài liệu chung]` (General Documents)
- **FR-2.2**: The LLM Agent MUST format structured documents into clean Markdown tables (e.g., items, quantities, unit prices, totals for receipts) or structured key-value bullet points.
- **FR-2.3**: If LLM formatting encounters an error, the system MUST fallback gracefully to Raw OCR formatting without dropping user requests.

### FR-3: Interactive Threaded Q&A (Quote-Reply Context)

- **FR-3.1**: Users MUST be able to ask follow-up questions about scanned documents by replying (quote-reply `m.References`) to any bot response message.
- **FR-3.2**: The bot MUST associate reply messages with the corresponding `ScanSession` and feed original OCR text into LLM context.
- **FR-3.3**: The system MUST enforce a configurable ask limit per scan session (default 5 questions/session).
- **FR-3.4**: When replying to Q&A threads, the bot MUST indicate progress (e.g. `💭 AI đang suy nghĩ (Câu X/Y)...`) and include question sequence numbering (`(Câu X/Y)`).

### FR-4: Personalized & Admin-Customizable Quota Engine

- **FR-4.1**: When a user interacts with OmniScan for the first time, the system MUST automatically provision a default configuration record in the `user_configs` database table (`daily_scan_limit`, `session_ask_limit`).
- **FR-4.2**: The bot MUST read user limits dynamically from `user_configs`. Administrators MUST be able to update limits directly in the database (SQLite or Redis) for specific users, and OmniScan MUST apply changes immediately without service restart.
- **FR-4.3**: The `!quota` (or `!omniscan quota`) command MUST report:
  - Used scan count today vs daily limit.
  - Remaining scans today.
  - Ask limit per document session.

### FR-5: Privacy Data Hygiene & Session Purge

- **FR-5.1**: When a scan session completes its maximum allowed Q&A asks (e.g., 5th question answered), all stored OCR text and session metadata MUST be **immediately purged/deleted** from storage.
- **FR-5.2**: The system MUST run an automated background ticker (hourly) to purge any inactive scan sessions older than 24 hours.

### FR-6: Command Namespacing & Subcommands

- **FR-6.1**: The bot MUST support namespaced subcommands to prevent command collision with other bots in the same server:
  - Help: `!omniscan help`, `!omniscan`, `!omni`, `!help`, `/help`
  - Quota: `!omniscan quota`, `!omni quota`, `!quota`, `/quota`, `!me`
  - Ping: `!omniscan ping`, `!omni ping`, `!ping`, `/ping`

### FR-7: Rich Card UI & Presentation Formatting

- **FR-7.1**: Help messages and result messages MUST be rendered in card-like structured layouts using horizontal dividers (`────────────────────────`), category badges, and blockquote tips.
- **FR-7.2**: OCR text embedded inside Markdown codeblocks MUST escape triple backticks (```) to prevents UI layout distortion.
- **FR-7.3**: Responses exceeding 3,000 characters MUST be safely truncated to respect Mezon message payload boundaries.

---

## 3. Non-Functional Requirements (NFR)

### NFR-1: Pre-Call Security & SSRF Protection

- **NFR-1.1**: The validator MUST enforce URL scheme restriction (`http://` and `https://` only).
- **NFR-1.2**: The validator MUST block private/internal IP ranges (Loopback `127.0.0.1`, RFC 1918 `10.x.x.x`, `172.16-31.x.x`, `192.168.x.x`), link-local (`169.254.x.x`), and AWS/GCP metadata IP (`169.254.169.254`).
- **NFR-1.3**: Attachment sizes MUST be capped at 100 MiB.
- **NFR-1.4**: Allowed extension types MUST be strictly enforced: `.png`, `.jpg`, `.jpeg`, `.webp`, `.tiff`, `.tif`, `.pdf`.

### NFR-2: Single-Instance Process Lock Guard

- **NFR-2.1**: On local single-instance mode, main startup MUST acquire an exclusive OS file lock (`syscall.Flock` on `omniscan.lock`).
- **NFR-2.2**: If a second process attempts to start, it MUST log a collision message and exit immediately with status 1, guaranteeing zero duplicate bot instances or duplicate chat replies.

### NFR-3: Horizontal Scaling & Redis Cluster Mode

- **NFR-3.1**: When `REDIS_URL` is configured, OmniScan MUST activate `RedisQuotaStore`, `RedisSessionStore`, `RedisDeduplicator` (`SETNX omniscan:msg:<messageID>`), and `RedisSharedStore`.
- **NFR-3.2**: Quota increments in Redis MUST use atomic Lua scripts to guarantee thread safety and strict 24-hour TTL expiration across multiple bot replicas.

### NFR-4: Automatic Quota Refund on Faults

- **NFR-4.1**: If backend OCR processing fails or times out, 1 scan quota unit MUST be automatically refunded to the user (`RefundQuota`).

### NFR-5: Strict Environment Credentials Loading

- **NFR-5.1**: All LLM Agent credentials (`LLM_BASE_URL`, `LLM_API_KEY`, `LLM_MODEL`) and Mezon bot credentials (`MEZON_BOT_ID`, `MEZON_BOT_TOKEN`) MUST be loaded strictly from `.env` or system environment variables without hardcoded fallbacks.

---

## 4. User Flow & State Machine

```text
User Sends Message in Channel
      │
      ▼
Is Message a Quote Reply (m.References > 0)?
 ├── YES ──► Fetch Session from SessionStore
 │            │
 │            ├── Found ──► Check Session Ask Limit
 │            │              ├── Allowed (< 5) ──► Call LLM Q&A ──► Reply & Increment Ask Count
 │            │              │                                        │
 │            │              │                                        └── If Ask Count == 5 ──► Purge Session
 │            │              └── Reached (>= 5) ──► Reply Limit Reached
 │            └── Not Found ──► Ignore / Normal Handling
 │
 └── NO ───► Is Command (!scan, !ocr, !omniscan)?
              │
              ├── Help/Quota/Ping ──► Return Rich Card Response
              │
              └── Scan / OCR ──► Pre-Call Validation (SSRF, Scheme, MIME, Size)
                                  │
                                  ├── Fail ──► Reply Security Error
                                  └── Pass ──► Check Daily Quota (user_configs DB)
                                                │
                                                ├── Exceeded ──► Reply Quota Exceeded
                                                └── Allowed ──► Submit OCR to mac-ocr Proxy
                                                                 │
                                                                 ├── Fail ──► Refund Quota & Reply Error
                                                                 └── Pass ──► Format Output & Create Session
```

---

## 5. Traceability Matrix

| Requirement ID | Module / File | Verification Method |
| :--- | :--- | :--- |
| **FR-1, FR-2** | [omniscan/bot/bot.go](file:///Users/dungxbuif/workspace/mac-ocr/omniscan/bot/bot.go), [omniscan/agent/agent.go](file:///Users/dungxbuif/workspace/mac-ocr/omniscan/agent/agent.go) | Unit test & Manual test |
| **FR-3** | [omniscan/storage/session.go](file:///Users/dungxbuif/workspace/mac-ocr/omniscan/storage/session.go), [bot.go](file:///Users/dungxbuif/workspace/mac-ocr/omniscan/bot/bot.go) | Q&A thread test |
| **FR-4** | [omniscan/storage/store.go](file:///Users/dungxbuif/workspace/mac-ocr/omniscan/storage/store.go) | DB query & limit update test |
| **FR-5** | [omniscan/storage/session.go](file:///Users/dungxbuif/workspace/mac-ocr/omniscan/storage/session.go) | Session purge test |
| **FR-6, FR-7** | [omniscan/bot/formatter.go](file:///Users/dungxbuif/workspace/mac-ocr/omniscan/bot/formatter.go) | Formatter unit test |
| **NFR-1** | [omniscan/security/validator.go](file:///Users/dungxbuif/workspace/mac-ocr/omniscan/security/validator.go) | Validator unit test |
| **NFR-2** | [omniscan/main.go](file:///Users/dungxbuif/workspace/mac-ocr/omniscan/main.go) | Process lock CLI test |
| **NFR-3** | [omniscan/storage/redis_store.go](file:///Users/dungxbuif/workspace/mac-ocr/omniscan/storage/redis_store.go) | Redis Integration test |
