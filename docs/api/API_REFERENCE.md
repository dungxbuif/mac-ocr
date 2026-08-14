# OCR API Reference

The OCR API is asynchronous. Submission endpoints return `202 Accepted` and durable server-generated document identifiers. Clients poll documents, select a notification channel, or use MCP task events until processing reaches a terminal state.

Interactive Swagger UI is served at `/api/v1/docs`. The OpenAPI 3.1 contract is generated from runtime Go schemas and served at `/api/v1/openapi.json`; there is no separately maintained YAML contract.

Only `application/json` submissions are supported. Single-document envelopes are capped at 36 MiB; batch and MCP envelopes are capped at 128 MiB. Exceeding an envelope cap returns `413 PAYLOAD_TOO_LARGE`. Base64 is limited to 25 MiB decoded per item and returns `400 BASE64_TOO_LARGE`; URL downloads and app-owned presigned-upload objects have a 100 MiB default resource-safety ceiling and return `413 URL_CONTENT_TOO_LARGE`.

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

JSON submissions contain exactly one source. `url` may be either a public HTTPS URL or an app-owned `s3://...` `sourceUrl` returned by the presigned upload endpoint:

```json
{"input":{"url":"https://files.example.com/invoice.png"}}
```

```json
{"input":{"base64":"iVBORw0KGgo..."}}
```

```json
{"input":{"url":"s3://macocr-inputs/uploads/123/20260814T130000.000_abcd_invoice.pdf"}}
```

Unknown JSON fields are rejected. A JSON input containing both `url` and `base64`, or neither, returns `400 INVALID_SOURCE`.

## Large file upload through presigned URLs

### `POST /v1/uploads/presign`

Use this route when a file is too large or inefficient for Base64. The route requires the same Bearer API key as OCR submission.

Request:

```json
{
  "filename": "invoice.pdf",
  "sizeBytes": 73400320,
  "contentType": "application/pdf"
}
```

Response:

```json
{
  "method": "PUT",
  "uploadUrl": "https://storage.example.com/...",
  "sourceUrl": "s3://macocr-inputs/uploads/123/...",
  "headers": {
    "Content-Length": "73400320",
    "Content-Type": "application/pdf"
  },
  "sizeBytes": 73400320,
  "maxUploadBytes": 104857600,
  "expiresAt": "2026-08-14T14:15:00Z",
  "reservationExpiresAt": "2026-08-15T14:00:00Z"
}
```

`POST /v1/uploads/presign` does not receive the file bytes. Upload the exact file directly to `uploadUrl` with the returned `PUT` method and every returned header, then submit OCR with `{"input":{"url":"<sourceUrl>"}}`. Do not send the OCR API key to `uploadUrl`; its temporary authorization is already encoded in the signed URL. `uploadUrl` is the external upload destination, while `sourceUrl` is the app-owned internal reference accepted by OCR submission.

The presigned SigV4 request binds the declared size as `Content-Length`; a production-equivalent S3 service must reject a different length. The service also accepts only `sourceUrl` values whose bucket and key prefix belong to the authenticated user, checks object metadata, and streams the uploaded bytes during OCR submission under the same hard ceiling. A client therefore cannot bypass the limit by declaring a small `sizeBytes` and later submitting a larger object.

Presign creation atomically reserves `sizeBytes` against the account's aggregate storage quota. Concurrent presign calls cannot oversubscribe the quota. On successful document creation the reservation becomes retained-byte usage in the same PostgreSQL transaction as document quota and row creation. An unsubmitted reservation expires at `reservationExpiresAt`; the retention worker deletes any corresponding object and refunds the reserved bytes. `expiresAt` applies only to the temporary PUT URL.

The aggregate check is `storage_used_bytes + storage_reserved_bytes + requested size <= storage_quota_bytes`. Direct URL and Base64 submissions become used bytes when accepted; a presigned upload is reserved first and becomes used only when its `sourceUrl` is submitted. A failed object-storage signing operation is refunded immediately. A failed or abandoned client PUT remains reserved until `reservationExpiresAt`; there is intentionally no public cancel/delete route. If the PUT URL expires before upload, request a new presign only after the old reservation expires or after the account has sufficient remaining quota.

### Upload size failures

| Failure point | HTTP response | Machine code/body |
|---|---:|---|
| Presign `sizeBytes` exceeds `MAX_UPLOAD_BYTES` | `413` from OCR Platform | `URL_CONTENT_TOO_LARGE`, with `limits.maxUploadBytes` |
| Retained plus reserved bytes would exceed the account quota | `429` from OCR Platform | `STORAGE_QUOTA_EXCEEDED` |
| Presigned PUT uses a different length or signed header | Usually `403` from object storage | Provider-specific S3 XML; not `application/problem+json` |
| Submitted app-owned object is actually over the server limit | `413` from OCR Platform | `URL_CONTENT_TOO_LARGE` |
| Decoded Base64 exceeds 25 MiB | `400` from OCR Platform | `BASE64_TOO_LARGE`, with `limits.maxDecodedBytes` |
| Complete JSON envelope exceeds its route limit | `413` from OCR Platform | `PAYLOAD_TOO_LARGE`, with `limits.maxRequestBytes` |

Oversized presign example:

```http
HTTP/1.1 413 Request Entity Too Large
Content-Type: application/problem+json
```

```json
{
  "type": "about:blank",
  "code": "URL_CONTENT_TOO_LARGE",
  "status": 413,
  "title": "Upload file is too large",
  "detail": "sizeBytes exceeds the configured upload limit",
  "limits": {"maxUploadBytes": 104857600},
  "links": [
    {"rel": "presign", "href": "/v1/uploads/presign", "method": "POST"},
    {"rel": "capabilities", "href": "/v1/ocr/capabilities", "method": "GET"}
  ]
}
```

The example limit is the default. Follow `rel=capabilities` to discover deployment limits and `rel=presign` to retry after reducing or splitting the file. Clients should read `limits.maxUploadBytes` because a deployment may use another value. A rejected presign does not reserve document quota or bytes and does not create an upload URL.

Aggregate quota failure follows the same HATEOAS link-array shape:

```json
{
  "type": "about:blank",
  "code": "STORAGE_QUOTA_EXCEEDED",
  "status": 429,
  "title": "Storage quota exceeded",
  "detail": "storage quota exceeded",
  "links": [
    {"rel": "self", "href": "/v1/uploads/presign", "method": "POST"},
    {"rel": "capabilities", "href": "/v1/ocr/capabilities", "method": "GET"}
  ]
}
```

Honor `Retry-After`, but note that space becomes available only when retained inputs or abandoned reservations expire, or when an administrator raises the account quota.

## Single document

### `POST /v1/documents`

Only `input` is required. It must contain exactly one source. This is a complete minimal request:

```json
{"input":{"url":"https://files.example.com/invoice.png"}}
```

The equivalent presigned-upload request is `{"input":{"url":"<sourceUrl returned by presign>"}}`. `options` and `notification` are optional. Omitting `options`, sending `options: {}`, or sending only selected option fields applies defaults to every unspecified field: accurate recognition, `vi-VN` and `en-US`, automatic language detection, language correction, no custom words, and minimum text height `0`.

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
  "links": [
    {"rel": "self", "href": "https://ocr.dungxbuif.com/v1/documents/doc_18f673199c0"}
  ]
}
```

### `GET /v1/documents/{documentId}`

Returns `queued`, `processing`, `completed`, `failed`, or `cancelled`. Pending resources include `Retry-After`. A completed document includes the full cached `result` in this same response; no `/result` endpoint exists. Conditional reads support `If-None-Match`.

There is no public list, delete, or cancel operation. Clients must retain the returned `documentId` and read that exact resource. Unknown or expired document IDs return `404`; a completed document whose Redis result expired while its metadata is still retained returns `410`.

For the complete result payload, Apple Vision field provenance, confidence semantics, bounding-box coordinates, PDF page joining, and client examples, see [OCR response model](OCR_RESPONSE.md).

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

Completed document responses include `resultExpiresAt`. The default `RESULT_TTL` is 168 hours. The callback writes the full result to Redis with this TTL, and document reads obtain the payload directly from Redis. A cache miss or elapsed TTL returns `410 RESULT_EXPIRED` while metadata remains. Every minute, retention clears expired result/input payload fields, removes unreferenced presigned uploads after `UPLOAD_TTL` (default 24 hours), and deletes terminal PostgreSQL document rows after `DOCUMENT_TTL` (default 2160 hours, or 90 days). Delivered notification audit events use the same 90-day default. Reads return `404 NOT_FOUND` after metadata deletion.

## MCP for agents

`/mcp` implements authenticated MCP Streamable HTTP using protocol revision `2025-11-25`. It exposes `submit_ocr_document`, `submit_ocr_batch`, and `get_ocr_document`, plus `ocr://documents/{documentId}` resources. A submitted document is an MCP task; agents may read one known task with `tasks/get` or `tasks/result`. Task listing and cancellation are not exposed. `GET /mcp` emits durable `notifications/tasks/status` and `notifications/resources/updated` events for terminal tasks and supports `Last-Event-ID`. MCP submission automatically selects its event channel and otherwise reuses the same URL/Base64 validation and document queue as REST.

MCP POST requests require `application/json`, enforce the same 128 MiB envelope cap and strict unknown-field validation as REST, and validate task/document IDs before repository access. Expected input errors are returned as JSON-RPC errors; internal storage and implementation errors are not exposed to agent clients.

See [MCP integration](MCP_INTEGRATION.md) for exact JSON-RPC envelopes, tool/task/resource response differences, event resume behavior, errors, and retention rules.

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
- Large uploads use `POST /v1/uploads/presign`, direct `PUT` to object storage, then a normal JSON OCR submission with the returned app-owned `sourceUrl`.
- Base64 is limited to 25 MiB decoded per item; URL downloads have a 100 MiB safety ceiling.
- Base64 uses strict standard alphabet and padding validation before decoding.
- File type is determined from magic bytes, not filename or client headers.
- PNG and JPEG structure and pixel counts are validated.
- Truncated and active/encrypted PDF content is rejected.
- Remote input requires HTTPS and blocks credentials, private, loopback, link-local, multicast, unspecified, and metadata addresses. App-owned `s3://` source URLs are resolved internally by bucket/key ownership instead of through public HTTP fetch.
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
| `POST` | `/v1/uploads/presign` | Create authenticated presigned upload URLs for large files |
| `GET` | `/api/v1/docs` | Swagger UI |
| `GET` | `/api/v1/openapi.json` | Runtime-generated OpenAPI contract, including `x-mcp-tools` |
