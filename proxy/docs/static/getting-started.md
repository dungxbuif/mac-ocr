# Getting Started

This guide walks through authentication, document submission, status polling, and result retrieval.

## Requirements

You need an API base URL and an API key. The examples below use environment variables so credentials do not appear in shell history more than necessary.

```bash
export OCR_API_URL="https://ocr.example.com"
export OCR_API_KEY="your-api-key"
```

Every protected request uses a Bearer token:

```http
Authorization: Bearer <api-key>
```

## Submit Base64 content

Encode a local file with standard padded Base64 and place it in JSON. Decoded content is limited to 25 MiB per document.

```bash
OCR_BASE64="$(base64 < receipt.png | tr -d '\n')"
curl -X POST "$OCR_API_URL/v1/documents" \
  -H "Authorization: Bearer $OCR_API_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"input\":{\"base64\":\"$OCR_BASE64\"}}"
```

The API does not accept form uploads and does not accept a declared file type. It decodes Base64 strictly, detects JPEG, PNG, TIFF, WebP, or PDF from magic bytes, and validates the decoded document before queueing it. Oversized Base64 returns `400 BASE64_TOO_LARGE`; host that file and submit its HTTPS URL instead.

## Submit a remote document

Use a URL source when the document is already hosted, especially when it is larger than the Base64 limit. URLs must be public HTTPS endpoints and are snapshotted during submission.

```bash
curl -X POST "$OCR_API_URL/v1/documents" \
  -H "Authorization: Bearer $OCR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "input": {
      "url": "https://files.example.com/receipt.jpg"
    },
    "options": {
      "recognitionLevel": "accurate",
      "languages": ["en-US"],
      "automaticallyDetectsLanguage": true,
      "usesLanguageCorrection": true
    }
  }'
```

The service snapshots remote input at submission time. Processing does not depend on the URL remaining available afterward.

## Check status

```bash
curl \
  -H "Authorization: Bearer $OCR_API_KEY" \
  "$OCR_API_URL/v1/documents/doc_18f673199c0"
```

Documents move through the following public states:

| Status | Meaning |
|---|---|
| `queued` | Accepted and waiting for processing |
| `processing` | Recognition is in progress |
| `completed` | A result is available |
| `failed` | Processing ended with an error |
| `cancelled` | A queued document was cancelled |

While work is pending, respect the `Retry-After` response header. Avoid fixed high-frequency polling loops.

## Read the result

```bash
curl \
  -H "Authorization: Bearer $OCR_API_KEY" \
  "$OCR_API_URL/v1/documents/doc_18f673199c0"
```

A completed response includes normalized text, pages, blocks when available, and the result expiry time:

```json
{
  "documentId": "doc_18f673199c0",
  "status": "completed",
  "result": {
    "text": "INVOICE\nInvoice number: 2026-001\nTotal: 125.00",
    "pageCount": 1,
    "pages": []
  },
  "resultExpiresAt": "2026-08-21T13:40:03Z",
  "_links": {
    "self": {
      "href": "https://ocr.example.com/v1/documents/doc_18f673199c0"
    }
  }
}
```

## Supported inputs

| Input | Recommended use | Limit |
|---|---|---:|
| Public HTTPS URL | Hosted and larger documents | 100 MiB downloaded safety ceiling |
| Base64 JSON | Local or compact documents | 25 MiB decoded per item |
| Batch JSON | Multiple URL or Base64 items | 100 items |

Single JSON envelopes are capped at 36 MiB. Batch and MCP JSON envelopes are capped at 128 MiB, so a batch containing many large documents should use URLs. Accepted formats are JPEG, PNG, TIFF, WebP, and PDF. PDFs containing encryption, scripts, launch actions, embedded files, or automatic actions are rejected.

## Next steps

- Review [Authentication and Limits](authentication.md) before implementing retries.
- Choose [webhook or SSE notifications](notifications.md), or use [MCP](mcp.md) for an agent integration.
- Handle errors according to the [Error Catalog](error-catalog.md).
