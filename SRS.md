# SRS — Mac OCR Platform

**Status:** Design Baseline
**Date:** 2026-08-12
**Audience:** API consumers, MCP integrators, platform operators, implementation team

---

## 1. Purpose

Mac OCR Platform cung cấp OCR dựa trên Apple Vision qua hai interface dùng chung một business layer:

- REST API cho backend services và SDK.
- MCP server cho AI agents.

Proxy là public control plane: authentication, validation, uploads, quota, durable queue, job state, result retention và notifications. Native Mac OCR Service là thin executor chạy trên máy macOS dùng chung tài nguyên với workload khác.

Mọi submission là asynchronous. Proxy luôn trả durable identifiers; không giữ request để chờ OCR hoàn thành.

---

## 2. Topology assumptions

- Internet clients chỉ gọi public Proxy qua HTTPS.
- Proxy và Native có thể chủ động gọi trực tiếp tới địa chỉ của nhau.
- Cách expose bằng LAN, VPN, tunnel, DNS hoặc port-forward nằm ngoài phạm vi SRS.
- Native nhận Proxy base URL từ first-run setup hoặc deployment seed; node credentials được provision riêng và giữ trong Keychain.
- Proxy nhận Native base URL qua deployment configuration.
- Native gửi result/capacity webhook tới Proxy.
- Proxy không gọi `/health` trước từng dispatch; Native quyết định admission atomically tại `POST /ocr`.
- Native được phân phối dưới dạng macOS menu bar app nhưng OCR/HTTP runtime chạy trong LaunchAgent độc lập với vòng đời UI.

---

## 3. Core decisions

1. Một durable queue phục vụ cả REST và MCP.
2. Submission luôn trả `202 Accepted` với `documentId` hoặc `batchId`.
3. `GET` status/result luôn được hỗ trợ; polling là interoperability baseline.
4. Client webhook và SSE là notification channels tùy chọn, không phải source of truth.
5. Native capacity được điều khiển động và có thể đặt về `0` để pause/drain.
6. Native completion webhook đồng thời mang capacity snapshot để Proxy dispatch tiếp.
7. PostgreSQL giữ durable state; S3 giữ input/result blobs; Redis chỉ là cache có TTL.
8. Production dùng S3 do operator cung cấp; local development dùng S3-compatible storage trong Docker.
9. Docusaurus Markdown/MDX là website tài liệu chính; OpenAPI là machine contract.
10. Tất cả success/error representations có HATEOAS absolute links phù hợp với trạng thái hiện tại.
11. Root application (`/`) là website Docusaurus; không xây landing application riêng.
12. `/admin` là control-plane UI tối giản, chỉ dành cho role `admin` và dùng cùng business services với API/MCP.
13. Native được đóng gói thành macOS menu bar app; OCR Agent auto-start độc lập và local controls chỉ được giảm capacity.

---

## 4. Actors

| Actor | Responsibility |
|---|---|
| Admin | Quản lý users, API keys, quotas, retention và Native runtime policy |
| REST service | Submit single/batch documents, poll status/result, tùy chọn webhook hoặc SSE |
| MCP agent | Submit OCR, poll bằng tools/tasks, đọc result theo page/cursor |
| Native worker | Atomic admission, OCR từng page/image, báo result và capacity |
| Native menu app | First-run Proxy setup, connection test, local pause/cap và trạng thái vận hành |
| Cleanup worker | Xóa upload/input/result/event/idempotency records hết hạn |

---

## 5. Public input model

### 5.1 Supported source types

Mỗi document chọn đúng một source:

| Source | Use case | Requirement |
|---|---|---|
| Multipart file | File nhỏ upload trực tiếp | Recommended for direct upload |
| HTTPS URL | File đã được host | Proxy fetch, validate và snapshot một lần |
| Base64 JSON | Payload rất nhỏ/legacy clients | Supported nhưng không khuyến nghị |
| `uploadId` | File lớn hoặc resumable upload | Recommended above direct-upload limit |

Proxy normalize mọi source thành immutable S3 object trước khi queue OCR. Retry không fetch lại URL và không phụ thuộc file local của client.

### 5.2 Direct file upload

Direct upload dùng `multipart/form-data` và được stream vào storage; Proxy không load toàn bộ file vào RAM.

Nếu request vượt `maxDirectUploadBytes`, Proxy trả `413 Payload Too Large` cùng absolute URL để tạo upload session:

```json
{
  "type": "https://docs.example.com/problems/direct-upload-too-large",
  "code": "DIRECT_UPLOAD_TOO_LARGE",
  "status": 413,
  "title": "File is too large for direct upload",
  "detail": "Use a resumable upload session for this file.",
  "limits": {
    "maxDirectUploadBytes": 10485760
  },
  "_links": {
    "createUpload": {
      "href": "https://api.example.com/v1/uploads",
      "method": "POST"
    },
    "documentation": {
      "href": "https://docs.example.com/uploads/large-files"
    }
  }
}
```

Limit trong ví dụ chỉ minh họa; giá trị thật đến từ config/capabilities.

### 5.3 Base64

- Strict decode; reject alphabet, padding hoặc data URI sai.
- Limit theo decoded bytes, không theo độ dài JSON string.
- MIME phải được xác nhận bằng magic bytes.
- Base64 có limit thấp hơn multipart vì tăng payload và memory pressure.
- SDK/docs phải ưu tiên multipart hoặc `uploadId`.

### 5.4 URL

- Chỉ cho phép HTTPS theo mặc định.
- Connect/read timeout, redirect limit và streaming byte limit.
- SSRF validation áp dụng cho mỗi DNS resolution và redirect.
- Block loopback, private, link-local, multicast và cloud metadata addresses.
- Không tin `Content-Length`, filename hoặc remote `Content-Type`.
- Signed URL/query string không được ghi log.

### 5.5 File validation

Validation hoàn thành trước khi reserve quota và tạo OCR work items:

- Allowlisted MIME/magic bytes.
- Per-file decoded byte limit.
- Pixel dimension và decompression-bomb limit.
- PDF page-count limit; reject encrypted/password-protected PDF nếu không có contract giải mã.
- Empty, truncated, corrupted hoặc mismatched file.
- Batch item count, total bytes và total pages.
- Filename được coi là metadata không tin cậy.

### 5.6 OCR option schema

API và MCP dùng cùng một typed schema. Unknown properties bị reject; không forward raw JSON keys vào Vision.

Client-controllable OCR semantics:

| API field | Apple Vision mapping | Validation |
|---|---|---|
| `recognitionLevel` | `recognitionLevel` | `fast` hoặc `accurate` |
| `recognitionLanguages` | `recognitionLanguages` | BCP-47 identifiers; supported for selected level/revision; ordered by priority |
| `automaticallyDetectsLanguage` | `automaticallyDetectsLanguage` | Boolean |
| `usesLanguageCorrection` | `usesLanguageCorrection` | Boolean |
| `customWords` | `customWords` | String array; bounded item count, item length and total UTF-8 bytes |
| `minimumTextHeight` | `minimumTextHeight` | Normalized fraction in `[0,1]` |
| `regionOfInterest` | `regionOfInterest` | Normalized `{x,y,width,height}` fully inside `[0,1]`; Vision origin is bottom-left |
| `revision` | `revision` | Omitted/`default` or a revision advertised by Native capabilities |

Platform-controlled properties are not client options: `preferBackgroundProcessing`, compute-device selection, deprecated `usesCPUOnly`, concurrency and resource limits. Trên shared host, operator policy luôn có quyền ưu tiên hơn client workload.

Separate output options may control serialization without changing OCR execution:

- `includeText`.
- `includeBlocks`.
- `maximumCandidatesPerObservation`, bounded by platform limit.
- Page/cursor sizing for result retrieval.

Default behavior is a named server profile, returned by capabilities and recorded as resolved effective options on every document. Recommended initial profile:

```json
{
  "recognitionLevel": "accurate",
  "recognitionLanguages": ["vi-VN", "en-US"],
  "automaticallyDetectsLanguage": false,
  "usesLanguageCorrection": true,
  "customWords": [],
  "minimumTextHeight": 0,
  "regionOfInterest": { "x": 0, "y": 0, "width": 1, "height": 1 },
  "revision": "default"
}
```

Defaults are deployment configuration, not SDK constants. Omitted fields inherit the current default profile; explicit fields override only those values.

Validation order:

1. JSON Schema/type/unknown-field validation at Proxy.
2. Range, size and cross-field validation at Proxy.
3. Capability validation against selected recognition level/revision/languages.
4. Resolve and persist immutable effective options with the document.
5. Native validates the resolved options again before atomic admission.

Batch merge order is `server defaults → batch options → item options`. Each resulting item is validated independently and stores its own effective options.

### 5.7 Capabilities discovery

```http
GET /v1/ocr/capabilities
```

Response includes:

- Engine/OS/build and capability version.
- Default profile and all field constraints.
- Supported revisions.
- Supported languages for each recognition level/revision combination.
- Supported input MIME types and public upload/batch/result limits.
- Whether the snapshot is live or last-known/stale.
- HATEOAS links to submit, upload and option documentation.

Native computes language/revision capabilities from the installed Vision runtime and publishes them at startup/config change. Proxy does not maintain a hardcoded language list.

---

## 6. Large upload flow

1. Client gọi `POST /v1/uploads` với filename, media type, size và optional checksum.
2. Proxy tạo upload session và trả S3 presigned/resumable links.
3. Client upload trực tiếp tới S3-compatible storage.
4. Client gọi upload completion link.
5. Proxy xác nhận size/checksum/magic bytes và chuyển upload sang `ready`.
6. Client submit document bằng `uploadId`.

Upload chưa hoàn thành không được submit OCR. Abandoned uploads được cleanup sau TTL.

Upload-session response cũng là một resource representation:

```json
{
  "uploadId": "upl_01K...",
  "status": "created",
  "expiresAt": "2026-08-12T17:00:00Z",
  "parts": {
    "partSizeBytes": 8388608,
    "maxParts": 10000
  },
  "_links": {
    "self": { "href": "https://api.example.com/v1/uploads/upl_01K..." },
    "uploadParts": {
      "href": "https://api.example.com/v1/uploads/upl_01K.../parts",
      "method": "POST"
    },
    "complete": {
      "href": "https://api.example.com/v1/uploads/upl_01K.../complete",
      "method": "POST"
    },
    "abort": {
      "href": "https://api.example.com/v1/uploads/upl_01K...",
      "method": "DELETE"
    },
    "documentation": { "href": "https://docs.example.com/uploads/large-files" }
  }
}
```

---

## 7. Single document API

### 7.1 Submit

```http
POST /v1/documents
Authorization: Bearer ocr_live_xxx
Idempotency-Key: invoice-import-2026-001
```

JSON source example:

```json
{
  "clientDocumentId": "erp-invoice-2026-001",
  "input": {
    "type": "url",
    "url": "https://storage.example.com/invoice.jpg"
  },
  "options": {
    "recognitionLevel": "accurate",
    "languages": ["vi-VN", "en-US"]
  },
  "notifications": {
    "webhookEndpointId": "whe_01K...",
    "publishToEventStream": true
  }
}
```

Proxy luôn trả:

```http
202 Accepted
Location: https://api.example.com/v1/documents/doc_01K...
Retry-After: 3
```

```json
{
  "documentId": "doc_01K...",
  "clientDocumentId": "erp-invoice-2026-001",
  "status": "queued",
  "createdAt": "2026-08-12T16:00:00Z",
  "resultExpiresAt": null,
  "_links": {
    "self": {
      "href": "https://api.example.com/v1/documents/doc_01K..."
    },
    "result": {
      "href": "https://api.example.com/v1/documents/doc_01K.../result"
    },
    "cancel": {
      "href": "https://api.example.com/v1/documents/doc_01K...",
      "method": "DELETE"
    },
    "documentation": {
      "href": "https://docs.example.com/documents/lifecycle"
    }
  }
}
```

### 7.2 Status

```http
GET /v1/documents/{documentId}
If-None-Match: "document-version-4"
```

Proxy trả `ETag` và `Retry-After` khi document chưa terminal. Không đổi thì có thể trả `304 Not Modified`.

Processing representation:

```http
200 OK
ETag: "document-version-4"
Retry-After: 3
```

```json
{
  "documentId": "doc_01K...",
  "clientDocumentId": "erp-invoice-2026-001",
  "status": "processing",
  "progress": { "totalPages": 12, "completedPages": 4, "failedPages": 0 },
  "updatedAt": "2026-08-12T16:00:04Z",
  "_links": {
    "self": { "href": "https://api.example.com/v1/documents/doc_01K..." },
    "result": { "href": "https://api.example.com/v1/documents/doc_01K.../result" },
    "cancel": {
      "href": "https://api.example.com/v1/documents/doc_01K...",
      "method": "DELETE"
    }
  }
}
```

### 7.3 Result

```http
GET /v1/documents/{documentId}/result
```

| Document state | Result endpoint response |
|---|---|
| `queued`, `processing`, `retry_wait` | `202` + `Retry-After` + status link |
| `completed` | `200` result hoặc result page/cursor links |
| `failed`, `cancelled` | Problem response + actionable links |
| Result expired | `410 Gone` + retention documentation link |
| Missing or unauthorized | `404 Not Found` |

Result lớn được đọc theo page hoặc cursor; không trả một JSON không giới hạn.

Completed result representation:

```json
{
  "documentId": "doc_01K...",
  "status": "completed",
  "resultExpiresAt": "2026-08-19T16:00:00Z",
  "result": {
    "text": "Hóa đơn số 123...",
    "pageCount": 1,
    "pages": [
      {
        "pageNumber": 1,
        "text": "Hóa đơn số 123...",
        "blocks": []
      }
    ]
  },
  "_links": {
    "self": { "href": "https://api.example.com/v1/documents/doc_01K.../result" },
    "document": { "href": "https://api.example.com/v1/documents/doc_01K..." },
    "pages": { "href": "https://api.example.com/v1/documents/doc_01K.../result/pages{?cursor,limit}", "templated": true },
    "documentation": { "href": "https://docs.example.com/results/schema" }
  }
}
```

---

## 8. Batch API

### 8.1 Model

```text
Batch
├── Document A
│   └── Page jobs
├── Document B
│   └── Page jobs
└── Document C
    └── Page jobs
```

Batch là tập hợp documents độc lập. Proxy scheduler interleave page jobs giữa tenants; batch lớn không được chiếm toàn bộ Native.

### 8.2 Client identifiers

- Mỗi batch item bắt buộc có `clientDocumentId` do client cung cấp.
- `clientDocumentId` unique trong tenant idempotency namespace.
- Proxy vẫn sinh opaque `documentId` cho authorization, lifecycle và URLs.
- Cùng `clientDocumentId` + cùng immutable input digest trả mapping hiện có.
- Cùng `clientDocumentId` + input khác trả `409 Conflict`.
- Batch request bắt buộc có `Idempotency-Key`.

### 8.3 Submit

```http
POST /v1/batches
Idempotency-Key: daily-invoices-2026-08-12
```

```json
{
  "items": [
    {
      "clientDocumentId": "invoice-001",
      "input": { "type": "upload", "uploadId": "upl_001" }
    },
    {
      "clientDocumentId": "invoice-002",
      "input": { "type": "url", "url": "https://example.com/002.png" }
    }
  ],
  "options": {
    "recognitionLevel": "accurate",
    "languages": ["vi-VN", "en-US"]
  }
}
```

Proxy trả `202` với `batchId`, mapping từng `clientDocumentId` sang `documentId`, status/result links và batch polling link:

```json
{
  "batchId": "bat_01K...",
  "status": "queued",
  "summary": { "total": 2, "accepted": 2, "rejected": 0 },
  "items": [
    {
      "clientDocumentId": "invoice-001",
      "documentId": "doc_001",
      "status": "queued",
      "_links": {
        "document": { "href": "https://api.example.com/v1/documents/doc_001" },
        "result": { "href": "https://api.example.com/v1/documents/doc_001/result" }
      }
    },
    {
      "clientDocumentId": "invoice-002",
      "documentId": "doc_002",
      "status": "queued",
      "_links": {
        "document": { "href": "https://api.example.com/v1/documents/doc_002" },
        "result": { "href": "https://api.example.com/v1/documents/doc_002/result" }
      }
    }
  ],
  "_links": {
    "self": { "href": "https://api.example.com/v1/batches/bat_01K..." },
    "results": { "href": "https://api.example.com/v1/batches/bat_01K.../results{?cursor,limit}", "templated": true },
    "cancel": {
      "href": "https://api.example.com/v1/batches/bat_01K...",
      "method": "DELETE"
    }
  }
}
```

### 8.4 Partial validation/failure

- Invalid batch envelope, duplicate IDs hoặc missing multipart parts: reject toàn request bằng `422`.
- Content validation failure của một item: item đó `rejected`; item hợp lệ vẫn được accept nếu atomic-batch mode không được yêu cầu.
- Hạ tầng fail chỉ retry affected page/item, không retry toàn batch.
- Batch terminal state có thể là `completed`, `completed_with_errors`, `failed` hoặc `cancelled`.
- Results giữ nguyên item order từ manifest.

---

## 9. Notifications

### 9.1 Polling

Polling luôn hỗ trợ cho single và batch. Client phải tôn trọng `Retry-After`, dùng backoff/jitter và ETag.

### 9.2 Client webhook

- Client submit `webhookEndpointId`, không gửi raw callback URL tùy ý theo request.
- Endpoint được đăng ký, verified và có signing secret trước khi sử dụng.
- Delivery at-least-once, signed HMAC, có timestamp và `eventId`.
- Client deduplicate theo `eventId`.
- Notification chỉ chứa state và result link, không nhét result lớn.
- Polling vẫn hoạt động nếu webhook delivery fail.

### 9.3 SSE

Private services sau NAT/firewall có thể mở outbound stream:

```http
GET /v1/events
Accept: text/event-stream
Last-Event-ID: evt_01K...
```

- Một stream có thể nhận events cho toàn principal/tenant.
- Proxy gửi heartbeat, event IDs và retry hint.
- Client reconnect bằng `Last-Event-ID`.
- Event retention hết hạn thì client reconcile qua document/batch list APIs.
- SSE là notification; status/result APIs vẫn là authority.

### 9.4 Transactional outbox

Document state update và notification outbox record phải commit trong cùng database transaction để không có trạng thái `completed` nhưng mất completion event.

---

## 10. MCP interface

MCP và REST gọi cùng Document Service, queue, quota và result store.

Required tools:

| Tool | Purpose |
|---|---|
| `submit_ocr_document` | Submit one input; trả `documentId` và links/resource handles |
| `submit_ocr_batch` | Submit nhiều items; mỗi item bắt buộc `clientDocumentId` |
| `get_ocr_document` | Lấy durable status |
| `get_ocr_batch` | Lấy batch status/summary |
| `get_ocr_result` | Lấy result nhỏ hoặc page/cursor/resource handle |
| `cancel_ocr_document` | Best-effort cancellation |

MCP Tasks extension có thể map task handle sang `documentId`/`batchId` nếu client hỗ trợ. Fallback tools luôn tồn tại; không bắt agent giữ tool call mở chờ OCR.

Base64 lớn không được khuyến nghị trong MCP arguments. Ưu tiên resource URI, HTTPS URL hoặc `uploadId`. Result lớn được đọc theo page/cursor để không làm tràn model context.

---

## 11. Native OCR Service

### 11.1 OCR request

Proxy dispatch một image/page đã normalize:

```json
{
  "documentId": "doc_01K...",
  "pageId": "page_01K...",
  "attemptId": "att_01K...",
  "input": {
    "url": "https://api.example.com/worker-inputs/signed-object",
    "mediaType": "image/jpeg",
    "sha256": "..."
  },
  "options": {
    "recognitionLevel": "accurate",
    "languages": ["vi-VN", "en-US"]
  },
  "callback": {
    "url": "https://api.example.com/webhooks/native/events"
  }
}
```

Native performs atomic admission:

- Accepted: `202` với `attemptId`.
- Capacity unavailable: `503 Service Unavailable` + `Retry-After`.
- Invalid dispatch: `4xx`, terminal for that attempt.

Proxy không fail document khi Native busy; job chuyển `retry_wait` rồi queue lại.

### 11.2 Dynamic resource governor

Native là authority về khả năng chạy OCR trên shared host.

```text
effectiveLimit = min(operatorLimit, resourceSafetyLimit, hardCeiling)
available      = max(0, effectiveLimit - active)
```

Config động tối thiểu:

- `operatorLimit`: số OCR tối đa operator cho phép, gồm `0` để pause.
- `hardCeiling`: upper bound từ bootstrap/deployment config.
- `drain`: ngừng accept mới, không kill jobs đang chạy.
- Thermal-state policy.
- Memory-pressure policy.
- Cooldown/hysteresis để tránh capacity flapping.
- Callback retry/backoff policy.

Giảm limit không cancel OCR đang chạy. Native chỉ accept job mới khi `active < effectiveLimit` và resource policy cho phép.

Benchmark trên Mac mini M4 Pro cho thấy `.accurate` concurrency 2/4 gần như không tăng throughput nhưng tăng latency mạnh; default chính thức là `operatorLimit=1`. Chi tiết tại [BENCHMARK.md](BENCHMARK.md).

### 11.3 Capacity/result webhook

Native gửi event khi:

- Start/restart.
- Runtime config thay đổi.
- Ready, busy, paused, draining hoặc unhealthy.
- Slot được release mà không có result event.
- Attempt completed/failed.

Result event luôn kèm capacity snapshot sau khi Native release slot:

```json
{
  "eventId": "evt_native_01K...",
  "type": "attempt.completed",
  "nodeId": "mac-main",
  "bootId": "boot_01K...",
  "sequence": 42,
  "attemptId": "att_01K...",
  "documentId": "doc_01K...",
  "result": {
    "text": "...",
    "blocks": []
  },
  "capacity": {
    "configVersion": 7,
    "state": "ready",
    "operatorLimit": 1,
    "effectiveLimit": 1,
    "active": 0,
    "available": 1,
    "reason": null
  },
  "occurredAt": "2026-08-12T16:00:03Z"
}
```

Native giữ local delivery outbox và retry cho đến khi Proxy ACK 2xx. Proxy deduplicate theo `eventId`/`attemptId`, bỏ event có sequence cũ trong cùng `bootId` và không để late attempt overwrite terminal result.

### 11.4 Health

`GET /health` dùng cho bootstrap, monitoring, circuit breaker và reconcile event bị mất. Nó không reserve slot và không được gọi làm preflight trước mỗi dispatch.

### 11.5 macOS app, first-run setup and auto-start

Native is distributed as a signed macOS application with a menu bar control process and a separate per-user LaunchAgent. Closing or quitting the menu UI must not terminate accepted OCR work or callback delivery. Stopping the OCR Agent is a separate explicit action.

On first launch, if no active Proxy configuration exists, the app presents a minimal setup screen:

1. User enters a Proxy domain, IP address or full base URL.
2. The app normalizes a bare domain/IP to the configured default scheme and rejects URL user-info, query, fragment and unexpected paths.
3. User presses **Check connection**.
4. Native validates DNS/TCP/TLS, authenticates to the Proxy connectivity-check endpoint and verifies protocol compatibility.
5. Proxy performs a reverse challenge against the Native endpoint configured for that node, proving both dispatch and callback directions without creating an OCR job.
6. Only after all checks pass does the app atomically activate the Proxy URL, register/enable auto-start and start normal capacity publication.

If macOS requires Login Items approval, the app shows the pending state and provides an action to open the correct System Settings pane. Setup is not reported as fully auto-start-enabled until Service Management reports the registered/enabled state.

Production defaults to HTTPS. An IP address used with HTTPS must pass normal certificate hostname/IP SAN validation; the UI must never offer “ignore TLS errors”. Explicit HTTP is allowed only by local-development policy.

The screen asks only for the Proxy address. Node credentials are provisioned separately and stored in Keychain; they must not be embedded as one shared application secret. If credentials are absent or invalid, connection check returns an actionable `PAIRING_REQUIRED`/`UNAUTHORIZED` state rather than silently accepting an unauthenticated health check.

Connection check is side-effect bounded:

- It does not submit OCR, reserve quota or occupy a Native capacity slot.
- A failed check does not replace a previously working active configuration.
- Results distinguish invalid URL, DNS, TCP, TLS, authentication, protocol mismatch and reverse-connectivity failure.
- A successful result displays Proxy identity, negotiated protocol version and both-direction reachability.

After successful setup, the LaunchAgent starts automatically at user login. If Proxy is temporarily unavailable, Native remains running and reconnects with exponential backoff/jitter; it does not reopen first-run setup or spin in a tight loop. The menu shows disconnected state and offers a manual **Check connection** action.

Menu controls include run/pause, a conservative local concurrency cap, Launch at Login, current active/available jobs, Proxy connectivity, resource-pressure reason and callback-outbox count. Local controls can only reduce remote capacity:

```text
effectiveLimit = localPaused
  ? 0
  : min(hardCeiling, proxyDesiredLimit, localLimit, safetyLimit)
```

Local pause persists across Agent restart and has highest precedence. Every local state/cap change emits a capacity event so the Proxy immediately stops or resumes dispatch consistently.

---

## 12. State models

Document:

```text
queued → processing → completed
   └──→ retry_wait → queued
   └──→ failed
   └──→ cancelled
```

Native capacity:

```text
starting → ready ↔ busy
              ├─→ draining → paused
              └─→ unhealthy
```

Retry giữ nguyên `documentId`/`pageId` nhưng tạo `attemptId` mới.

---

## 13. Storage, cache and retention

| Store | Responsibility |
|---|---|
| PostgreSQL | Users, keys, documents, batches, attempts, state, quotas, outbox, retention timestamps, S3 pointers |
| S3 | Uploaded inputs, normalized page inputs, full OCR results |
| Redis | Hot status/result fragments, rate limits, optional dispatch hints; never sole source of truth |

Result lifecycle:

1. Completion persists result to S3 and metadata/state to PostgreSQL.
2. `GET result` may cache small result/metadata in Redis with bounded TTL.
3. Redis miss loads from PostgreSQL/S3 and repopulates cache.
4. At `resultExpiresAt`, cleanup deletes S3 result and invalidates Redis.
5. Document tombstone/metadata remains longer so client receives `410 RESULT_EXPIRED` instead of ambiguous `404`.

Input, result, event, upload, idempotency and tombstone retention are separate configurable periods. Cleanup operations are idempotent.

---

## 14. HATEOAS and errors

- `_links` uses absolute URLs built from configured public base URLs, never untrusted `Host` headers.
- Links are state-dependent; completed documents do not advertise cancel if cancellation is invalid.
- Problem responses follow RFC 9457-style fields plus stable `code`, limits and `_links`.
- Every actionable error includes a relevant docs link and, where applicable, the next API action.
- Error response must not create costly side effects by default; `413` links to upload-session creation rather than silently creating an orphan upload.

Common codes:

| HTTP | Code | Meaning |
|---:|---|---|
| 400 | `INVALID_INPUT` | Malformed or inconsistent input |
| 401 | `UNAUTHORIZED` | Missing/invalid credential |
| 403 | `FORBIDDEN` | Scope/role denied |
| 404 | `DOCUMENT_NOT_FOUND` | Missing or deliberately hidden unauthorized resource |
| 409 | `CLIENT_DOCUMENT_ID_CONFLICT` | Same client ID with different input |
| 410 | `RESULT_EXPIRED` | Result retention elapsed |
| 413 | `DIRECT_UPLOAD_TOO_LARGE` | Must use upload session |
| 422 | `BATCH_VALIDATION_FAILED` | Invalid batch envelope/manifest |
| 429 | `RATE_LIMITED` / `QUOTA_EXCEEDED` | Client must follow `Retry-After` or quota links |
| 503 | `SERVICE_UNAVAILABLE` | Proxy cannot durably accept work |

---

## 15. Authentication and quota

- REST and MCP use the same Bearer API keys and quota pool.
- Roles: `admin`, `user`; users can login to manage their own keys.
- API key scopes: `ocr:execute`, `ocr:read`.
- Plaintext API key shown once; stored using prefix lookup + HMAC-SHA256.
- Admin/user password uses Argon2id.
- Reserve quota after validation and atomic document creation.
- One accepted image/PDF page is one OCR unit.
- Pre-OCR validation rejection is not charged.
- Terminal infrastructure failure is refunded idempotently.
- Batch cost is the sum of accepted pages, not one unit per batch.

---

## 16. Documentation

Website documentation uses Docusaurus in docs-only mode with Markdown/MDX.

The public application root `/` opens the documentation home page. Documentation pages own all non-reserved content routes; there is no separate marketing/landing application.

Required sections:

- Getting started and authentication.
- Single document submission.
- Batch submission and `clientDocumentId` semantics.
- Direct/base64/URL/large upload flows.
- Polling, webhook and SSE.
- MCP setup/tools/tasks/large result access.
- Result schema, retention and expiration.
- HATEOAS links and full error catalog.
- Native integration, dynamic capacity and operations.
- SDK examples for cURL, Go, TypeScript and Python.

No documentation versioning is enabled. `/openapi.json` remains machine contract for validation/SDK generation, not the primary human documentation.

Reserved application routes must not be used as documentation slugs:

- `/admin` and `/admin/*` for the admin UI.
- `/v1/*` for REST resources and admin APIs.
- `/mcp` for MCP transport.
- `/events` for SSE.
- `/openapi.json`, `/healthz` and `/readyz` for machine endpoints.

---

## 17. Admin console

The first release includes a small operational admin console at `/admin`; it is not a second public client application.

MVP scope:

- Dashboard: Proxy dependencies, Native state/capacity, oldest queue age, queue counts and recent failures.
- Documents/batches: paginated search by server/client ID and state, lifecycle details, attempts and state-valid cancel action.
- Users/API keys/quotas: create/disable users, issue/revoke keys, assign scopes and change quota policy. Plaintext keys are shown exactly once.
- Retention: inspect and update allowed TTL policies within deployment safety bounds; shortening retention never silently deletes data during the same request.
- Native control: inspect desired/effective config, set `operatorLimit`, pause/drain/resume and see config/capacity versions.
- Notifications: inspect webhook endpoints/deliveries and replay a failed delivery when permitted.
- Audit: paginated view of all admin mutations and actor/outcome metadata.

Security and behavior requirements:

- Only `admin` sessions may call `/v1/admin/*`; hiding UI controls is not authorization.
- Browser sessions use `HttpOnly`, `Secure`, `SameSite=Strict` cookies, short idle/absolute expiry and CSRF protection on mutations.
- All mutations require confirmation for consequential actions, optimistic concurrency where applicable and an immutable audit event.
- UI follows server-provided HATEOAS actions and uses the same application services/state machines as REST/MCP.
- OCR text, input blobs, signed URLs, password hashes and API-key secrets are not displayed by default or written to browser telemetry.
- Lists are server-paginated; UI has explicit loading, empty, stale, partial-failure and permission-denied states.
- Documentation remains public unless deployment policy explicitly protects the entire site; admin authentication does not gate docs.

---

## 18. Non-functional requirements

| ID | Requirement |
|---|---|
| NFR-01 | Accepted submission and durable IDs committed atomically |
| NFR-02 | Native admission is atomic; stale capacity cannot exceed effective limit |
| NFR-03 | Completion/result callbacks are at-least-once and idempotent |
| NFR-04 | Polling supports ETag and server-directed `Retry-After` |
| NFR-05 | URL fetch protects against SSRF across DNS and redirects |
| NFR-06 | Multipart/upload processing is streaming and bounded |
| NFR-07 | Redis eviction/restart cannot destroy the only copy of a valid result |
| NFR-08 | Cleanup is idempotent and distinguishes expired from nonexistent results |
| NFR-09 | Batch scheduler prevents a tenant/batch from monopolizing Native |
| NFR-10 | Native runtime limit can change without process restart; lowering drains safely |
| NFR-11 | Every response/error exposes valid state-appropriate absolute links |
| NFR-12 | Logs correlate requestId, documentId, pageId, attemptId, batchId and eventId |

---

## 19. Deployment-configurable limits

The following values must not be hardcoded into SDKs:

- Direct upload bytes.
- Base64 decoded bytes.
- File bytes, pixels and PDF pages.
- Batch items/bytes/pages.
- Poll interval bounds.
- Result inline/page size.
- Upload/input/result/event/idempotency/tombstone TTLs.
- Native hard ceiling, operator limit and resource policies.
- Callback timeouts/retries.

Clients discover public limits and supported formats through a capabilities endpoint and HATEOAS links.
