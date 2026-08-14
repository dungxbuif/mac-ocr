# Authentication and Limits

## API keys

Protected API endpoints require a Bearer API key:

```http
Authorization: Bearer <api-key>
```

Keys use the `sk_ocr_` prefix followed by 48 hexadecimal characters. Treat them like passwords: keep them in a secret manager, transmit them only over HTTPS, and never commit them to source control. A rejected, revoked, or disabled credential returns `401 Unauthorized`. Revocation invalidates cached lookup entries; plaintext keys are never stored in the cache.

When an administrator deactivates an account, all of its keys stop authenticating immediately. Existing document history remains available after reactivation, and work accepted before deactivation is not automatically cancelled.

## Rate limits

Requests are limited at two levels:

- Per API key, allowing separate budgets for individual integrations.
- Per account, protecting the aggregate usage of all keys owned by that account.

When either limit is exceeded, the API returns `429 Too Many Requests`. Clients should honor `Retry-After`, apply exponential backoff, and add jitter when many workers share the same credential.

## Document quotas

Document quotas limit accepted OCR work rather than raw HTTP requests.

- A valid single-document submission consumes one unit.
- A batch consumes one unit for each accepted item.
- Rejected input does not consume document quota.
- A reserved unit is returned when work cannot be created because of an internal failure.

A depleted quota returns `429` with code `QUOTA_EXCEEDED`. Retrying the same request without a quota change will not succeed.

## Batch submission

Batch requests accept a JSON array of up to 100 items. Each item uses exactly the same input and recognition options as a single-document request. Items are queued as normal documents in array order.

```bash
curl -X POST "$OCR_API_URL/v1/batches" \
  -H "Authorization: Bearer $OCR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '[
    {
      "input": {"url": "https://files.example.com/invoice-001.png"},
      "options": {"recognitionLevel": "accurate", "languages": ["en-US"]}
    },
    {
      "input": {"base64": "iVBORw0KGgo..."}
    }
  ]'
```

The response returns one `documentId` per array index. If any item is invalid, the whole request returns `400` before quota is reserved or documents are queued.
