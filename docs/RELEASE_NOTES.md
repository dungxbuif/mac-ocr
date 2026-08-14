# Release Notes — v1.0.0

**Release Date:** August 15, 2026

---

## 🚀 Overview

High-performance, asynchronous OCR backend platform powered by the macOS Apple Vision OCR Engine. The system connects clients to local or remote macOS OCR workers through a secure Go proxy, S3-compatible object storage, PostgreSQL, and Redis.

---

## ✨ Key Features

### ⚡ Asynchronous OCR Pipeline

- **Asynchronous Processing:** Submissions return `202 Accepted` with durable server-generated document IDs.
- **Multi-Format Support:** Content detection via magic bytes for `JPEG`, `PNG`, `TIFF`, `WebP`, and `PDF`.
- **Flexible Payload Envelopes:** Accepts Base64 encoded payload or public/presigned HTTPS URLs.
- **Batch Processing:** Submit 1–100 items per batch request (`POST /v1/batches`) processed in a single transaction with individual document ID tracking.

### 🛡️ Large File Presigned S3 Uploads

- **Direct S3 Uploads:** `POST /v1/uploads/presign` issues authenticated a`PUT uploadUrl` for large files (up to 100 MiB), eliminating Base64 decoding overhead.
- **Multi-Layer Validation:** Enforces file-size limits at presign issuance, re-verified via `HEAD` metadata and bounded streaming before OCR execution.

### 🔐 Security, Authentication & Quotas

- **API Key Security:** Protected endpoints require `Bearer sk_ocr_<48-hex-chars>`. Credentials are SHA-256 hashed and cached in Redis.
- **Admin Control Plane:** Embedded React Admin Console (`/admin/`) and CLI tool (`macocr-admin`) for quota management, rate limiting, and key revocation.
- **Instant Account Deactivation:** `POST /v1/users/{userId}/deactivate` revokes active sessions and API keys immediately across PostgreSQL and Redis.
- **Worker Webhook Security:** Worker callbacks require HMAC SHA-256 signatures over `nodeId.timestamp.eventId.<raw-body>`.

### 🤖 Model Context Protocol (MCP) for AI Agents

- **Native MCP Streamable HTTP:** Endpoint `/mcp` implements protocol revision `2025-11-25`.
- **Agent Tools & Resources:** Exposes `submit_ocr_document`, `submit_ocr_batch`, `get_ocr_document`, and `ocr://documents/{documentId}` resource URIs.
- **Push Notifications:** Emits `notifications/tasks/status` and `notifications/resources/updated` via SSE stream.

### 🔔 Notifications & Events

- **Webhooks:** Signed HTTP callbacks with AES-GCM encrypted secrets at rest and exponential backoff retry.
- **SSE Stream:** Real-time event streaming via `GET /v1/events` supporting `Last-Event-ID` reconnection.

### 📚 Embedded Docs & Developer Tools

- **Swagger UI:** Served live at `/api/v1/docs`.
- **OpenAPI 3.1 Contract:** Served at `/api/v1/openapi.json`.
- **Built-in Docs:** Embedded documentation site served at `/`.

### 🐳 Containerization

- **Multi-Stage Dockerfile:** Root `Dockerfile` builds frontend static assets (Admin UI + Docs) and static Go binaries (`macocr-proxy`, `macocr-admin`).
- **Build Arguments:** Configurable `ARG` parameters for `PUBLIC_API_BASE_URL`, `PUBLIC_DOCS_BASE_URL`, `APP_ENV`, `VERSION`, and `GIT_COMMIT`.

---

## 🛠️ API Reference

| Method     | Endpoint                     | Purpose                                       |
| :--------- | :--------------------------- | :-------------------------------------------- |
| `POST`     | `/v1/documents`              | Submit single document for OCR                |
| `GET`      | `/v1/documents/{documentId}` | Get document status & cached result           |
| `POST`     | `/v1/batches`                | Submit 1–100 document batch                   |
| `POST`     | `/v1/uploads/presign`        | Issue presigned S3 upload URL for large files |
| `GET`      | `/v1/events`                 | Stream account events via SSE                 |
| `POST/GET` | `/mcp`                       | MCP agent protocol endpoint                   |
| `GET`      | `/healthz` / `/readyz`       | Liveness and readiness health checks          |

---

## 🐳 Deployment Quick Start

```bash
# Build Docker image
docker build \
  --build-arg PUBLIC_API_BASE_URL="https://ocr.example.com" \
  --build-arg PUBLIC_DOCS_BASE_URL="https://docs.example.com" \
  -t macocr-proxy:latest .

# Run container
docker run -d \
  -p 8080:8080 \
  --env-file proxy/.env \
  macocr-proxy:latest
```
