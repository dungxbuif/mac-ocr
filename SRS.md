# SRS — Mac OCR Platform
**Version**: 1.0 · **Status**: Final Draft · **Date**: 2026-08-12

---

## 1. Bối cảnh & Mục tiêu

Mac OCR Platform tận dụng Apple Vision trên macOS để cung cấp OCR chất lượng cao. Thay vì expose trực tiếp, một **proxy server** đứng ở giữa đảm nhiệm toàn bộ phần platform: auth, quota, job tracking, và hai giao diện đồng thời — REST API cho developer và MCP cho LLM/AI agent.

**Mục tiêu V1**:
1. Throughput tối đa từ Apple Vision bằng cách kiểm soát concurrency đúng chỗ (proxy, không phải Mac)
2. Developer experience tốt: docs tự generate, API key đơn giản
3. AI-native từ đầu: MCP là first-class interface, không phải add-on

**Ngoài phạm vi V1**: OAuth 2.1, multi-node Mac cluster, document layout reconstruction, LLM post-processing.

---

## 2. Actors & Use Cases

| Actor | Mô tả |
|-------|--------|
| **Admin** | Người vận hành platform. Tạo user, kiểm soát quota. Chỉ có 1 admin seed ban đầu. |
| **User** | Developer hoặc service sử dụng OCR. Tự quản lý API key trong giới hạn được cấp. |
| **API Client** | Bất kỳ HTTP client nào gọi REST API — server, script, tool. |
| **MCP Client** | LLM agent gọi OCR như một tool qua Model Context Protocol. |

### Admin use cases
- Tạo / disable user
- Gán và cập nhật rate limit + quota cho user

### User use cases
- Xem account info và usage
- Tạo, liệt kê, revoke API key — tự chọn limit ≤ account limit
- Submit ảnh để OCR (sync response với jobId, async kết quả)
- Xem trạng thái và kết quả OCR job

### MCP Client use cases
- Gọi tool `ocr` → nhận jobId
- Gọi tool `get_ocr_job` → nhận kết quả khi xong

---

## 3. Yêu cầu chức năng

### 3.1 Mac OCR Service

**Quyết định cốt lõi**: Mac Service là *thin executor* — không có queue, không có scheduler, không có business logic. Mọi thứ phức tạp nằm ở proxy.

#### OCR endpoint

```
POST /ocr
```

Request:
```json
{
  "requestId": "01J...",
  "imageUrl": "https://storage.example.com/img.jpg",
  "options": {
    "recognitionLevel": "accurate",
    "languages": ["vi-VN", "en-US"],
    "automaticallyDetectsLanguage": false,
    "usesLanguageCorrection": true,
    "customWords": ["Zalo", "MoMo"],
    "minimumTextHeight": null
  },
  "callbackUrl": "https://proxy.example.com/internal/ocr/callback"
}
```

ACK ngay (synchronous, trước khi OCR xong):
```json
{ "requestId": "01J...", "accepted": true }
```

Callback sau khi OCR xong (Mac POST đến `callbackUrl`):
```json
{
  "requestId": "01J...",
  "status": "completed",
  "text": "Hóa đơn số: 12345\nTổng cộng: 120.000đ",
  "blocks": [
    {
      "text": "Hóa đơn số: 12345",
      "confidence": 0.98,
      "boundingBox": { "x": 0.10, "y": 0.70, "width": 0.40, "height": 0.05 }
    }
  ]
}
```

**Options**: Map 1-1 sang Apple Vision native properties. Mac không invent thêm options kiểu "invoice mode".

**Output boundary**: Mac chỉ trả `text` (join) và `blocks[]` (serialize từ `VNRecognizedTextObservation`). Bounding box là normalized coordinates từ Vision, không convert. Không reorder, không layout reconstruction, không paragraph detection.

#### Health endpoint

```
GET /health
```

```json
{
  "status": "ready",
  "ocr": {
    "engine": "apple-vision",
    "recognitionLevel": "accurate",
    "maxConcurrency": 4,
    "active": 1,
    "available": 3
  }
}
```

`maxConcurrency` đến từ config. `available = maxConcurrency - active`. Không tự-tính từ CPU load.

#### Concurrency & overload

- Mac tự giữ semaphore với `maxConcurrency` slots
- Khi full (available = 0): trả `503 Service Unavailable` + `Retry-After: 1`
- Proxy chịu trách nhiệm retry, Mac không queue

#### Image preprocessing (nhẹ, không optional)

Trước khi gọi Vision:
1. Decode image từ URL
2. Normalize EXIF orientation
3. Downsample nếu > giới hạn kích thước cấu hình (mặc định: 4000px cạnh dài)

Không làm: brightness adjustment, binarization, deskew.

---

### 3.2 Proxy — User & Access Management

**Quyết định**: Không có self-registration. Không có org/team. Hai role cứng: `admin` và `user`.

#### Accounts

- Admin seed khi deploy (env hoặc migration seed)
- Admin tạo user bằng API, trả về password tạm thời (user đổi ngay lần đầu login — V2 có thể skip nếu dùng API key only)
- Admin disable user → tất cả key của user bị block ngay

User limits (admin set):
| Field | Ý nghĩa |
|-------|---------|
| `rate_limit_rpm` | Request/phút tối đa cho toàn bộ key của user |
| `quota_daily` | OCR jobs/ngày |
| `quota_monthly` | OCR jobs/tháng |

#### API Keys

- Format: `ocr_live_<22-char base62>` — prefix `ocr_live_` cho easy grep
- DB lưu: `key_prefix` (8 ký tự đầu để lookup), `key_hash` (HMAC-SHA256)
- Plaintext chỉ trả **một lần** khi tạo
- Key có thể set limit riêng, **không vượt** account limit
- Overcommit cho phép: user có 10k daily quota có thể tạo key A=8k và key B=8k
- Runtime: `effective_allowance = min(key_remaining, user_remaining)`
- Scopes: `ocr:execute`, `ocr:read` (default: cả hai khi tạo)
- Key có `expires_at` optional

---

### 3.3 Proxy — Authentication

#### API Key auth (mọi OCR call)

```
Authorization: Bearer ocr_live_xxxxxxxxxxxxxxxxxxxxxx
```

Pipeline:
```
Extract prefix → lookup key → verify HMAC → check key.status=active
→ check user.status=active → build Principal
```

**Principal** là abstraction dùng chung cho REST và MCP:
```go
type Principal struct {
    UserID        string
    KeyID         string
    Role          Role          // admin | user
    Scopes        []Scope
    EffectiveLimits struct {
        RPM          int
        QuotaDaily   int
        QuotaMonthly int
    }
}
```

Service layer nhận `Principal`, không biết request đến từ REST hay MCP.

#### Admin login (chỉ dùng cho admin dashboard/CLI)

```
POST /api/auth/login  →  session token (JWT, 24h)
```

Password: Argon2id. Admin không cần API key để dùng admin endpoints.

---

### 3.4 Proxy — Rate Limiting & Quota

**Quyết định**: Rate limit (RPM) dùng sliding window in-memory (Redis nếu multi-instance, in-process nếu single). Quota (daily/monthly) persist vào PostgreSQL.

Enforcement order (fail fast, theo thứ tự):
1. Key rate limit (RPM)
2. Account rate limit (RPM)
3. Key daily quota
4. Account daily quota
5. Key monthly quota
6. Account monthly quota

Quota accounting:
- **Reserve khi accept** (không chờ OCR xong)
- Failed job do infrastructure → **auto refund**
- Failed job do content (Vision không nhận dạng được) → **charge** (Vision đã chạy)
- 1 image accepted = 1 unit (V1 — không tính pixel hay megabyte)

---

### 3.5 Proxy — OCR Job Management

**Quyết định**: Job ID = ULID (sortable, URL-safe). Cùng ID xuyên suốt: DB record, Mac `requestId`, MCP `jobId`.

#### Job lifecycle

```
accepted → queued → dispatched → processing → completed
                                             → failed (retriable)
                                             → failed (terminal)
```

- `failed (retriable)`: Mac 503, network error, callback timeout → proxy auto-retry với backoff
- `failed (terminal)`: Vision error không recover được → stop, charge refunded

#### Multi-page documents

Proxy nhận PDF → split thành page images → tạo N job records → dispatch độc lập. Page fail → chỉ retry page đó. Mac không biết document context.

#### Callback timeout

Nếu Mac accept job nhưng callback không đến trong `callbackTimeoutSeconds` (config, mặc định 60s): proxy đánh dấu job `failed (retriable)`, retry dispatch.

---

### 3.6 Proxy — REST API

| Method | Path | Auth | Mô tả |
|--------|------|------|--------|
| `POST` | `/api/auth/login` | — | Admin login |
| `GET` | `/api/account` | key/session | Account info |
| `GET` | `/api/account/usage` | key/session | Usage stats |
| `POST` | `/api/keys` | session | Tạo API key |
| `GET` | `/api/keys` | session | Liệt kê keys |
| `GET` | `/api/keys/:id` | session | Chi tiết key |
| `PATCH` | `/api/keys/:id` | session | Update key (limit, name, status) |
| `DELETE` | `/api/keys/:id` | session | Revoke key |
| `POST` | `/api/ocr` | key | Submit OCR job |
| `GET` | `/api/jobs/:id` | key/session | Get job status + result |
| `POST` | `/api/admin/users` | admin | Tạo user |
| `GET` | `/api/admin/users` | admin | Liệt kê users |
| `GET` | `/api/admin/users/:id` | admin | Chi tiết user |
| `PATCH` | `/api/admin/users/:id` | admin | Update user (limit, status) |
| `POST` | `/internal/ocr/callback` | internal secret | Mac callback |

Internal callback dùng shared secret header, không dùng Bearer key.

---

### 3.7 Proxy — MCP Interface

**Quyết định**: Streamable HTTP transport (MCP spec 2026-07-28), stateless. Không SSE. Endpoint duy nhất `POST /mcp`.

Auth: Bearer API key — cùng key hệ thống, cùng quota pool với REST. Không tạo credential riêng cho MCP.

V1 tools:

| Tool | Input | Output |
|------|-------|--------|
| `ocr` | `imageUrl` (required) + tất cả Vision options | `{ jobId, status: "queued" }` |
| `get_ocr_job` | `jobId` | `{ jobId, status, text?, blocks? }` |
| `get_ocr_capabilities` | — | Engine info, supported options, current load |

MCP không expose: create_user, create_key, update_quota — đây là admin control plane, không phải compute tool.

OCR là async — `ocr` tool trả jobId ngay, agent gọi `get_ocr_job` để poll. Không block tool call chờ Vision xong.

---

### 3.8 Proxy — Docs

```
GET /docs          → Scalar/Huma UI, auto-generated
GET /openapi.json  → OpenAPI 3.1 spec
```

Huma generate từ route definitions. Không viết tay.

---

## 4. Yêu cầu phi chức năng

| ID | Category | Yêu cầu |
|----|----------|---------|
| NFR-01 | Performance | Proxy auth + quota check overhead < 5ms p99 |
| NFR-02 | Performance | Mac slot available → dispatch next image trong < 50ms |
| NFR-03 | Reliability | Proxy retry tối đa 3 lần với exponential backoff trước khi mark terminal failed |
| NFR-04 | Reliability | Callback timeout 60s, configurable |
| NFR-05 | Security | API key plaintext chỉ hiển thị một lần |
| NFR-06 | Security | Password Argon2id, cost params không thấp hơn OWASP minimum |
| NFR-07 | Security | Internal callback endpoint validate shared secret |
| NFR-08 | Observability | Mỗi OCR job log đủ: requestId, latency, status, quota_cost |
| NFR-09 | Operability | `maxConcurrency` configurable không cần redeploy Mac Service |
| NFR-10 | Correctness | Quota deducted atomically với job creation (no race condition) |

---

## 5. Constraints

- Mac OCR Service **chỉ chạy trên macOS** (Apple Vision requirement)
- `maxConcurrency` tối ưu phụ thuộc hardware — cần benchmark, không hardcode 4 làm "đúng"
- V1 single Mac node — multi-node là V2 problem
- MCP V1 dùng Bearer key, không support OAuth 2.1 MCP flow

---

## 6. V2 Backlog

| Item | Lý do defer |
|------|------------|
| OAuth 2.1 MCP authorization | Chỉ cần khi cho phép MCP client công cộng |
| Dynamic effectiveCapacity (thermal pressure) | Cần đo lường trước khi implement |
| Multi-node Mac cluster | V1 single node đủ để prove concept |
| OCR unit cost theo image size | Cần data usage thực tế trước |
| MCP Tasks extension | Spec mới, chưa stable đủ cho V1 |
| attemptId cho retry tracking | Nice-to-have, không block V1 |
