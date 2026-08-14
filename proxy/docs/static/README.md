# OCR Platform

OCR Platform is an API-first service for extracting structured text from images and documents. It provides durable asynchronous workflows for applications, data pipelines, automation tools, and MCP-compatible agents.

## Why OCR Platform?

- **Simple asynchronous API** — submit work once, receive a stable document ID, and retrieve the result when processing completes.
- **Simple JSON input** — submit exactly one public HTTPS URL or strict Base64 payload; no client-supplied file type is trusted.
- **Batch processing** — submit up to 100 independently tracked documents in a single request.
- **Push delivery** — choose signed webhooks, resumable server-sent events, or MCP task/resource notifications.
- **Production-friendly controls** — API-key authentication, rate limits, document quotas, and tenant isolation.
- **Consistent HTTP contracts** — absolute resource links, standard status codes, and RFC 9457 Problem Details errors.
- **Language-aware recognition** — choose language priorities, automatic language detection, recognition level, and correction behavior per document.

## How it works

1. Submit a document to `POST /v1/documents`.
2. Receive `202 Accepted` with a durable `documentId`.
3. Follow the response links or poll the document resource.
4. Read the normalized OCR result from the same document resource after completion.

The document resource is the source of truth for lifecycle state. Completed result payloads remain available for a configured TTL, then the endpoint returns `410 RESULT_EXPIRED` while the document tombstone remains.

## Quick start

```bash
curl -X POST https://ocr.example.com/v1/documents \
  -H "Authorization: Bearer $OCR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"input":{"url":"https://files.example.com/invoice.png"}}'
```

```json
{
  "documentId": "doc_18f673199c0",
  "status": "queued",
  "createdAt": "2026-08-14T13:40:00Z",
  "_links": {
    "self": {
      "href": "https://ocr.example.com/v1/documents/doc_18f673199c0"
    }
  }
}
```

Continue with [Getting Started](getting-started.md), then review [Authentication and Limits](authentication.md), [Notifications](notifications.md), [MCP for Agents](mcp.md), and the [Error Catalog](error-catalog.md).

For an interactive endpoint reference, open [Swagger UI](/api/v1/docs).

## API stability

Versioned endpoints live under `/v1`. Additive response fields may be introduced without a version change, so clients should ignore fields they do not recognize. Breaking contract changes are released under a new API version.

## License and contributions

This repository is under active development. Bug reports and focused pull requests are welcome. Please avoid including credentials, customer documents, or sensitive OCR output in issues and test fixtures.
