# Error Catalog

API errors use the RFC 9457 Problem Details format. Applications should branch on `code` and `status`; `detail` is intended for operators and may become more specific over time.

```json
{
  "type": "about:blank",
  "code": "QUOTA_EXCEEDED",
  "status": 429,
  "title": "Document quota exceeded",
  "detail": "Document quota of 1000 items exceeded"
}
```

| HTTP Status | Code | Meaning | Action |
|---|---|---|---|
| 400 | `INVALID_INPUT` | Malformed JSON or validation failed | Fix request payload |
| 400 | `INVALID_SOURCE` | Input has neither source or has both `url` and `base64` | Send exactly one source |
| 400 | `INVALID_BASE64` | Base64 alphabet or padding is invalid | Encode the original file with standard padded Base64 |
| 400 | `BASE64_TOO_LARGE` | Decoded Base64 exceeds 25 MiB | Host the file and send its public HTTPS URL, or remove that batch item |
| 400 | `INVALID_URL` | URL is malformed, non-HTTPS, unreachable, or returns an error | Provide a reachable HTTPS URL |
| 400 | `SSRF_BLOCKED` | URL resolves to a private, local, link-local, or metadata address | Host the file on a public HTTPS endpoint |
| 400 | `FILE_VALIDATION_FAILED` | File is truncated, malformed, oversized by pixels, encrypted, or contains unsupported active PDF content | Repair or re-export the document |
| 401 | `UNAUTHORIZED` | Missing, malformed, unknown, revoked, or account-disabled API key | Check the Bearer token or contact an administrator |
| 403 | `FORBIDDEN` | Authenticated caller lacks permission, or an administrator CSRF check failed | Verify permissions and administrator session headers |
| 404 | `NOT_FOUND` | Document ID does not exist or is not owned by the API key account | Verify the document ID |
| 410 | `RESULT_EXPIRED` | The document tombstone exists but its result TTL elapsed | Re-submit the source if a new result is required |
| 409 | `STATE_CONFLICT` | Resource state conflict (e.g. cancel processing doc) | Wait for job completion |
| 413 | `PAYLOAD_TOO_LARGE` | Complete JSON envelope exceeds 36 MiB for single or 128 MiB for batch/MCP | Use URL inputs or split the batch |
| 413 | `URL_CONTENT_TOO_LARGE` | Downloaded URL content exceeds the 100 MiB safety ceiling | Host a smaller representation |
| 415 | `UNSUPPORTED_CONTENT_TYPE` | Submission is not `application/json` | Send JSON with `input.url` or `input.base64` |
| 415 | `UNSUPPORTED_MEDIA_TYPE` | Magic bytes are not JPEG, PNG, TIFF, WebP, or PDF | Upload a supported image or PDF; changing the extension will not work |
| 429 | `RATE_LIMITED` | Request rate limit exceeded | Honor `Retry-After` and retry with backoff |
| 429 | `QUOTA_EXCEEDED` | Exceeded total document quota | Contact admin to reset quota |
| 503 | `SERVICE_UNAVAILABLE` | PostgreSQL, Redis, object storage, notification outbox, or result cache is unavailable | Retry with exponential backoff |
