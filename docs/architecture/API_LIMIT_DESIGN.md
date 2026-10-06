# Authentication, Rate Limits, and Quotas

## API key format

New plaintext credentials use:

```text
sk_ocr_<48 lowercase hexadecimal characters>
```

The database stores a SHA-256 hash and a display prefix containing `sk_ocr_` plus eight random characters. Plaintext is returned only at creation time.

## Authentication checks

For each protected request the service:

1. Parses `Authorization: Bearer <key>`.
2. Hashes the supplied key and looks up the database row.
3. Rejects missing, unknown, or revoked keys.
4. Rejects disabled accounts.
5. Applies the per-key rate limit.
6. Applies the aggregate per-account rate limit.

The raw key must match `sk_ocr_` followed by 48 hexadecimal characters before any lookup. The server hashes it with SHA-256 and reads the authoritative key row in PostgreSQL on every request, then checks the account state. Redis may store five-minute metadata entries, but a positive cache never bypasses authoritative revocation or deactivation. Cached entries never contain the plaintext secret.

## Rate limiting

Redis maintains fixed UTC calendar-minute counters for both key and account principals. Each counter is incremented in a transactional pipeline with a two-minute cleanup expiry. Exceeding either configured RPM returns `429 RATE_LIMITED` and `Retry-After` in whole seconds until the next minute. The calendar boundary permits bursts across adjacent windows; this is a fixed-window policy rather than a rolling 60-second limit.

RequireAPIKey enforces both counters on OCR/scan/batch submissions, document/scan GET reads, `/v1/uploads/presign`, `/v1/events` connection setup, and GET/POST `/mcp`. Limits count HTTP requests, not documents or MCP tool calls. A batch is one rate unit; its documents reserve quota individually. Invalid authenticated requests still count. Per-key rejection occurs first and does not increment the account counter. Already-open SSE streams revalidate credentials without consuming further rate units.

Zero on an existing key disables its per-key counter; zero account RPM disables the account counter. Both must be zero for unlimited requests. Creating a key with zero inherits the account RPM, and creation clamps a requested key limit above a positive account limit. Updating an existing key can set zero or a larger value; the independent aggregate account limit still caps traffic across all keys, so self-service cannot bypass it.

API-key limits do not apply to public docs/health/capabilities, worker sockets/results, or cookie-session admin/self-service controls. Login has a separate in-memory five-attempt/minute policy per socket peer IP and normalized email hash, including successful attempts. Behind ingress several clients may share the peer-IP bucket; this policy is per proxy process, unlike the Redis-backed API counters.

Redis rate-counter failure causes authentication to fail closed with HTTP 503 because a limit decision cannot be made. Client errors omit Redis connection details.

Account quota/limit rows are cached in Redis for five minutes using cache-aside reads. Administrative limit changes refresh the entry. Quota reservation, refund, and reset invalidate it. Cache failures fall back to PostgreSQL for configuration reads; atomic quota enforcement always runs in PostgreSQL.

New keys prewarm the Redis API-key cache. Revocation writes `revoked_at` in PostgreSQL and deletes every indexed cached key for the owning account, preventing a revoked key from surviving as a cache hit.

Account deactivation sets `users.disabled`, invalidates the same account-scoped API-key cache index, and keeps all key/document records for auditability. API-key authentication and administrator sessions verify the authoritative user row on every request, so deactivation is immediate even if Redis is unavailable. Deactivation does not cancel work that was already queued. Reactivation is an explicit administrator update.

## Document quota

`account_configs` contains:

| Field | Meaning |
|---|---|
| `rate_limit_rpm` | Aggregate request limit; zero disables the aggregate limit |
| `doc_quota` | Maximum accepted documents; zero means unlimited |
| `doc_used` | Atomic accepted-document counter |
| `quota_reset_at` | Optional administrative metadata |

Quota is reserved only after file/options validation:

- Single submission: reserve 1.
- Batch submission: reserve the number of array items.
- Invalid submission: reserve 0.
- Public user cancellation is not exposed. Internal terminal failures may refund quota according to the dispatch/finalization path.
- Dispatch infrastructure failure: refund 1.

Exhausted quota returns `429 QUOTA_EXCEEDED`.

## Aggregate storage-byte quota

`storage_quota_bytes` caps retained input bytes plus outstanding presigned reservations; zero means unlimited. `storage_used_bytes` tracks input objects referenced by retained documents and `storage_reserved_bytes` tracks presigned uploads not yet submitted. Presign reservation and document consumption use PostgreSQL row locks/transactions, so concurrent requests cannot oversubscribe the account. Reservation cleanup uses the same account-first lock order as submission and deletes an object only after atomically releasing its still-reserved row; a concurrently consumed document object is therefore preserved. Input retention cleanup decrements usage, while expired unsubmitted reservations delete the object and refund reserved bytes. Exhaustion returns `429 STORAGE_QUOTA_EXCEEDED` with HATEOAS `self` and `capabilities` links.

## Account and key management

The repository includes CLI commands for seeding an administrator, creating users, changing limits, resetting usage, disabling accounts, and creating/revoking keys. CLI key revocation requires both owner user ID and key ID so it can invalidate the correct Redis cache index. The web admin uses session and CSRF cookies for its protected dashboard endpoints and exposes an explicit account-deactivation action.

Administrators manage users and account limits. Authenticated ordinary users may create/list/update/revoke keys, read account configuration, and reset passwords for their own account; cross-account requests are forbidden. Mutating session requests require the session's CSRF token. Worker roster and worker controls remain administrator-only.
