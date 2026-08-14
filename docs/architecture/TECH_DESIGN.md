# Technical Design — Current Implementation

## Runtime topology

```text
API clients
    │ HTTPS + Bearer API key
    ▼
Go proxy
	├── PostgreSQL: durable users, quota, document lifecycle, outbox
	├── Redis: API-key lookup, rate counters, account-config cache, TTL result serving
    ├── S3 API: immutable input and result objects
    └── Scheduler
           │ POST /ocr with presigned input URL
           ▼
       OCR worker
           │ signed completion/failure callback
           └──────────────────────────────────────► Go proxy
```

## Repository layout

```text
proxy/cmd/proxy/                    application entrypoint
proxy/domain/                       entities and repository interfaces
proxy/internal/rest/                HTTP handlers and middleware
proxy/internal/usecase/             authentication, document, object, system logic
proxy/internal/repository/postgres/ PostgreSQL schema and adapters
proxy/internal/repository/redis/    rate limiting, account config cache, result cache, health
proxy/internal/repository/s3/       S3-compatible object adapter
proxy/internal/scheduler/           durable queue polling and dispatch
proxy/internal/native/              worker HTTP client and webhook signatures
proxy/internal/notifications/       secret encryption, outbox publishing, webhook delivery
proxy/internal/retention/           expired result cleanup
proxy/docs/static/                   embedded guides and Swagger UI shell
local-dev/s3/                       s3rver-based local object storage
local-dev/native/                   one-file local OCR worker simulator
local-dev/start.js                  one-command local service launcher
```

## Submission flow

Single and batch submissions share `prepareQueuedDocument`:

1. Validate recognition options.
2. Infer the source from the HTTP representation.
3. Strictly decode Base64 or fetch public HTTPS input under bounded limits.
4. Detect the actual media type from magic bytes.
5. Perform structural and security validation.
6. Store the normalized input in object storage.
7. Build a server-generated queued document.

Single submission invokes this flow once, reserves one quota unit, and inserts one document. If quota or persistence fails, the newly stored input is deleted best-effort.

Batch submission invokes the same flow in array order, reserves quota for the array length, and inserts all independent documents through `CreateMany` in one PostgreSQL transaction. A validation or persistence failure deletes already prepared input objects best-effort. No batch row, batch foreign key, batch counter, batch queue, or public batch resource exists.

## JSON contract

The handlers use a strict one-value JSON decoder that rejects unknown fields and trailing JSON. They accept `application/json` only and infer the source:

- `input.url` present: URL source.
- `input.base64` present: Base64 source.
- Both or neither: `INVALID_SOURCE`.

The batch handler binds the top-level value directly to a slice; object-wrapped batches are rejected.

## Object storage

Input object keys use:

```text
inputs/{userId}/{UTC timestamp}_{random suffix}_{sanitized filename}
```

The S3 adapter supports put, get, head, delete, list-based health checks, and presigned GET URLs. Production uses any configured S3-compatible endpoint. Local development uses the `s3rver` package.

## Database model

`documents` contains server IDs, owner, state, input metadata, resolved options, result data, attempt ID, and timestamps. It does not contain batch IDs, client IDs, or idempotency keys.

For a multi-document submission, all document rows are committed in one PostgreSQL transaction. Quota reservation occurs first and is compensated with a refund if document persistence fails.

Quota reservation uses an atomic conditional update:

```sql
UPDATE account_configs
SET doc_used = doc_used + $2
WHERE user_id = $1
  AND (doc_quota = 0 OR doc_used + $2 <= doc_quota);
```

Redis caches serialized `account_configs` values for five minutes. Reads are cache-aside; limit changes refresh the cache and quota reserve/refund/reset invalidates it. The database conditional update remains the only quota authority.

Redis also caches active API-key metadata for five minutes under the SHA-256 hash of the plaintext key. The plaintext is never cached. Authentication checks Redis first and falls back to the API-key repository on a miss or cache error, then always checks the user row in PostgreSQL. Creating a key prewarms the cache. Revoking a key updates `revoked_at` and invalidates the account's cached key entries.

Deactivating an account updates the authoritative `users.disabled` flag and invalidates its API-key cache index. API-key requests and administrator-session requests both re-read the user row, so the deactivation boundary does not depend on cache TTL. Keys and documents remain stored, and queued work is not cancelled.

## Scheduler

The scheduler polls once per second and can also be woken by a worker callback. `ClaimNext` atomically transitions the oldest queued document to processing using `FOR UPDATE SKIP LOCKED`. It creates a presigned input URL and calls the worker's `/ocr` endpoint.

- Worker busy: return the document to `queued`.
- Presign or dispatch failure: mark `failed` and refund quota.
- Accepted dispatch: persist the attempt ID and await callback.

## Worker callback

The callback route is `/webhooks/native/events`. Signature input is:

```text
nodeId.timestamp.eventId.<raw body>
```

The proxy accepts completion and failure events. On completion it writes result JSON to object storage, writes the full structured result to Redis with `RESULT_TTL`, stores lifecycle/result metadata in PostgreSQL, creates an idempotent notification outbox event, and wakes the scheduler. Redis write failure leaves the document non-terminal so a callback retry can repair the serving cache.

## Client notifications and MCP

Document rows contain an optional notification channel. Webhook secrets are AES-GCM encrypted with `NOTIFICATION_ENCRYPTION_KEY`. Terminal document events are durable PostgreSQL outbox rows. The dispatcher claims webhook rows with a delivery lease, revalidates public HTTPS targets, signs the exact body, and retries with bounded exponential delay.

SSE reads account-scoped outbox rows by ordered event ID and supports `Last-Event-ID`. MCP uses the same SSE events to emit `notifications/tasks/status` and `notifications/resources/updated`; MCP document tasks map directly to server-generated document IDs.

Result JSON receives the configured `RESULT_TTL`. `GetDocument` checks ownership and lifecycle in PostgreSQL, then reads completed payloads directly from Redis. Expired or missing result keys return `410`. Every minute the retention worker clears expired PostgreSQL payload fields even if best-effort S3 deletion fails, preserving the document tombstone.

## Local development

`local-dev` intentionally stays flat:

- `s3/serve.js` starts the established `s3rver` library on port 9000.
- `native/main.go` starts a simulator on port 8787.
- `start.js` launches both and forwards shutdown signals.

The simulator binds to loopback, authenticates JSON dispatch/config requests, rejects unknown or trailing JSON, downloads the real presigned input under the same 100 MiB ceiling, checks optional SHA-256, produces deterministic development text, retries signed callbacks, drains callback responses for connection reuse, and waits briefly for active jobs during shutdown. It is not a production accuracy emulator.

## Documentation routing

| Path | Handler |
|---|---|
| `/` and guide assets | Embedded documentation filesystem |
| `/api/v1/docs` | Embedded Swagger UI |
| `/api/v1/openapi.json` | Runtime-generated OpenAPI contract |
| `/admin/` | Embedded admin console |
| `/v1/*` | JSON API |
| `/mcp` | MCP Streamable HTTP POST/GET |

Machine routes are registered before the documentation fallback.
