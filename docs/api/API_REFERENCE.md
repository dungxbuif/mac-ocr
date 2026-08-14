# OCR API Reference

The OCR API is asynchronous. Submission endpoints return `202 Accepted` and durable server-generated document identifiers. Clients poll documents, select a notification channel, or use MCP task events until processing reaches a terminal state.

Interactive Swagger UI is served at `/api/v1/docs`. The OpenAPI 3.1 contract is generated from runtime Go schemas and served at `/api/v1/openapi.json`; there is no separately maintained YAML contract.

Only `application/json` submissions are supported. Single-document envelopes are capped at 36 MiB; batch and MCP envelopes are capped at 128 MiB. Exceeding an envelope cap returns `413 PAYLOAD_TOO_LARGE`. Base64 is limited to 25 MiB decoded per item and returns `400 BASE64_TOO_LARGE`; URL downloads have a 100 MiB resource-safety ceiling and return `413 URL_CONTENT_TOO_LARGE`.

## Authentication

Protected endpoints require an API key:

```http
Authorization: Bearer sk_ocr_<secret>
```

New keys use the `sk_ocr_` prefix. The plaintext key is returned only when it is created.

The complete format is `sk_ocr_` followed by 48 hexadecimal characters. Authentication hashes the key and may validate active-key metadata from Redis; plaintext credentials are never cached. Revocation persists `revoked_at` and invalidates cached entries for the account. Disabled-account state is verified from PostgreSQL on every request.

## Administrative account deactivation

`POST /v1/users/{userId}/deactivate` requires an authenticated administrator session and its CSRF token. It marks the account disabled, invalidates the account's cached API-key metadata, and immediately rejects both API-key authentication and existing administrator sessions belonging to that account. API-key rows, document history, audit metadata, and already queued work are retained; queued work continues processing. The existing `PATCH /v1/users/{userId}` control can set `disabled` back to `false` when an administrator intentionally reactivates an account.

The Swagger contract describes the public OCR and MCP data plane. Administrator session and account-management controls are documented here as the private control plane.

## Input contract

The client does not send a file type. The service determines content type from magic bytes and accepts JPEG, PNG, TIFF, WebP, and PDF.

JSON submissions contain exactly one source:

```json
{"input":{"url":"https://files.example.com/invoice.png"}}
```

```json
{"input":{"base64":"iVBORw0KGgo..."}}
```

Unknown JSON fields are rejected. A JSON input containing both `url` and `base64`, or neither, returns `400 INVALID_SOURCE`.

## Single document

### `POST /v1/documents`

URL or Base64 input:

```json
{
  "input": {"url": "https://files.example.com/invoice.png"},
  "options": {
    "recognitionLevel": "accurate",
    "languages": ["en-US"],
    "automaticallyDetectsLanguage": false,
    "usesLanguageCorrection": true
  }
}
```

`clientDocumentId` and `Idempotency-Key` are not part of the contract. The server generates the document ID.

Response:

```json
{
  "documentId": "doc_18f673199c0",
  "status": "queued",
  "createdAt": "2026-08-14T13:40:00Z",
  "_links": {
    "self": {"href": "https://ocr.example.com/v1/documents/doc_18f673199c0"}
  }
}
```

### `GET /v1/documents/{documentId}`

Returns `queued`, `processing`, `completed`, `failed`, or `cancelled`. Pending resources include `Retry-After`. A completed document includes the full cached `result` in this same response; no `/result` endpoint exists. Conditional reads support `If-None-Match`.

### `DELETE /v1/documents/{documentId}`

Cancels a queued document. Processing or terminal documents return `409 STATE_CONFLICT`.

### `GET /v1/documents`

Lists documents owned by the authenticated account. Supported query parameters are `status`, `limit`, and `offset`.

## Batch documents

### `POST /v1/batches`

The request body is a JSON array containing 1–100 items. It is not wrapped in an `items` object. Each item uses the same `input` and `options` contract as a single JSON submission.

```json
[
  {
    "input": {"url": "https://files.example.com/invoice-1.png"},
    "options": {"languages": ["en-US"]}
  },
  {
    "input": {"base64": "iVBORw0KGgo..."}
  }
]
```

All items are validated before quota reservation and queue creation. If one item fails, the request returns an error containing its zero-based `batch item <index>` and no document is queued.

Response items use their array index for correlation:

```json
{
  "status": "accepted",
  "summary": {"total": 2, "accepted": 2, "rejected": 0},
  "items": [
    {"index": 0, "documentId": "doc_01", "status": "queued"},
    {"index": 1, "documentId": "doc_02", "status": "queued"}
  ]
}
```

Every accepted item becomes a normal queued document. All document rows are inserted in one transaction, but no batch row or `batch_id` is stored. The scheduler processes batch-submitted and individually submitted documents through the same dispatch and callback path.

Batch is submission convenience only. There is no public batch resource. Poll each returned `documentId` through `GET /v1/documents/{documentId}`.

## Notifications

Single JSON submissions and every batch item may include one optional notification channel:

```json
{
  "input": {"url": "https://files.example.com/invoice.png"},
  "notification": {
    "type": "webhook",
    "url": "https://api.yourcompany.com/ocr-webhook",
    "secret": "my-hmac-secret-at-least-16-characters"
  }
}
```

Webhook URLs must use HTTPS, must resolve only to public addresses, and are checked again at delivery. Secrets contain 16–256 characters, are encrypted at rest, and are never returned. Delivery is at least once with retry. Verify the hexadecimal HMAC-SHA256 in `X-OCR-Signature` over `<X-OCR-Timestamp>.<X-OCR-Event-Id>.<raw-body>`.

For SSE use `"notification":{"type":"sse"}` and connect to `GET /v1/events` with the same API key. Events contain IDs, a three-second retry hint, and heartbeats. Reconnect with `Last-Event-ID`.

## Result retention

Completed document responses include `resultExpiresAt`. The default `RESULT_TTL` is 168 hours. The callback writes the full result to Redis with this TTL, and document reads obtain the payload directly from Redis. A cache miss or elapsed TTL returns `410 RESULT_EXPIRED`. Every minute, retention clears expired result payload fields from PostgreSQL and best-effort removes the object copy while retaining lifecycle metadata.

## MCP for agents

`/mcp` implements authenticated MCP Streamable HTTP using protocol revision `2025-11-25`. It exposes `submit_ocr_document`, `submit_ocr_batch`, `get_ocr_document`, and `cancel_ocr_document`, plus `ocr://documents/{documentId}` resources. A submitted document is an MCP task; agents use `tasks/get`, `tasks/list`, `tasks/result`, or `tasks/cancel`. `GET /mcp` emits durable `notifications/tasks/status` and `notifications/resources/updated` events for terminal tasks and supports `Last-Event-ID`. MCP submission automatically selects its event channel and otherwise reuses the same URL/Base64 validation and document queue as REST.

MCP POST requests require `application/json`, enforce the same 128 MiB envelope cap and strict unknown-field validation as REST, and validate task/document IDs before repository access. Expected input errors are returned as JSON-RPC errors; internal storage and implementation errors are not exposed to agent clients.

## Recognition options

| Field | Validation |
|---|---|
| `recognitionLevel` | `fast` or `accurate` |
| `languages` | Up to 10 unique BCP-47-like identifiers |
| `automaticallyDetectsLanguage` | Boolean |
| `usesLanguageCorrection` | Boolean |
| `customWords` | Up to 100 non-empty values, 128 UTF-8 bytes each, 8 KiB total |
| `minimumTextHeight` | Number from 0 through 1 |

## Validation and security

- Public submissions accept JSON URL or Base64 sources only; multipart and raw object routes are not registered.
- Base64 is limited to 25 MiB decoded per item; URL downloads have a 100 MiB safety ceiling.
- Base64 uses strict standard alphabet and padding validation before decoding.
- File type is determined from magic bytes, not filename or client headers.
- PNG and JPEG structure and pixel counts are validated.
- Truncated and active/encrypted PDF content is rejected.
- Remote input requires HTTPS and blocks credentials, private, loopback, link-local, multicast, unspecified, and metadata addresses.
- DNS results are checked again and pinned when opening the upstream connection to mitigate rebinding.
- Unknown JSON properties are rejected.

## Error model

Errors use `application/problem+json`:

```json
{
  "type": "about:blank",
  "code": "BASE64_TOO_LARGE",
  "status": 400,
  "title": "Base64 input is too large",
  "detail": "batch item 1: base64 input exceeds the decoded size limit. Upload the file to your own storage and submit its HTTPS URL, or remove this item from the batch.",
  "limits": {"maxDecodedBytes": 26214400}
}
```

Important codes include `INVALID_SOURCE`, `INVALID_BASE64`, `BASE64_TOO_LARGE`, `PAYLOAD_TOO_LARGE`, `URL_CONTENT_TOO_LARGE`, `INVALID_URL`, `SSRF_BLOCKED`, `UNSUPPORTED_CONTENT_TYPE`, `UNSUPPORTED_MEDIA_TYPE`, `FILE_VALIDATION_FAILED`, `UNAUTHORIZED`, `RATE_LIMITED`, `QUOTA_EXCEEDED`, `NOT_FOUND`, `STATE_CONFLICT`, and `RESULT_EXPIRED`.

## Discovery and system endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/healthz` | Process liveness |
| `GET` | `/readyz` | PostgreSQL, Redis, and object-storage readiness |
| `GET` | `/v1/ocr/capabilities` | Public options, languages, and limits |
| `GET` | `/api/v1/docs` | Swagger UI |
| `GET` | `/api/v1/openapi.json` | Runtime-generated OpenAPI contract, including `x-mcp-tools` |
