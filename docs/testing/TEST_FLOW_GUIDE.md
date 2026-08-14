# Manual Test Flow

This guide matches the current API contract.

## 1. Start local dependencies

PostgreSQL and Redis must already be available at the URLs configured in `proxy/.env`.

Start S3-compatible storage and the local OCR worker:

```bash
cd local-dev
npm install --prefix s3
npm start
```

Expected endpoints:

- S3: `http://localhost:9000`
- Local OCR worker: `http://localhost:8787/health`

## 2. Configure and start the proxy

```bash
cp proxy/.env.example proxy/.env
cd proxy
go run ./cmd/proxy
```

Verify:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

Swagger UI is available at `http://localhost:8080/api/v1/docs`.

## 3. Create a test account and key

Use the admin CLI rather than public account-management HTTP routes:

```bash
cd proxy
go run ./cmd/admin seed --email admin@example.com --password 'replace-this-password'
go run ./cmd/admin create-user --email user@example.com --rate 120 --quota 100
go run ./cmd/admin create-key --user-id 2 --name local-test --rate 120
```

Copy the one-time plaintext `sk_ocr_...` value:

```bash
export OCR_API_URL=http://localhost:8080
export OCR_API_KEY='sk_ocr_replace_with_generated_key'
```

Verify account deactivation and reactivation before continuing:

```bash
go run ./cmd/admin disable-user --user-id 2
curl -i -H "Authorization: Bearer $OCR_API_KEY" "$OCR_API_URL/v1/documents"
go run ./cmd/admin enable-user --user-id 2
```

The request made while disabled must return `401 UNAUTHORIZED`. Deactivation retains keys, document history, and queued work; enabling the account allows an otherwise active key to authenticate again. To test permanent key revocation, use `go run ./cmd/admin revoke-key --user-id 2 --key-id <key-id>`; revocation invalidates the account's Redis key cache.

## 4. Verify form uploads are rejected

```bash
curl -i -X POST "$OCR_API_URL/v1/documents" \
  -H "Authorization: Bearer $OCR_API_KEY" \
  -F "file=@sample.png"
```

Expected: `415 UNSUPPORTED_CONTENT_TYPE`. Public submission is JSON-only.

## 5. Submit one URL document

```bash
curl -i -X POST "$OCR_API_URL/v1/documents" \
  -H "Authorization: Bearer $OCR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "input": {"url": "https://files.example.com/sample.png"},
    "options": {"recognitionLevel": "accurate", "languages": ["en-US"]}
  }'
```

Do not include `type`, `mediaType`, `clientDocumentId`, or `Idempotency-Key`.

## 6. Submit one Base64 document

```bash
BASE64_DATA=$(base64 < sample.png | tr -d '\n')
curl -i -X POST "$OCR_API_URL/v1/documents" \
  -H "Authorization: Bearer $OCR_API_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"input\":{\"base64\":\"$BASE64_DATA\"}}"
```

Expected: `202` for a supported valid file up to 25 MiB decoded, with `documentId`, `status: queued`, `Location`, `Retry-After`, and `_links`.

## 7. Submit a direct-array batch

```bash
curl -i -X POST "$OCR_API_URL/v1/batches" \
  -H "Authorization: Bearer $OCR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '[
    {"input":{"url":"https://files.example.com/a.png"}},
    {"input":{"url":"https://files.example.com/b.png"},"options":{"languages":["en-US"]}}
  ]'
```

Expected: `202` and two response items with `index` values `0` and `1` plus server-generated `documentId` values. No public batch ID is returned.

## 8. Poll status and result

```bash
curl -H "Authorization: Bearer $OCR_API_KEY" \
  "$OCR_API_URL/v1/documents/$DOCUMENT_ID"
```

The local worker downloads the stored input and calls the proxy back asynchronously. The result text is deterministic development metadata, not a production accuracy result.

## 9. Negative validation cases

### Both sources

```json
{"input":{"url":"https://files.example.com/a.png","base64":"AAAA"}}
```

Expected: `400 INVALID_SOURCE`.

### Invalid Base64

```json
{"input":{"base64":"not-valid***"}}
```

Expected: `400 INVALID_BASE64`.

### Base64 over 25 MiB decoded

Expected: `400 BASE64_TOO_LARGE`, `limits.maxDecodedBytes: 26214400`, and guidance to host the file behind a public HTTPS URL or remove the batch item.

### Batch item failure

```json
[
  {"input":{"url":"https://files.example.com/a.png"}},
  {"input":{"base64":"not-valid***"}}
]
```

Expected: `400 INVALID_BASE64` with `batch item 1` in `detail`; no quota reservation or queued document.

### Private URL

```json
{"input":{"url":"https://127.0.0.1/secret"}}
```

Expected: `400 SSRF_BLOCKED`.

### Fake extension or script input

Submitting Base64 or URL content containing HTML, SVG, JavaScript, or renamed text returns `415 UNSUPPORTED_MEDIA_TYPE`. Active or encrypted PDF content returns `400 FILE_VALIDATION_FAILED`.

### Unknown property

```json
{"input":{"url":"https://files.example.com/a.png","unexpected":true}}
```

Expected: `400 INVALID_INPUT`.

### Trailing JSON

```text
{"input":{"url":"https://files.example.com/a.png"}} {}
```

Expected: `400 INVALID_INPUT` because every request body must contain exactly one JSON value.

## 10. Verify notification and result TTL paths

Submit one document with `"notification":{"type":"sse"}`, then connect to `GET /v1/events` with the same Bearer key. A terminal event should arrive with an `id`; reconnecting with `Last-Event-ID` resumes after that event. For webhooks, verify `X-OCR-Signature` against `timestamp.eventId.rawBody` using the configured secret.

On completion, verify `GET /v1/documents/{id}` returns the structured Redis-backed result and `resultExpiresAt`. With a short test `RESULT_TTL`, the same read must return `410 RESULT_EXPIRED` after expiry, and the periodic worker must clear PostgreSQL `result_text`/`result_key` while leaving the document row.

For MCP, initialize at `POST /mcp`, call `tools/list`, submit with `submit_ocr_document`, and keep authenticated `GET /mcp` open. Verify task-status and resource-updated messages use the returned document ID.

## 11. Automated checks

```bash
cd proxy
go test ./...
go vet ./...

cd ../local-dev/native
go test ./...
go vet ./...
```
