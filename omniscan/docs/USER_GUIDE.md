# OmniScan Bot — Comprehensive User & Operator Guide (v1.0.0)

This document provides complete instructions for standard users, bot administrators, and DevOps engineers deploying and operating **OmniScan**.

---

## 📖 1. End-User Guide

### 1.1 Command Prefix & Overview
All OmniScan commands start with the prefix **`*`**. You can trigger commands in any Mezon text channel where OmniScan is present.

### 1.2 `*scan` — Smart AI Document Analysis
Upload an image or document, or paste a public image URL along with `*scan`. OmniScan will OCR the document, reconstruct the layout, and use an AI reasoning agent to format it into Markdown.

#### Basic Usage:
```text
*scan https://example.com/invoice.jpg
```
*(Or attach an image / PDF to your message and type `*scan`)*

#### Custom Prompt Steering:
You can pass custom instructions wrapped in quotes before the URL or attachment:
```text
*scan "Dịch toàn bộ sang tiếng Việt và lập bảng chi tiết" https://example.com/sheet.png
*scan "Chỉ trích xuất họ tên, số CCCD, ngày sinh và địa chỉ"
```

#### Output Example:
- **Badge:** `🏷️ HÓA ĐƠN` *(Lượt 1/5)*
- **Body:** Markdown table with Line Items, Quantities, Unit Prices, VAT, and Total.
- **Buttons:** Click `🔄 Scan tiếp` or `📊 Lượt dùng`.

---

### 1.3 `*ocr` — 2D Bounding-Box Raw OCR
When you need the exact text verbatim with geometric layout preservation (e.g. multi-column vocabulary tables, code snippets, raw receipts) without LLM post-processing:

```text
*ocr https://example.com/vocabulary.png
```

#### Key Capabilities:
- Automatically fixes column misalignment using 2D coordinate grouping.
- Displays accuracy statistics (`🟢 Độ chính xác 98.2% • 1 trang • 42 khối chữ`).
- Easy 1-click text copy in codeblock format.
- For texts > 3,500 characters, full output is attached as an `ocr_YYYYMMDD.txt` file.

---

### 1.4 💬 Threaded Q&A (Hỏi Đáp Trực Tiếp)
After receiving any `*scan` or `*ocr` response, you can ask questions about that specific document:
1. **Reply (Quote/Trích dẫn)** the bot's result message.
2. Type your question in the reply box (e.g., *"Hạn thanh toán là ngày nào?"* or *"Tổng tiền đã gồm VAT chưa?"*).
3. The AI Agent will read the document context and answer concisely.

*Quota:* Up to **5 follow-up questions** per document by default.

---

### 1.5 `*quota` — Check Daily Limit
Check your remaining daily scans and per-document Q&A allowances:
```text
*quota
```

### 1.6 `*os` / `*os help` — Interactive Help
Displays the full interactive navigation card with quick tips and command buttons.

---

## 🛠️ 2. Operator & Administrator Guide

### 2.1 Managing User Quotas in PostgreSQL
When running on PostgreSQL (`DATABASE_URL`), user records are automatically inserted into `user_configs` upon their first message.

#### Adjust a user's daily scan quota:
```sql
UPDATE user_configs 
SET daily_scan_limit = 20, session_ask_limit = 10 
WHERE user_id = '182736451928374650';
```

#### Give unlimited scans to a VIP or Moderator:
```sql
UPDATE user_configs 
SET daily_scan_limit = 999999 
WHERE user_id = '182736451928374650';
```

#### Reset a user's daily usage manually:
```sql
DELETE FROM user_daily_scans WHERE user_id = '182736451928374650';
```

---

### 2.2 Horizontal Scaling Architecture

For high-availability multi-replica deployments in Kubernetes or Docker Swarm:
- Set `REDIS_URL=redis://redis-cluster:6379/0`
- Set `DATABASE_URL=postgres://user:pass@pg-cluster:5432/macocr`

```text
               ┌───────────────────────┐
               │     Mezon Gateway     │
               └───────────┬───────────┘
                           │ WebSocket
              ┌────────────┴────────────┐
              ▼                         ▼
     ┌─────────────────┐       ┌─────────────────┐
     │ OmniScan Pod 1  │       │ OmniScan Pod 2  │
     └────────┬────────┘       └────────┬────────┘
              │                         │
              ├──────────► Redis ◄──────┤ (SETNX Dedup + Lua Quota)
              │                         │
              ├───────► PostgreSQL ◄────┤ (Persistent User Configs)
              │                         │
              └────────► MacOCR ◄───────┘ (Native OCR Cluster)
```

---

## 🔒 3. Security Specifications

1. **SSRF Filtering:** Binds to pre-flight DNS resolver. Disallows `localhost`, IPv4 loopback (`127.0.0.0/8`), Link-local (`169.254.0.0/16`), and Private IP ranges (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`).
2. **Payload Protection:** 100 MiB hard cap on attachments and image downloads.
3. **Data Lifecycle:** Document OCR texts in sessions automatically expire after 24 hours or upon reaching 5 questions.
