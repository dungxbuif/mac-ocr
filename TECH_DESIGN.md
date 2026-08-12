# Technical Design — Mac OCR Platform
**Version**: 1.0 · **Status**: Final Draft · **Date**: 2026-08-12

---

## 1. System Overview

```
┌─────────────────────────────────────────────────────────┐
│                      Internet                           │
└───────────────────────────┬─────────────────────────────┘
                            │ HTTPS
                            ▼
┌─────────────────────────────────────────────────────────┐
│                    Proxy Server (Go)                    │
│                                                         │
│  /docs  /openapi.json                                   │
│  /api/*  (REST — Huma)                                  │
│  /mcp    (MCP — go-sdk, Streamable HTTP)                │
│  /internal/*  (Mac callback, internal only)             │
│                                                         │
│  ┌──────────┐  ┌───────────┐  ┌────────────────────┐  │
│  │   Auth   │  │   Quota   │  │    OCR Service     │  │
│  └──────────┘  └───────────┘  └────────────────────┘  │
│                      │                  │               │
│               ┌──────┴──────────────────┘               │
│               ▼                                         │
│          PostgreSQL                                     │
└───────────────────────────┬─────────────────────────────┘
                            │ HTTP (internal network)
                            ▼
┌─────────────────────────────────────────────────────────┐
│                Mac OCR Service (Swift)                  │
│                                                         │
│  POST /ocr          GET /health                         │
│                                                         │
│  Semaphore(maxConcurrency)                              │
│     │                                                   │
│  Apple Vision Framework                                 │
│  VNRecognizeTextRequest (.accurate)                     │
└─────────────────────────────────────────────────────────┘
```

---

## 2. Mac OCR Service

### 2.1 Technology

| Item | Quyết định | Lý do |
|------|-----------|-------|
| Language | **Swift** | Native Apple Vision access, không wrapper overhead |
| HTTP Server | **Hummingbird** (hoặc Vapor) | Lightweight Swift HTTP server, đủ cho 2 endpoints |
| Concurrency | **Swift Concurrency (async/await + actor)** | Native, safe semaphore pattern |
| Config | Environment variables | Simple, container/launchd friendly |

### 2.2 Concurrency Model

```swift
actor OCRSlotManager {
    let maxConcurrency: Int  // from config
    private var active: Int = 0

    func tryAcquire() -> Bool {
        guard active < maxConcurrency else { return false }
        active += 1
        return true
    }

    func release() {
        active -= 1
    }

    var available: Int { maxConcurrency - active }
}
```

- `POST /ocr` → `tryAcquire()` → nếu false → 503
- OCR xong (bất kể success/fail) → `release()`
- Không có queue trong Mac Service. Queue là việc của proxy.

### 2.3 OCR Pipeline

```
POST /ocr received
    │
    ├─ tryAcquire() → false → 503 Retry-After: 1
    │
    ├─ ACK { accepted: true }  ← trả ngay, không chờ OCR
    │
    └─ Task.detached {
           download(imageUrl)
               → decode CGImage
               → normalize orientation (CGImagePropertyOrientation)
               → resize if needed (max edge from config)
               → VNImageRequestHandler(cgImage)
               → VNRecognizeTextRequest(options)
               → .perform()
               → serialize observations
               → POST callbackUrl
               → release()
       }
```

Tại sao trả ACK trước? Vì Vision `.accurate` có thể mất 1–5 giây. Client cần biết request đã được nhận ngay, không timeout.

### 2.4 Vision Configuration

```swift
let request = VNRecognizeTextRequest()
request.recognitionLevel    = options.recognitionLevel  // .accurate | .fast
request.recognitionLanguages = options.languages
request.automaticallyDetectsLanguage = options.automaticallyDetectsLanguage
request.usesLanguageCorrection       = options.usesLanguageCorrection
request.customWords                  = options.customWords
if let minHeight = options.minimumTextHeight {
    request.minimumTextHeight = Float(minHeight)
}
```

### 2.5 Response Serialization

```swift
struct Block: Codable {
    let text: String
    let confidence: Float
    let boundingBox: BoundingBox  // normalized, từ VNRecognizedTextObservation.boundingBox
}

// observation.topCandidates(1).first
// observation.boundingBox → CGRect normalized (origin bottom-left, Vision space)
// Quyết định: giữ nguyên Vision coordinate space, proxy/client tự convert nếu cần
```

Không invert Y-axis tại Mac. Document rõ trong API docs rằng boundingBox dùng Vision coordinate system (origin bottom-left).

### 2.6 Config (Environment Variables)

| Var | Default | Mô tả |
|-----|---------|--------|
| `OCR_MAX_CONCURRENCY` | `4` | Số slot đồng thời |
| `OCR_MAX_IMAGE_EDGE` | `4000` | Pixel, cạnh dài tối đa trước khi resize |
| `OCR_DEFAULT_LEVEL` | `accurate` | Recognition level mặc định |
| `OCR_CALLBACK_TIMEOUT_MS` | `5000` | Timeout khi POST callback |
| `OCR_PORT` | `8080` | Port |

### 2.7 Error Callback

Nếu OCR fail (download fail, decode fail, Vision error):
```json
{
  "requestId": "01J...",
  "status": "failed",
  "error": {
    "code": "DOWNLOAD_FAILED",
    "message": "HTTP 404 fetching image"
  }
}
```

Error codes: `DOWNLOAD_FAILED`, `DECODE_FAILED`, `VISION_ERROR`, `TIMEOUT`.

---

## 3. Proxy Server

### 3.1 Technology Stack

| Item | Quyết định | Lý do |
|------|-----------|-------|
| Language | **Go 1.23+** | Excellent HTTP, concurrency, deployment |
| HTTP + OpenAPI | **Huma v2** | Auto-generate OpenAPI 3.1, Scalar docs, type-safe handlers |
| Router | **chi** (via Huma) | Lightweight, idiomatic |
| Database | **PostgreSQL 16** | ACID, advisory locks cho quota |
| DB Driver | **pgx/v5** | Fastest Go PostgreSQL driver |
| Query Gen | **sqlc** | Type-safe SQL, không ORM magic |
| Migrations | **golang-migrate** | Simple, reversible |
| MCP | **modelcontextprotocol/go-sdk** | Official SDK |
| Rate limiting | **In-process sliding window** | Single node V1, đủ cho Redis swap sau |
| Job ID | **ULID** (github.com/oklog/ulid) | Sortable, URL-safe, không cần DB sequence |
| Password | **argon2id** (golang.org/x/crypto) | OWASP recommended |
| Config | **env + .env file** | Simple, 12-factor |

### 3.2 Project Structure

```
mac-ocr-proxy/
├── cmd/
│   └── server/
│       └── main.go          # wire everything, start server
│
├── internal/
│   ├── auth/
│   │   ├── service.go       # LookupByKey(), CreateSession(), VerifySession()
│   │   ├── middleware.go    # HTTP middleware → Principal
│   │   └── principal.go    # Principal type + EffectiveLimits
│   │
│   ├── users/
│   │   ├── service.go       # CreateUser(), UpdateUser(), DisableUser()
│   │   └── repository.go   # DB queries
│   │
│   ├── keys/
│   │   ├── service.go       # CreateKey(), RevokeKey(), ValidateKeyLimits()
│   │   ├── generator.go    # ocr_live_<base62> generation + HMAC
│   │   └── repository.go
│   │
│   ├── quota/
│   │   ├── service.go       # CheckAndReserve(), Refund(), Charge()
│   │   ├── ratelimiter.go  # In-process sliding window (RPM)
│   │   └── repository.go   # usage_counters DB ops
│   │
│   ├── ocr/
│   │   ├── service.go       # Submit(), GetJob() — core business logic
│   │   ├── models.go        # OCRJob, OCRInput, OCRResult
│   │   └── repository.go   # ocr_jobs DB ops
│   │
│   ├── worker/
│   │   ├── dispatcher.go   # Sliding window dispatch to Mac nodes
│   │   ├── mac_client.go   # HTTP client for Mac OCR Service
│   │   └── retry.go        # Exponential backoff retry logic
│   │
│   ├── api/
│   │   ├── admin.go         # Admin handlers (Huma)
│   │   ├── account.go       # Account handlers
│   │   ├── keys.go          # Key management handlers
│   │   ├── ocr.go           # OCR submit + job query
│   │   └── callback.go     # Internal Mac callback receiver
│   │
│   └── mcp/
│       ├── server.go        # MCP server setup, tool registration
│       └── tools.go         # ocr tool, get_ocr_job tool — call ocr.Service
│
├── db/
│   ├── migrations/          # golang-migrate SQL files
│   └── queries/             # sqlc .sql files
│
└── config/
    └── config.go            # Config struct, env loading
```

**Nguyên tắc**: `api/` và `mcp/` chỉ là adapters. Business logic ở `ocr/service.go`, `quota/service.go`, `auth/service.go`. Không duplicate.

### 3.3 Database Schema

```sql
-- users
CREATE TABLE users (
    id              TEXT PRIMARY KEY,  -- ULID
    username        TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,
    role            TEXT NOT NULL CHECK (role IN ('admin', 'user')),
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    rate_limit_rpm  INT NOT NULL DEFAULT 60,
    quota_daily     INT NOT NULL DEFAULT 1000,
    quota_monthly   INT NOT NULL DEFAULT 20000,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- api_keys
CREATE TABLE api_keys (
    id              TEXT PRIMARY KEY,  -- ULID
    user_id         TEXT NOT NULL REFERENCES users(id),
    name            TEXT NOT NULL,
    key_prefix      TEXT NOT NULL UNIQUE,  -- first 8 chars, for lookup
    key_hash        TEXT NOT NULL,         -- HMAC-SHA256
    scopes          TEXT[] NOT NULL DEFAULT ARRAY['ocr:execute', 'ocr:read'],
    rate_limit_rpm  INT NOT NULL,
    quota_daily     INT NOT NULL,
    quota_monthly   INT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ
);

-- usage_counters (upsert pattern)
CREATE TABLE usage_counters (
    entity_type  TEXT NOT NULL,  -- 'user' | 'key'
    entity_id    TEXT NOT NULL,
    period       DATE NOT NULL,  -- daily: exact date, monthly: first of month
    period_type  TEXT NOT NULL CHECK (period_type IN ('daily', 'monthly')),
    count        INT NOT NULL DEFAULT 0,
    PRIMARY KEY (entity_type, entity_id, period, period_type)
);

-- ocr_jobs
CREATE TABLE ocr_jobs (
    id              TEXT PRIMARY KEY,  -- ULID — cũng là Mac requestId
    user_id         TEXT NOT NULL REFERENCES users(id),
    api_key_id      TEXT REFERENCES api_keys(id),
    status          TEXT NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued', 'dispatched', 'processing', 'completed', 'failed')),
    request_payload JSONB NOT NULL,
    result_text     TEXT,
    result_blocks   JSONB,
    error_code      TEXT,
    error_message   TEXT,
    quota_cost      INT NOT NULL DEFAULT 1,
    retry_count     INT NOT NULL DEFAULT 0,
    is_quota_refunded BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    dispatched_at   TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ
);

CREATE INDEX idx_ocr_jobs_user_id ON ocr_jobs(user_id);
CREATE INDEX idx_ocr_jobs_status ON ocr_jobs(status) WHERE status IN ('queued', 'dispatched');
```

**Lý do ULID**: Sortable theo thời gian, URL-safe, không cần DB sequence/serial. Job ID đồng thời là `requestId` gửi đến Mac — không cần mapping thêm.

### 3.4 API Key Design

```
Key format:  ocr_live_<22 ký tự base62>
Ví dụ:       ocr_live_7Kx9mNpQrStUvWxYzAb3

key_prefix = "ocr_live_7Kx"   (11 ký tự đầu, đủ unique cho lookup)
key_hash   = HMAC-SHA256(full_key, server_secret)
```

Lookup flow:
```go
// 1. Extract prefix từ Authorization header
prefix := key[:11]

// 2. SELECT FROM api_keys WHERE key_prefix = $1
row := db.GetKeyByPrefix(ctx, prefix)

// 3. Verify hash
expected := hmac.SHA256(fullKey, serverSecret)
if !hmac.Equal(expected, row.KeyHash) → 401
```

Tại sao HMAC thay vì bcrypt? Key lookup cần tốc độ — HMAC O(1), bcrypt O(cost). API key đủ dài (22 base62 ≈ 131 bits entropy) nên không cần slow hash.

### 3.5 Quota — Atomic Reserve

Tránh race condition bằng PostgreSQL advisory lock hoặc `FOR UPDATE`:

```sql
-- Trong một transaction:
SELECT count FROM usage_counters
WHERE entity_type = 'key' AND entity_id = $1
AND period = CURRENT_DATE AND period_type = 'daily'
FOR UPDATE;

-- Nếu count + cost > limit → rollback → 429

INSERT INTO usage_counters ... ON CONFLICT ... DO UPDATE SET count = count + $cost;
```

Hoặc dùng Lua script nếu upgrade lên Redis later. V1: PostgreSQL đủ cho single node.

### 3.6 Dispatcher (Sliding Window)

```go
type MacNode struct {
    BaseURL    string
    MaxSlots   int
    semaphore  chan struct{}
}

func NewMacNode(baseURL string, maxSlots int) *MacNode {
    sem := make(chan struct{}, maxSlots)
    for i := 0; i < maxSlots; i++ {
        sem <- struct{}{}  // pre-fill
    }
    return &MacNode{BaseURL: baseURL, MaxSlots: maxSlots, semaphore: sem}
}

func (n *MacNode) Dispatch(ctx context.Context, job *OCRJob) error {
    // Acquire slot (non-blocking — slot pre-reserved khi accept job)
    select {
    case <-n.semaphore:
        defer func() { n.semaphore <- struct{}{} }()
    default:
        return ErrNoSlotAvailable
    }
    return n.client.PostOCR(ctx, job)
}
```

**Quyết định**: Semaphore giữ ở proxy, không poll `/health` trước mỗi dispatch. Mac `/health` chỉ dùng cho monitoring, không phải scheduling signal.

### 3.7 Retry Logic

```
Attempt 1 → fail → wait 1s
Attempt 2 → fail → wait 2s
Attempt 3 → fail → mark terminal failed, refund quota
```

Retriable: `503`, network error, callback timeout.
Terminal: `400` từ Mac (bad request), Vision `VISION_ERROR` với non-transient code.

### 3.8 MCP Integration

```go
// Dùng official modelcontextprotocol/go-sdk

server := mcp.NewServer("mac-ocr", "1.0")

server.AddTool(mcp.Tool{
    Name:        "ocr",
    Description: "Extract text from an image using Apple Vision OCR on macOS.",
    InputSchema: OCRInput{},  // same struct as REST
}, func(ctx context.Context, args OCRInput) (*mcp.ToolResult, error) {
    principal := principalFromCtx(ctx)  // set by MCP auth middleware
    job, err := ocrService.Submit(ctx, principal, args)
    if err != nil { return nil, err }
    return mcp.TextResult(fmt.Sprintf(`{"jobId":"%s","status":"queued"}`, job.ID)), nil
})

server.AddTool(mcp.Tool{
    Name:        "get_ocr_job",
    Description: "Get the status and result of an OCR job.",
    InputSchema: struct{ JobID string `json:"jobId"` }{},
}, func(ctx context.Context, args struct{ JobID string `json:"jobId"` }) (*mcp.ToolResult, error) {
    principal := principalFromCtx(ctx)
    job, err := ocrService.GetJob(ctx, principal, args.JobID)
    // ...
})
```

MCP auth middleware giống REST middleware — extract Bearer token → Principal → inject vào context.

### 3.9 Internal Callback

Mac POST đến `/internal/ocr/callback` với header:
```
X-Internal-Secret: <shared secret from env>
```

Handler:
```go
func handleCallback(ctx context.Context, body CallbackBody) error {
    // 1. Verify X-Internal-Secret
    // 2. Load job from DB
    // 3. Update job status + result
    // 4. If failed → refund quota
    // 5. Release dispatcher slot (nếu slot bị giữ)
}
```

### 3.10 Huma Route Setup

```go
huma.Register(api, huma.Operation{
    OperationID: "submit-ocr",
    Method:      http.MethodPost,
    Path:        "/api/ocr",
    Tags:        []string{"OCR"},
    Security:    []map[string][]string{{"bearerAuth": {"ocr:execute"}}},
}, func(ctx context.Context, input *OCRSubmitInput) (*OCRSubmitOutput, error) {
    principal := auth.MustPrincipal(ctx)
    job, err := ocrSvc.Submit(ctx, principal, input.Body)
    // ...
})
```

Huma tự generate OpenAPI 3.1 từ Go structs và operation definitions.

---

## 4. Data Flow — End to End

### 4.1 REST OCR Submit

```
Client: POST /api/ocr
    Authorization: Bearer ocr_live_xxx
    Body: { imageUrl, options }

Proxy:
    1. Extract key prefix → DB lookup → verify HMAC
    2. Check key.status, user.status
    3. Check rate limit (in-memory sliding window)
    4. BEGIN TRANSACTION
       - Check + reserve quota (daily + monthly, key + user)
       - INSERT ocr_jobs (status=queued)
    5. COMMIT
    6. → Response 202: { jobId: "01J...", status: "queued" }
    7. Dispatcher picks up job:
       - Acquire semaphore slot
       - POST to Mac: { requestId: jobId, imageUrl, options, callbackUrl }
       - Update job status → dispatched
    8. Mac ACK { accepted: true }
    9. Update job → processing

Mac (async):
    10. Download + decode + resize image
    11. VNRecognizeTextRequest.perform()
    12. Serialize observations
    13. POST /internal/ocr/callback: { requestId, status: completed, text, blocks }

Proxy callback handler:
    14. Verify X-Internal-Secret
    15. Update ocr_jobs: status=completed, result_text, result_blocks, completed_at
    16. Release semaphore slot → dispatcher picks next job

Client polls:
    GET /api/jobs/01J... → { status: "completed", text: "...", blocks: [...] }
```

### 4.2 MCP OCR Call

```
MCP Client → POST /mcp
    Authorization: Bearer ocr_live_xxx
    Body: { jsonrpc MCP tool call: "ocr", args: { imageUrl } }

Proxy MCP middleware:
    → Same auth pipeline → Principal

MCP tool handler:
    → ocrService.Submit(ctx, principal, input)
    → Returns { jobId, status: "queued" }

MCP Client → POST /mcp
    Body: { tool call: "get_ocr_job", args: { jobId } }

MCP tool handler:
    → ocrService.GetJob(ctx, principal, jobId)
    → Returns job status/result
```

Quota được deduct tại `ocrService.Submit()` — cùng code path cho REST và MCP.

---

## 5. Security

| Concern | Approach |
|---------|----------|
| API key storage | HMAC-SHA256, không lưu plaintext |
| Key entropy | 22 ký tự base62 ≈ 131 bits, brute-force không khả thi |
| Admin password | Argon2id, memory=64MB, iterations=3, parallelism=2 |
| Internal callback | Shared secret header, endpoint không expose ra ngoài |
| SQL injection | sqlc generate parameterized queries, không string concat |
| Key exposure | Log không được log Authorization header value |

---

## 6. Observability

### 6.1 Structured Logging

Mỗi request log JSON với fields:
- `request_id` (trace ID, không phải OCR job ID)
- `ocr_job_id` (nếu có)
- `user_id`, `key_id`
- `status_code`, `latency_ms`
- `quota_cost`

### 6.2 Metrics (V1 minimal)

Export Prometheus metrics:
- `ocr_jobs_total{status}` — counter
- `ocr_job_duration_seconds` — histogram
- `ocr_slots_active` — gauge (từ Mac `/health` hoặc dispatcher state)
- `quota_checks_total{result}` — rate limit / quota exceeded counter

### 6.3 Mac Health Check

Proxy background goroutine poll Mac `/health` mỗi 30s. Chỉ dùng để alert/monitoring — không dùng để scheduling.

---

## 7. Deployment

### 7.1 Mac OCR Service

```
macOS machine (Mac Mini recommended)
├── Mac OCR Service binary (Swift)
│   └── Managed by launchd (plist)
└── Config via environment (.env hoặc launchd EnvironmentVariables)
```

Exposed chỉ trên internal network, không expose ra internet.

### 7.2 Proxy Server

```
Linux server (hoặc Mac nếu cần)
├── Proxy binary (Go, static binary)
├── PostgreSQL 16
└── Managed by systemd
```

Hoặc Docker Compose nếu muốn containerize.

### 7.3 Network Topology

```
Internet → HTTPS → Proxy (public IP / Cloudflare)
Proxy → HTTP (internal) → Mac OCR Service (LAN / Tailscale)
Mac OCR Service → HTTPS → Proxy /internal/ocr/callback
```

---

## 8. Key Decisions & Rationale

| Decision | Alternatives considered | Rationale |
|----------|------------------------|-----------|
| 1 image/request (không batch HTTP) | Batch 10-100 images/request | Vision xử lý từng image riêng, batch HTTP không cải thiện compute. Error handling đơn giản hơn nhiều |
| Proxy giữ semaphore, không poll Mac capacity | Poll /health trước mỗi dispatch | Ít latency hơn, không cần round-trip để biết có slot không |
| ULID làm job ID | UUID, DB serial | Sortable theo thời gian, một ID dùng xuyên suốt mọi layer |
| Quota reserve khi accept | Reserve khi complete | Tránh overselling. Refund nếu infra fail. |
| HMAC cho API key verify | bcrypt/scrypt | Key đủ entropy → không cần slow hash. HMAC O(1) vs bcrypt O(cost) |
| MCP dùng chung Bearer key | Tạo MCP-specific credential | Cùng quota pool, đơn giản hơn, không confuse user |
| ACK ngay, callback async | Long-poll, WebSocket | Vision accurate có thể mất vài giây. ACK tránh HTTP timeout |
| Không batch endpoint trên Mac | /ocr/batch | Đưa scheduler về Mac là sai: Mac phải tự handle queue, error, retry — phức tạp hóa component đơn giản nhất |
