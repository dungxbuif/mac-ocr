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

The raw key must match `sk_ocr_` followed by 48 hexadecimal characters before any lookup. The server hashes it with SHA-256 and checks a five-minute Redis metadata cache first. Cache misses and cache errors fall back to PostgreSQL. Cached entries never contain the plaintext secret, and the disabled-user check still reads PostgreSQL on every request.

## Rate limiting

Redis maintains fixed one-minute counters for both key and account principals. A transactional pipeline increments each counter and applies a two-minute expiry. Exceeding either configured RPM returns `429 RATE_LIMITED` with `Retry-After`.

Redis failure currently causes authentication to fail closed because a limit decision cannot be made.

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

All account-management and API-key routes require an administrator session. Mutating requests also require the session's CSRF token.
