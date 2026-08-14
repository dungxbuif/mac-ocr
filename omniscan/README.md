# OmniScan — Mezon Bot Service for MacOCR

**OmniScan** is an automated Mezon platform bot written in Go that connects to the `mac-ocr` proxy backend. It listens to Mezon chat channels, detects document/image URLs or file attachments, extracts text via OCR, and replies directly to users in Mezon channels.

---

## 🛠️ Features & Architecture

- **Security & Pre-Call Validation Layer**: Scheme checking, SSRF/Private IP blocking, file format validation (`.png`, `.jpg`, `.jpeg`, `.webp`, `.tiff`, `.pdf`), and 100 MiB attachment size limit.
- **Horizontal Scaling & Redis Cluster**: Multi-replica ready using Redis atomic Lua scripts for 5 scans/day daily quota enforcement, `SETNX` message deduplication, and L2 shared caching across replicas.
- **Fallback Single-Instance Mode**: Zero-dependency embedded SQLite (`omniscan.db`) and in-memory deduplicator when running without Redis.
- **Fixed Balanced OCR**: Default `accurate` recognition level with `vi-VN` + `en-US` language support.

---

## 💬 Bot Commands in Mezon

- `!ocr <url>` or `/ocr <url>` — Runs OCR on the specified image/PDF URL or attached file.
- `!quota` or `/quota` — Displays user's daily remaining scan quota (5 scans/day).
- `!ping` or `/ping` — Connection and bot healthcheck.
- `!help` or `/help` — Displays bot capabilities and command usages.

---

## 🚀 Quick Start

1. **Configure Environment:**
   ```bash
   cd omniscan
   cp .env.example .env
   ```
   Edit `.env` with your `MEZON_BOT_ID`, `MEZON_BOT_TOKEN`, and `OCR_API_KEY`. Optionally set `REDIS_URL` for multi-replica horizontal scaling.

2. **Run OmniScan Service:**
   ```bash
   cd omniscan
   go run .
   ```
