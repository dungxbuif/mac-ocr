# Proxy rate-limit audit — 2026-10-06

Work note before implementation. Requested audit: determine whether existing limits are actually enforced and correct. Read-only production inspection and isolated probes are coordinated by the root agent; this note does not claim production verification.

## Existing behavior and scope

The Go main injects the Redis repository into the authentication service as RateLimiter. RequireAPIKey runs authoritative key/account checks then per-key and aggregate per-account counters on OCR/scan/batch submissions, document/scan reads, presign requests, SSE connection setup, and both MCP methods. Counters are fixed UTC-minute windows, incremented transactionally with two-minute cleanup TTL. Zero disables that principal's limit. Multiple keys share the account counter. Requests rejected later by validation/business logic still consume rate allowance; a key rejection happens before the account counter. Open SSE streams do not consume one request per event. Quotas are distinct PostgreSQL reservations.

Login has a separate in-process five-attempt/minute limiter keyed by socket peer IP and normalized email hash; it is not the Redis API limiter. Behind ingress, peer-IP buckets can be shared across clients. Worker sockets/callbacks, health/docs, capabilities, and cookie-session admin/self-service endpoints do not use API-key RPM limits.

## Intended corrections

1. Wrap Redis counter failures with domain.ErrStorageUnavailable. Continue failing closed, but return HTTP 503 rather than accidental HTTP 500; never expose upstream connection details.
2. RATE_LIMITED Retry-After should report whole seconds until the next UTC minute instead of always one second. Do not change configured RPM or fixed-window policy.
3. Reconcile stale internal/public authentication descriptions: authoritative key rows are checked in PostgreSQL on each request; positive key caches cannot bypass revocation. Clarify routes, fixed windows, zero semantics, and SSE setup.

## Verification plan

Observe failing regressions before implementing each correction. Run real isolated Redis concurrent Allow tests: exact accepted count, principal isolation, disabled zero limit, TTL cleanup and closed-on-backend-error. Exercise authentication with real Redis across two keys sharing one account, plus per-key precedence. Confirm Retry-After boundaries and HTTP 503 sanitized errors. Verify ordinary authenticated users cannot read worker roster or send controls. Run full Go race suite with local isolated PostgreSQL and Redis; retain production checks as separate evidence.

## Production-safe probe

Use a dedicated disposable account/key with RPM 2–3; send only GET for a nonexistent document (expect 404 until allowance exhausted, then429), avoiding job submission and paid work. Validate key and aggregate limits separately and verify recovery after the UTC-minute boundary. Do not flood production, modify normal users, clear global Redis, or put keys in command output/history. Record only status codes and retry headers; delete the test account/key afterward through normal authenticated controls.
