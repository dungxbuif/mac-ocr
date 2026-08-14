---
slug: /
title: OCR Platform documentation
sidebar_position: 1
---

# OCR Platform documentation

Integrate image and PDF text recognition through REST or MCP. OCR Platform accepts a public URL, Base64 input, or an account-owned presigned upload and returns a normalized result produced by the native Apple Vision worker.

<div className="homepage-actions">
  <a className="button button--primary button--lg" href="/admin/" target="_blank" rel="noopener noreferrer">Open dashboard</a>
  <a className="button button--secondary button--lg" href="/guides/onboarding">Read quickstart</a>
</div>

## Start here

1. Follow the [OCR API quickstart](guides/onboarding.md) to submit a document and read its result.
2. Use the [API reference](api/API_REFERENCE.md) for authentication, upload, polling, webhook, SSE, limits, and error contracts.
3. Read the [OCR response model](api/OCR_RESPONSE.md) before consuming text blocks, confidence values, or bounding boxes.
4. Follow [MCP integration](api/MCP_INTEGRATION.md) when connecting an agent or MCP client.

## Core contract

- Authenticate protected requests with your `sk_ocr_...` API key.
- Keep every returned `documentId`; public document listing is intentionally unavailable.
- Read one known document at `GET /v1/documents/{documentId}`.
- Use presigned upload for large or private files and submit only the returned `sourceUrl`.
- Treat OCR results as temporary and persist what your application needs before `resultExpiresAt`.

The live OpenAPI 3.1 contract is available at `/api/v1/openapi.json`, with interactive <a href="/api/v1/docs" target="_blank" rel="noopener noreferrer">Swagger UI</a> opening in a separate tab.
