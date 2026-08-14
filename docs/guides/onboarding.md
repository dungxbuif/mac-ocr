---
title: OCR API quickstart
sidebar_position: 1
---

# OCR API quickstart

This guide takes you from an API key to a completed OCR result. You do not need to install this repository or manage the native OCR worker.

## Before you start

Ask your OCR Platform administrator for:

- The API base URL, for example `https://ocr.example.com`.
- An API key beginning with `sk_ocr_`. The key is shown only once when it is created.

Store both values outside source control:

```bash
export OCR_API_URL=https://ocr.example.com
export OCR_API_KEY=sk_ocr_replace_with_your_key
```

Every protected request sends the key in the `Authorization` header:

```http
Authorization: Bearer sk_ocr_replace_with_your_key
```

## 1. Check supported options

```bash
curl --fail --silent --show-error \
  "$OCR_API_URL/v1/ocr/capabilities"
```

The response describes accepted file types, recognition options, and active size limits.

## 2. Submit a document

The simplest source is a public HTTPS URL. The platform detects the real file type from its bytes; you do not send a file-type field.

The smallest complete body is:

```json
{"input":{"url":"https://files.example.com/invoice.pdf"}}
```

All recognition options and notification settings are optional. The longer example below overrides only the fields your integration cares about; every omitted option receives the platform default.

```bash
curl --fail --silent --show-error \
  -X POST "$OCR_API_URL/v1/documents" \
  -H "Authorization: Bearer $OCR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "input": {"url": "https://files.example.com/invoice.pdf"},
    "options": {
      "recognitionLevel": "accurate",
      "languages": ["en-US"],
      "usesLanguageCorrection": true
    }
  }'
```

The API returns `202 Accepted` and a generated `documentId`:

```json
{
  "documentId": "doc_18f673199c0",
  "status": "queued",
  "createdAt": "2026-08-15T08:30:00Z",
  "links": [
    {"rel": "self", "href": "https://ocr.example.com/v1/documents/doc_18f673199c0"}
  ]
}
```

Save the `documentId`. The API deliberately has no endpoint that lists all of your documents.

## 3. Read that exact document

```bash
export DOCUMENT_ID=doc_18f673199c0

curl --fail --silent --show-error \
  "$OCR_API_URL/v1/documents/$DOCUMENT_ID" \
  -H "Authorization: Bearer $OCR_API_KEY"
```

While OCR is running, the response status is `queued` or `processing`. Follow the `Retry-After` response header before polling again. A completed response includes `result` directly; there is no separate `/result` endpoint.

```json
{
  "documentId": "doc_18f673199c0",
  "status": "completed",
  "resultExpiresAt": "2026-08-22T08:30:04Z",
  "result": {
    "text": "Invoice 1042\nTotal $125.00",
    "pageCount": 1,
    "pages": [
      {
        "pageNumber": 1,
        "text": "Invoice 1042\nTotal $125.00",
        "blocks": []
      }
    ]
  }
}
```

Read [OCR response model](../api/OCR_RESPONSE.md) before using confidence values or bounding boxes.

## Upload a large or private file

Do not Base64-encode a large file. The presign request only creates permission to upload; it does not upload any file bytes. Complete all three steps below.

### 1. Request the signed upload URL

```bash
FILE=invoice.pdf
SIZE_BYTES=$(wc -c < "$FILE" | tr -d ' ')

PRESIGN_RESPONSE=$(curl --fail --silent --show-error \
  -X POST "$OCR_API_URL/v1/uploads/presign" \
  -H "Authorization: Bearer $OCR_API_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"filename\":\"invoice.pdf\",\"sizeBytes\":$SIZE_BYTES,\"contentType\":\"application/pdf\"}")

UPLOAD_URL=$(printf '%s' "$PRESIGN_RESPONSE" | jq -er '.uploadUrl')
SOURCE_URL=$(printf '%s' "$PRESIGN_RESPONSE" | jq -er '.sourceUrl')
UPLOAD_LENGTH=$(printf '%s' "$PRESIGN_RESPONSE" | jq -er '.headers["Content-Length"]')
UPLOAD_TYPE=$(printf '%s' "$PRESIGN_RESPONSE" | jq -er '.headers["Content-Type"]')
```

The response contains `method: "PUT"`, a signed HTTPS `uploadUrl`, an internal `sourceUrl`, and the exact headers required by object storage.

If `sizeBytes` is greater than the deployment limit, no URL is created. The API returns HTTP `413` with `Content-Type: application/problem+json`:

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

Follow `rel=capabilities` to discover the deployment limits and `rel=presign` to retry after reducing or splitting the file. Use `limits.maxUploadBytes` instead of hard-coding 100 MiB; deployments may configure a different limit.

### 2. PUT the file directly to object storage

```bash
curl --fail --silent --show-error \
  -X PUT "$UPLOAD_URL" \
  -H "Content-Length: $UPLOAD_LENGTH" \
  -H "Content-Type: $UPLOAD_TYPE" \
  --data-binary "@$FILE"
```

Do not send `OCR_API_KEY` in this request. Authorization is already embedded in the expiring `uploadUrl`. Changing the body length or a signed header causes object storage to reject the upload.

An object-storage rejection is not an OCR Platform problem response. For example, a mismatched `Content-Length` commonly returns an S3-compatible HTTP `403` XML error. Request a new presign URL and retry with the original bytes and exact returned headers; do not submit `sourceUrl` after a failed PUT.

### 3. Submit the returned source URL for OCR

```bash
SUBMISSION_BODY=$(jq -n --arg url "$SOURCE_URL" '{input:{url:$url}}')

curl --fail --silent --show-error \
  -X POST "$OCR_API_URL/v1/documents" \
  -H "Authorization: Bearer $OCR_API_KEY" \
  -H "Content-Type: application/json" \
  --data-binary "$SUBMISSION_BODY"
```

`uploadUrl` and `sourceUrl` are not interchangeable. Never construct an `s3://` URL yourself: submit exactly the `sourceUrl` returned for your account. The server rechecks ownership, object existence, file type, and actual size before reserving quota. See [API reference](../api/API_REFERENCE.md#large-file-upload-through-presigned-urls) for the complete contract.

## Choose how to receive completion

- Poll `GET /v1/documents/{documentId}` for simple integrations.
- Add an HTTPS webhook when your service needs asynchronous delivery.
- Use SSE when a connected client needs task events.
- Use the authenticated MCP endpoint when an agent or MCP client owns the workflow.

See [API reference](../api/API_REFERENCE.md), [MCP integration](../api/MCP_INTEGRATION.md), or open the live <a href="/api/v1/docs" target="_blank" rel="noopener noreferrer">Swagger UI</a> in a separate tab.

## Retention behavior

OCR results are temporary. A completed response tells you when its result expires through `resultExpiresAt`. Store any business data you need before that time. The platform later removes both the cached result and its temporary document metadata; users cannot list or delete documents through the public API.
