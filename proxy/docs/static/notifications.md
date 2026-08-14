# Notifications and result retention

Polling `GET /v1/documents/{documentId}` is authoritative for lifecycle state. A completed response contains the cached `result` and `resultExpiresAt`; there is no separate result endpoint. Redis serves the result payload directly until its TTL expires. After expiry, the endpoint returns `410 RESULT_EXPIRED`; periodic retention removes result payload fields from PostgreSQL and best-effort deletes the object copy while preserving the document tombstone.

Add a webhook to a single JSON submission or an individual batch item:

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

The target must be public HTTPS. Private, local, metadata, credential-bearing, and unsafe redirect targets are rejected. Secrets contain 16–256 characters, are encrypted at rest, and never appear in responses or logs.

Webhook delivery is at least once and terminal events are persisted in a durable outbox. Verify `X-OCR-Signature`, a lowercase hexadecimal HMAC-SHA256 over:

```text
<X-OCR-Timestamp>.<X-OCR-Event-Id>.<raw-request-body>
```

For SSE, submit `"notification":{"type":"sse"}`, then open `GET /v1/events` using the same Bearer API key. The stream sends terminal event IDs, a retry hint, and heartbeats. Reconnect with `Last-Event-ID` to resume durable events.

Batch is only a submission convenience. Each item selects and receives its own notification channel and remains independently retrievable by `documentId`.
