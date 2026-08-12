# Technical Design — Mac OCR Platform

**Status:** Design Baseline
**Date:** 2026-08-12
**Related:** [SRS.md](SRS.md), [BENCHMARK.md](BENCHMARK.md)

---

## 1. Architecture

```text
 REST services                         MCP agents
      │ HTTPS                              │ Streamable HTTP
      └────────────────┬───────────────────┘
                       ▼
┌──────────────────────────────────────────────────────────┐
│                    Public Go Proxy                       │
│                                                          │
│ REST/MCP adapters → Document Service → Durable Scheduler │
│         │                  │                  │           │
│ HATEOAS │             PostgreSQL          Redis cache     │
│         │                  │                              │
│ Uploads/results ─────────── S3                             │
│                                                          │
│ / → Docusaurus docs       /admin → Admin console         │
└──────────────────────┬───────────────────▲───────────────┘
                       │ POST /ocr         │ signed webhook
                       ▼                   │
┌──────────────────────────────────────────────────────────┐
│ Native LaunchAgent (Swift)                               │
│ Atomic admission → ResourceGovernor → Apple Vision       │
│ Local callback outbox → result/capacity webhook           │
└──────────────────────────▲───────────────────────────────┘
                           │ local authenticated XPC
┌──────────────────────────┴───────────────────────────────┐
│ MacOCR.app — SwiftUI MenuBarExtra                        │
│ first-run Proxy setup, connectivity test, status/control │
└──────────────────────────────────────────────────────────┘
```

Proxy and Native can reach one another directly. The transport/tunnel mechanism is deployment-specific.

---

## 2. Technology stack

### 2.1 Proxy

| Concern | Choice |
|---|---|
| Language | Go 1.23+ |
| HTTP/OpenAPI | Huma v2 over chi |
| Database | PostgreSQL 16+ |
| Driver/queries | pgx/v5 + sqlc |
| Migrations | golang-migrate |
| Cache/rate limits | Redis |
| Blob storage | S3 API |
| MCP | Official `modelcontextprotocol/go-sdk` |
| IDs | ULID with type prefixes in serialized form |
| Password | Argon2id |
| API key verification | HMAC-SHA256 |
| Human docs | Docusaurus, Markdown/MDX, docs-only static build |

### 2.2 Native

| Concern | Choice |
|---|---|
| Language | Swift 6+ |
| Desktop UI | SwiftUI `MenuBarExtra` using window style |
| Background lifecycle | Per-user LaunchAgent registered through `SMAppService` |
| Local IPC | Authenticated XPC with a narrow typed protocol |
| Secret storage | macOS Keychain |
| HTTP server | Hummingbird or Vapor; select during implementation spike |
| OCR | Apple Vision `VNRecognizeTextRequest` |
| Concurrency | Swift Concurrency actor-based admission |
| Dynamic capacity | `ResourceGovernor` actor |
| Callback reliability | Local durable outbox + retry |

### 2.3 Storage by environment

| Environment | S3 implementation |
|---|---|
| Production | External S3 credentials/endpoint supplied by operator |
| Local development/test | MinIO S3-compatible service in Docker Compose |

Application code only depends on the S3 API. Local MinIO is not a production dependency.

---

## 3. Repository layout

```text
cmd/proxy/                     Go application entrypoint
internal/api/                  REST adapters and HATEOAS representations
internal/admin/                admin queries/actions and audit adapters
internal/mcp/                  MCP tools/tasks adapters
internal/documents/            document/batch business logic
internal/uploads/              direct and multipart upload orchestration
internal/scheduler/            fair queue and Native dispatch
internal/nativeclient/         Native HTTP client
internal/notifications/        outbox, webhook and SSE delivery
internal/auth/                 API key/session auth
internal/quota/                rate/quota reservation/refund
internal/storage/              PostgreSQL, Redis and S3 adapters
db/migrations/                 PostgreSQL schema migrations
db/queries/                    sqlc queries
native/App/                    signed SwiftUI menu bar application
native/Agent/                  per-user OCR/HTTP LaunchAgent
native/Shared/                 XPC DTOs, config schema and shared contracts
docs-site/                     Docusaurus source
docs-site/src/pages/admin/     small admin console entry and components
benchmarks/                    Apple Vision benchmark harness/raw results
SRS.md                         requirements baseline
TECH_DESIGN.md                 technical baseline
BENCHMARK.md                   measured capacity baseline
```

REST and MCP adapters must not duplicate validation, quota, job creation or result logic.

---

## 4. Public resource model

```text
Batch (optional)
└── Document (public lifecycle/resource)
    └── Page (internal work unit)
        └── Attempt (one Native execution)
```

Identifiers:

| ID | Generated by | Stability |
|---|---|---|
| `batchId` | Proxy | Batch lifetime |
| `documentId` | Proxy | Document lifetime |
| `clientDocumentId` | Client | Required for every batch item; tenant-scoped idempotency correlation |
| `pageId` | Proxy | Page lifetime |
| `attemptId` | Proxy | New on every dispatch retry |
| `eventId` | Event producer | Deduplication/replay |

Client IDs are not used as database primary keys or authorization boundaries.

---

## 5. PostgreSQL model

The schema below is conceptual; migrations remain authoritative.

```sql
CREATE TABLE batches (
    id                  TEXT PRIMARY KEY,
    user_id             TEXT NOT NULL,
    idempotency_key     TEXT NOT NULL,
    status              TEXT NOT NULL,
    total_items         INT NOT NULL,
    accepted_items      INT NOT NULL,
    rejected_items      INT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL,
    UNIQUE (user_id, idempotency_key)
);

CREATE TABLE documents (
    id                  TEXT PRIMARY KEY,
    batch_id            TEXT REFERENCES batches(id),
    user_id             TEXT NOT NULL,
    api_key_id          TEXT,
    client_document_id  TEXT,
    input_object_key    TEXT NOT NULL,
    input_sha256        TEXT NOT NULL,
    input_media_type    TEXT NOT NULL,
    status              TEXT NOT NULL,
    failure_class       TEXT,
    error_code          TEXT,
    error_detail        TEXT,
    result_object_key   TEXT,
    quota_cost          INT NOT NULL,
    result_expires_at   TIMESTAMPTZ,
    metadata_expires_at TIMESTAMPTZ,
    version             BIGINT NOT NULL DEFAULT 1,
    created_at          TIMESTAMPTZ NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL,
    UNIQUE (user_id, client_document_id)
);

CREATE TABLE pages (
    id                  TEXT PRIMARY KEY,
    document_id         TEXT NOT NULL REFERENCES documents(id),
    page_number         INT NOT NULL,
    input_object_key    TEXT NOT NULL,
    status              TEXT NOT NULL,
    result_object_key   TEXT,
    UNIQUE (document_id, page_number)
);

CREATE TABLE attempts (
    id                  TEXT PRIMARY KEY,
    page_id             TEXT NOT NULL REFERENCES pages(id),
    native_node_id      TEXT NOT NULL,
    native_boot_id      TEXT,
    status              TEXT NOT NULL,
    dispatch_count      INT NOT NULL,
    accepted_at         TIMESTAMPTZ,
    deadline_at         TIMESTAMPTZ,
    terminal_at         TIMESTAMPTZ,
    UNIQUE (page_id, dispatch_count)
);

CREATE TABLE outbox_events (
    id                  TEXT PRIMARY KEY,
    aggregate_type      TEXT NOT NULL,
    aggregate_id        TEXT NOT NULL,
    event_type          TEXT NOT NULL,
    payload             JSONB NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL,
    published_at        TIMESTAMPTZ
);
```

Additional tables cover users, API keys, sessions, usage counters, uploads, webhook endpoints, webhook deliveries, idempotency records, audit events, retention policy and Native desired runtime configuration.

---

## 6. S3 object layout

```text
inputs/{tenantId}/{documentId}/original
pages/{tenantId}/{documentId}/{pageNumber}/input
results/{tenantId}/{documentId}/manifest.json
results/{tenantId}/{documentId}/pages/{pageNumber}.json
uploads/{tenantId}/{uploadId}/object
```

Rules:

- Bucket names, endpoints and prefixes are configuration.
- Object keys are opaque and never derived directly from filenames.
- Server-side encryption is enabled in production.
- Presigned URLs are short-lived and must not appear in logs.
- Result manifest preserves page order and references page result objects.
- Proxy checks tenant ownership before issuing any presigned read URL.

---

## 7. Redis design

Redis is an optimization, not the system of record.

Suggested keys:

```text
document:{documentId}:status       JSON summary, short TTL
document:{documentId}:result       only if result <= cache limit
batch:{batchId}:status             JSON summary
native:{nodeId}:capacity           latest capacity snapshot
ratelimit:{principal}:{window}     request counters
```

Cache behavior:

```text
GET result
  ├─ Redis hit → authorize from cached ownership/version → return
  └─ Redis miss → PostgreSQL metadata → S3 result → optional cache fill
```

Redis eviction/restart must not change document truth. Cache keys are invalidated on state transition, result expiry, delete and permission changes.

---

## 8. Submission pipeline

### 8.1 Option resolution and validation

REST and MCP decode into one strict Go type; JSON schemas use `additionalProperties: false`.

```go
type OCROptionsPatch struct {
    RecognitionLevel            *RecognitionLevel `json:"recognitionLevel,omitempty"`
    RecognitionLanguages        *[]string         `json:"recognitionLanguages,omitempty"`
    AutomaticallyDetectsLanguage *bool             `json:"automaticallyDetectsLanguage,omitempty"`
    UsesLanguageCorrection       *bool             `json:"usesLanguageCorrection,omitempty"`
    CustomWords                  *[]string         `json:"customWords,omitempty"`
    MinimumTextHeight            *float32          `json:"minimumTextHeight,omitempty"`
    RegionOfInterest             *NormalizedRect   `json:"regionOfInterest,omitempty"`
    Revision                     *RevisionChoice   `json:"revision,omitempty"`
}

type EffectiveOCROptions struct {
    RecognitionLevel             RecognitionLevel
    RecognitionLanguages         []string
    AutomaticallyDetectsLanguage bool
    UsesLanguageCorrection       bool
    CustomWords                  []string
    MinimumTextHeight            float32
    RegionOfInterest             NormalizedRect
    Revision                     int
    CapabilityVersion            string
}
```

Resolution:

```text
server profile
  → merge batch patch (batch only)
  → merge item/document patch
  → validate schema/ranges/custom-word budgets
  → validate languages/revision against Native capability snapshot
  → persist EffectiveOCROptions in document request manifest
```

Native maps each effective field 1:1 to `VNRecognizeTextRequest`. It performs final validation before `tryAcquire`; validation failure does not consume a slot. Resource-related Vision settings (`preferBackgroundProcessing`, compute devices and concurrency) remain operator-controlled.

Native capability snapshot:

```json
{
  "capabilityVersion": "mac-main:boot_01K:vision-3",
  "defaultRevision": 3,
  "supportedRevisions": [1, 2, 3],
  "languages": {
    "accurate:3": ["en-US", "vi-VN"],
    "fast:3": ["en-US", "vi-VN"]
  },
  "constraints": {
    "customWordsMaxItems": 500,
    "customWordMaxUtf8Bytes": 128,
    "customWordsMaxTotalUtf8Bytes": 32768,
    "maximumCandidatesPerObservation": 3
  }
}
```

Numbers in the example are configurable platform bounds, not Apple constants. Proxy exposes a HATEOAS capabilities resource and records `capabilityVersion` with each accepted document for reproducibility/audit.

### 8.2 Single document

```text
1. Authenticate and authorize `ocr:execute`.
2. Enforce request rate limit.
3. Parse one source: multipart, URL, base64 or uploadId.
4. Stream/fetch/decode with hard limits and SSRF controls.
5. Validate magic bytes, dimensions/pages and content constraints.
6. Persist immutable input to S3.
7. Transaction:
   - reserve quota
   - create document/pages
   - enqueue page work
   - insert outbox event
8. Commit.
9. Return `202` with absolute HATEOAS links.
```

If the client disconnects after step 8, the job remains accepted. A retry with the same `Idempotency-Key` returns the existing representation.

### 8.3 Batch

Batch follows the same item pipeline with a manifest transaction.

Validation policy:

- Envelope/manifest errors reject the request before batch creation.
- Per-item content errors are represented as rejected items; valid items continue.
- `clientDocumentId` is required and checked before expensive fetch/decode.
- Quota is reserved only for accepted pages.
- Scheduler queues pages rather than treating a batch as one unit.

---

## 9. Upload service

### 9.1 Direct upload

- Stream multipart directly to S3 multipart upload.
- Abort S3 multipart upload on parse/validation error.
- Enforce bytes while streaming; do not rely on `Content-Length`.
- Above direct limit return `413` with absolute `createUpload` and documentation links.

### 9.2 Large/resumable upload

```text
POST /v1/uploads
  → uploadId + presigned multipart URLs + complete/abort links

POST /v1/uploads/{uploadId}/complete
  → validate S3 object and transition upload to ready
```

An error response does not implicitly create an upload session; this prevents orphan resources. SDKs can follow the advertised action automatically.

### 9.3 URL snapshot

The fetcher resolves and validates each redirect destination, streams to S3 with byte/time limits, computes SHA-256, then validates decoded content. OCR retries always use the stored snapshot.

---

## 10. Scheduler

### 10.1 Fair queue

Use a PostgreSQL-backed work queue with `FOR UPDATE SKIP LOCKED` plus per-tenant weighted round-robin selection. A single large batch must not monopolize the only Native slot.

### 10.2 Capacity snapshot

Proxy keeps the latest Native snapshot:

```go
type CapacitySnapshot struct {
    NodeID          string
    BootID          string
    Sequence        uint64
    ConfigVersion   uint64
    State           string
    EffectiveLimit  int
    Active          int
    Available       int
    Reason          string
    ObservedAt      time.Time
}
```

Scheduling behavior:

1. Fresh snapshot with `available > 0`: dispatch up to advertised availability.
2. Busy/paused/draining/unhealthy snapshot: keep work queued.
3. Missing/stale snapshot: use circuit-breaker policy; do not poll `/health` per job.
4. Native `503`: return attempt to `retry_wait`; apply backoff and refresh snapshot.
5. Completion/capacity webhook: update snapshot and wake scheduler immediately.

Snapshot is a hint, not a reservation. Atomic `POST /ocr` admission is the correctness boundary.

---

## 11. Native ResourceGovernor

### 11.1 Actor model

```swift
actor ResourceGovernor {
    private(set) var active: Int = 0
    private(set) var proxyDesiredLimit: Int = 1
    private(set) var localLimit: Int = 1
    private(set) var localPaused: Bool = false
    private let hardCeiling: Int
    private var safetyLimit: Int
    private(set) var state: WorkerState
    private(set) var configVersion: UInt64
    private(set) var sequence: UInt64

    var effectiveLimit: Int {
        localPaused
            ? 0
            : min(proxyDesiredLimit, localLimit, safetyLimit, hardCeiling)
    }

    var available: Int {
        max(0, effectiveLimit - active)
    }

    func tryAcquire() -> AdmissionResult
    func release(attemptID: String)
    func apply(remoteConfig: RuntimeConfig)
    func apply(localOverride: LocalOverride)
}
```

`tryAcquire` checks state and increments `active` in one actor operation. A separate `/health` check cannot replace it.

### 11.2 Dynamic configuration

Configuration layers, from immutable ceiling to runtime inputs:

- Bootstrap/ceiling config: endpoints, credentials and `hardCeiling`; environment/deployment managed.
- Desired remote config: serialized as `operatorLimit`, pause/drain and resource thresholds; stored durably by Proxy and applied with monotonic `configVersion`.
- Local override: menu-bar pause and local cap, stored on the Mac and allowed only to reduce remote capacity.
- Safety limit: thermal/memory-pressure policy computed by the Agent.

Authenticated control operation:

```http
PUT /runtime/config
If-Match: "config-version-6"
```

Native applies config atomically, returns effective state, increments sequence and publishes `capacity.changed`. Reapplying the same version is idempotent. On Native restart, Proxy detects new `bootId` and reapplies latest desired configuration.

Every capacity representation includes remote desired limit, local limit/pause, safety limit, hard ceiling, effective limit and limiting reason. Local pause wins; otherwise the minimum limit wins. Releasing local override returns to Proxy policy but can never exceed the hard ceiling or current safety limit.

When lowering limit below `active`:

- Existing attempts continue.
- `available` becomes `0`.
- State becomes `draining` until `active <= effectiveLimit`.
- No new OCR is admitted.

### 11.3 Resource safety policy

Inputs include:

- `ProcessInfo.thermalState`.
- macOS memory-pressure events.
- Optional host-load signal supplied by the co-resident workload manager.
- Operator pause/drain override.

Policy requires cooldown and hysteresis. For example, a critical thermal/memory signal reduces safety limit immediately, while recovery requires a stable cool period. Queue depth never raises the safety limit.

### 11.4 Measured default

The M4 Pro benchmark shows `.accurate` throughput changes only from 1.354 job/s at concurrency 1 to 1.361 at concurrency 2, while p50 latency grows from 730 ms to 1457 ms. Therefore:

- Default `operatorLimit = 1`.
- Default hard ceiling should remain conservative and be explicitly configured.
- `operatorLimit = 0` is the supported pause mechanism.
- Concurrency 2 is an opt-in profile after workload-specific validation.

### 11.5 App/Agent lifecycle and first-run connection

`MacOCR.app` owns presentation only. `MacOCR Agent` owns the HTTP listener, OCR execution, ResourceGovernor, durable callback outbox and reconnect loop. Quitting the app does not stop the Agent. An explicit **Stop OCR Service** operation drains/stops it separately.

The app registers the per-user LaunchAgent with `SMAppService.agent(plistName:)`. The menu communicates over a narrow XPC interface:

```swift
protocol NativeControlXPC {
    func snapshot() async throws -> NativeStatus
    func checkConnection(draftProxyURL: URL) async throws -> ConnectionReport
    func activateProxy(draftID: UUID) async throws
    func setLocalOverride(_ value: LocalOverride) async throws
    func setLaunchAtLogin(_ enabled: Bool) async throws
}
```

XPC accepts only the signed companion app identity. It never exposes callback secrets, raw OCR results or arbitrary file/network operations.

First-run flow:

```text
no active Proxy config
  → show Proxy address field
  → normalize/validate draft URL
  → Check connection
      1. DNS/TCP/TLS
      2. authenticated POST /internal/native/connectivity-check
      3. protocol/capability negotiation
      4. Proxy → Native signed reverse challenge
  → display Proxy identity + negotiated version + both-direction status
  → user activates successful draft
  → atomically persist URL, register/enable LaunchAgent and publish capacity
```

The connectivity check does not create a document, reserve quota or acquire capacity. A failed draft never overwrites the active Proxy config. The response uses stable failure stages: `url`, `dns`, `tcp`, `tls`, `authentication`, `protocol`, and `reverse_connectivity`.

URL rules:

- A bare domain/IP is normalized using a configured default scheme; production defaults to HTTPS.
- Reject user-info, query, fragment and unexpected path components.
- TLS validation is never bypassed. HTTPS to an IP requires a certificate valid for that IP.
- Explicit HTTP is allowed only in local-development policy.

The Proxy performs the reverse challenge against the configured address for the presented `nodeId`. It signs a short-lived nonce; Native echoes the digest; both sides discard the challenge after one use. This proves the actual dispatch direction instead of only checking a public health endpoint.

After activation, the Agent starts at user login. If Service Management reports that user approval is required, the app exposes that state and opens the Login Items settings pane; it must not falsely report auto-start as enabled. Transient Proxy failure keeps the Agent alive and triggers bounded exponential backoff with jitter. A manual **Check connection** remains available; it does not disable ongoing work.

---

## 12. Native dispatch and callback

### 12.1 Atomic accept

```text
POST /ocr
  ├─ governor.tryAcquire succeeds
  │    ├─ persist active attempt metadata
  │    ├─ return 202
  │    └─ perform OCR asynchronously
  └─ governor.tryAcquire fails
       └─ return 503 + Retry-After + capacity representation
```

### 12.2 Completion order

```text
OCR completes
  → persist callback event in Native local outbox
  → release governor slot
  → attach post-release capacity snapshot/sequence
  → deliver signed webhook
  → Proxy transactionally stores result/state/outbox
  → Proxy returns 2xx
  → Native removes local outbox record
```

Release happens before the capacity snapshot so Proxy can safely dispatch the next job on receipt.

### 12.3 Webhook authentication

Use HTTPS plus HMAC request signing:

```text
X-Native-Node-Id
X-Native-Timestamp
X-Native-Event-Id
X-Native-Signature
```

Signature covers method, path, timestamp, event ID and exact body digest. Proxy enforces freshness, deduplication and constant-time signature comparison. Secrets are node-specific and rotatable.

### 12.4 Ordering and restart

- `bootId` changes each Native process boot.
- `sequence` increases monotonically within a boot.
- Proxy ignores older capacity sequences for the same boot.
- A new `bootId` invalidates assumptions about active attempts from the previous boot.
- Attempt timeout/reconciliation creates a new `attemptId`; old callback cannot overwrite a newer terminal state.

### 12.5 Health use

`GET /health` is used at startup, periodically at low frequency, after repeated dispatch failures and for monitoring. The scheduler does not execute `GET /health → POST /ocr` for every job because the snapshot can become stale between calls.

---

## 13. Result completion transaction

Proxy callback handling:

```text
1. Authenticate and deduplicate Native event.
2. Verify attempt is current or handle late completion policy.
3. Write page result to S3.
4. Transaction:
   - mark attempt/page terminal
   - aggregate document/batch state
   - set result/expiry metadata when completed
   - write client notification outbox event
   - update latest Native capacity snapshot metadata
5. Commit.
6. Invalidate/populate Redis cache.
7. Wake scheduler if capacity is available.
8. ACK Native webhook.
```

The handler is idempotent. A retry of the same Native event returns success after confirming the prior commit.

---

## 14. Public polling and representations

Status response includes:

- Resource identity and state.
- Progress counts for multi-page/batch resources.
- `createdAt`, `updatedAt`, terminal/expiry timestamps.
- Stable error code where applicable.
- `_links` generated from configured public base URL.

Example queued links:

```json
{
  "_links": {
    "self": { "href": "https://api.example.com/v1/documents/doc_01K" },
    "result": { "href": "https://api.example.com/v1/documents/doc_01K/result" },
    "cancel": {
      "href": "https://api.example.com/v1/documents/doc_01K",
      "method": "DELETE"
    },
    "docs": { "href": "https://docs.example.com/documents/lifecycle" }
  }
}
```

Representation builders are centralized so REST errors/successes expose consistent link relations. Absolute URLs use `PUBLIC_API_BASE_URL` and `PUBLIC_DOCS_BASE_URL`, never the incoming `Host` header.

---

## 15. Notification delivery

### 15.1 Client webhook

- Endpoint registration and verification are control-plane operations.
- Submission references an endpoint ID.
- Delivery worker reads transactional outbox.
- HMAC signed, at-least-once, exponential backoff with jitter.
- Persistent delivery history and manual replay.
- Payload contains IDs/status/result link, not a large result.

### 15.2 SSE

- One authenticated account/principal stream.
- Heartbeat interval shorter than typical idle timeout.
- Durable event cursor and `Last-Event-ID` resume.
- Bounded per-connection buffer; slow consumers are disconnected and resume later.
- Poll/status APIs remain available when the event cursor expires.

---

## 16. MCP design

MCP tools call `documents.Service` directly with a `Principal`.

```go
func SubmitOCRDocument(ctx context.Context, principal auth.Principal, input SubmitInput) (DocumentRepresentation, error)
```

Submission tools immediately return IDs and resource links. Large results return an MCP resource handle or cursor rather than inline content. MCP Tasks extension is an adapter over the same document state where supported; fallback get tools are always exposed.

---

## 17. Retention and cleanup

Separate configurable TTLs:

```text
UPLOAD_INCOMPLETE_TTL
INPUT_AFTER_COMPLETION_TTL
RESULT_TTL
EVENT_TTL
IDEMPOTENCY_TTL
TOMBSTONE_TTL
REDIS_STATUS_TTL
REDIS_RESULT_TTL
```

Cleanup worker uses claim/check/delete/finalize phases:

1. Claim expired records with database locking.
2. Recheck lifecycle/retention eligibility.
3. Delete S3 objects idempotently.
4. Invalidate Redis keys.
5. Convert document to expired tombstone or delete after tombstone TTL.
6. Record metrics/audit outcome.

Until tombstone expiry, result access returns `410 RESULT_EXPIRED` with documentation links.

---

## 18. Configuration

### 18.1 Proxy bootstrap

```text
DATABASE_URL
REDIS_URL
S3_ENDPOINT
S3_REGION
S3_BUCKET
S3_ACCESS_KEY_ID
S3_SECRET_ACCESS_KEY
S3_FORCE_PATH_STYLE
PUBLIC_API_BASE_URL
PUBLIC_DOCS_BASE_URL
NATIVE_BASE_URL
NATIVE_AUTH_SECRET
```

Secrets are supplied by the deployment secret manager, not committed `.env` files.

### 18.2 Native bootstrap

```text
OCR_PORT
OCR_HARD_CONCURRENCY_CEILING
PROXY_BASE_URL                 # optional first-run seed
NATIVE_ADVERTISED_BASE_URL
NATIVE_NODE_ID
NATIVE_CREDENTIAL_REF          # Keychain reference, not plaintext
ALLOW_INSECURE_LOCAL_HTTP      # false outside local development
```

The activated non-secret Proxy URL and local override are stored atomically in Application Support. Node credentials and callback-signing material live in Keychain. The app bundle never contains one shared production secret.

Dynamic runtime values are not environment-only because environment changes require restart. Proxy stores desired remote config and reapplies it after Native restart; the persisted local pause/cap is then composed with that remote config and safety policy.

### 18.3 Local development

Docker Compose provides PostgreSQL, Redis and MinIO. The Native Swift process runs on macOS outside Linux containers so it can access Apple Vision. Proxy may run locally or in Docker depending on networking setup.

---

## 19. Docusaurus documentation

```text
docs-site/
├── docs/
│   ├── getting-started/
│   ├── documents/
│   ├── batches/
│   ├── uploads/
│   ├── notifications/
│   ├── mcp/
│   ├── errors/
│   └── operations/
├── static/
├── src/
│   ├── components/
│   └── pages/admin/
├── sidebars.ts
└── docusaurus.config.ts
```

- Docs-only mode with documentation mounted at route base `/`.
- No versioned docs directories.
- Markdown/MDX content with hand-authored flows and examples.
- OpenAPI published separately for machine use.
- CI: build, broken links, Markdown lint, example checks, OpenAPI/docs link validation and secret scan.

### 19.1 Route ownership

The Go Proxy serves one public origin with explicit route precedence:

```text
/v1/*, /mcp, /events, /openapi.json, /healthz, /readyz  → machine handlers
/admin, /admin/*                                         → Docusaurus admin page/assets
all other GET/HEAD routes                                → Docusaurus documentation
```

Reserved machine/admin prefixes are registered before the static docs fallback. CI fails on a documentation slug that collides with a reserved prefix. Unknown asset-like paths return `404` and are not rewritten to the admin shell.

---

## 20. Admin console

The console is a small browser client built inside `docs-site`, so `/` stays the docs root and no second frontend toolchain is needed. Static assets contain no deployment secrets. The browser calls same-origin `/v1/admin/*` endpoints.

Admin backend rules:

- Handlers authenticate an `admin` session, enforce CSRF for mutations and call the existing document, auth, quota, notification and Native-config services.
- Admin endpoints do not write tables directly except through those services/repositories and their state-machine checks.
- Collection endpoints use cursor pagination, bounded filters and stable sort order.
- Mutation responses include updated resource version and HATEOAS actions; stale versions return `409`/`412` rather than last-write-wins.
- Every mutation writes an audit event in the same database transaction as the state change where feasible.
- Retention updates validate deployment safety bounds and affect later cleanup claims; shortening a TTL does not synchronously delete stored objects.
- Dashboard reads bounded aggregates/metrics snapshots; it does not execute unbounded counts on every refresh.

Browser security:

```text
session cookie: HttpOnly; Secure; SameSite=Strict; Path=/
CSRF: server-issued token bound to session, required on unsafe methods
CSP: self by default; explicit connect-src for configured same-origin endpoints
cache: no-store for authenticated admin JSON and HTML shell
```

The UI never persists credentials in `localStorage`. Plaintext API keys are rendered once after issuance, held only in component memory, and removed on navigation/refresh. OCR content and signed object URLs are excluded from list responses and UI telemetry.

---

## 21. Observability

Structured log fields:

```text
request_id, user_id, key_id, batch_id, document_id, client_document_id,
page_id, attempt_id, native_node_id, native_boot_id, native_sequence,
event_id, status, latency_ms, quota_cost
```

Metrics:

- Submission/validation/error totals by code.
- Queue depth/age by tenant priority.
- Document/page/attempt latency histograms.
- Native effective limit, active, available, state and reason.
- Dispatch 202/503/error counts.
- Native webhook delivery/retry age.
- Client webhook/SSE delivery health.
- Redis hit/miss/eviction.
- S3 operation latency/error.
- Cleanup objects/bytes/errors.

Alerts prioritize oldest queue age, Native stale capacity, callback outbox age, database/S3 failure and result cleanup lag.

---

## 22. Failure handling

| Failure | Behavior |
|---|---|
| Native busy | Keep document queued; retry using `Retry-After`/backoff |
| Native offline | Circuit open; queue remains durable |
| Completion webhook lost | Native local outbox retries until Proxy 2xx |
| Duplicate/late callback | Deduplicate; do not overwrite newer terminal attempt |
| Proxy restart | PostgreSQL queue/outbox resumes |
| Redis loss | Rehydrate from PostgreSQL/S3 |
| S3 temporary failure | Retry; do not claim result completed until durable write succeeds |
| Client disconnect after submit | Idempotency key returns same IDs |
| SSE disconnect | Resume by event cursor or reconcile through status API |
| Client webhook unavailable | Retry; polling remains available |
| Runtime limit reduced | Drain safely, no new admission |
| Thermal/memory pressure | Safety limit drops, capacity event wakes/stops scheduler appropriately |

---

## 23. Verification plan

Before production:

1. Corpus benchmark with real receipts, screenshots, scans and PDFs.
2. Soak test while co-resident workloads run.
3. Dynamic config tests: `1→0`, `0→1`, `2→1` with active work.
4. Capacity webhook loss/reorder/duplicate/restart tests.
5. Atomic admission race test under concurrent dispatch.
6. Redis flush during result reads.
7. S3 failure and cleanup idempotency tests.
8. URL SSRF/DNS rebinding/redirect tests.
9. Batch fairness and partial failure tests.
10. HATEOAS link validation for every state/error representation.
11. MCP large-input/result context-limit tests.
12. Docusaurus build/link/example validation.
13. Admin authorization, CSRF, audit, route-collision, accessibility and secret-persistence tests.

---

## 24. Primary references

- [Apple Vision — RecognizeTextRequest](https://developer.apple.com/documentation/vision/recognizetextrequest)
- [Apple Vision — recognition level](https://developer.apple.com/documentation/vision/vnrecognizetextrequest/recognitionlevel?language=objc)
- [Apple Vision — supported recognition languages](https://developer.apple.com/documentation/vision/recognizetextrequest/supportedrecognitionlanguages?changes=_1%2C_1)
- [Apple Vision — region of interest](https://developer.apple.com/documentation/vision/vnimagebasedrequest/regionofinterest?changes=_8&language=objc)
- [Apple Vision — background processing preference](https://developer.apple.com/documentation/vision/vnrequest/preferbackgroundprocessing)
- [Apple SwiftUI — MenuBarExtra](https://developer.apple.com/documentation/swiftui/menubarextra?changes=_8)
- [Apple Service Management — SMAppService](https://developer.apple.com/documentation/servicemanagement/smappservice)
- [Apple XPC](https://developer.apple.com/documentation/xpc?changes=la)
- [Docusaurus — docs-only mode](https://docusaurus.io/docs/docs-introduction)
