# Implementation Status

This file tracks the repository as implemented. Future ideas belong in `TICKETS.md` and must not be described as available API behavior.

## Delivered

### Proxy runtime

- Go HTTP server with graceful shutdown and request middleware.
- PostgreSQL schema initialization and repositories.
- Redis readiness, active API-key lookup cache, two-level request-rate limits, five-minute account configuration cache, and TTL OCR result cache.
- S3-compatible input/result storage and presigned reads.
- Health and readiness endpoints.

### Authentication and administration

- User, account-limit, and API-key repositories.
- `sk_ocr_` API-key generation, hashing, authentication, rate limiting, and revocation.
- Account deactivation through the admin API/UI and CLI, with Redis key-cache invalidation and authoritative checks for API-key and administrator sessions.
- Atomic document quota reserve/refund/reset.
- Admin CLI and session-based admin console.

### OCR APIs

- JSON-only public HTTPS URL and strict Base64 single-document input.
- Direct-array batch input with 1–100 items.
- Shared single/batch document preparation logic.
- Server-generated document identifiers; batch submission creates no batch identifier or persistence record.
- One document resource for status, completed result, expiration, list, and cancellation.
- Batch submission returning independent document IDs without a public batch resource.
- Capabilities endpoint and absolute response links.
- RFC 9457-style problem responses with stable error codes.

### Validation and security

- Unknown JSON-field rejection.
- Exactly-one-source enforcement.
- 36 MiB single and 128 MiB batch/MCP envelope limits; 25 MiB decoded Base64 and 100 MiB URL download limits.
- Magic-byte media allowlist.
- PNG/JPEG structure and pixel-limit checks.
- Basic TIFF/WebP truncation checks.
- PDF EOF, encryption, embedded-file, and active-action rejection.
- HTTPS-only URL fetching with redirect limits, private-address blocking, connection-time DNS checks, and response limits.
- Randomized object keys and cleanup of prepared inputs after validation, quota, or persistence failure.

### Scheduling

- PostgreSQL-backed FIFO claim using `FOR UPDATE SKIP LOCKED`.
- Presigned input dispatch to the OCR worker.
- Busy-worker requeue and infrastructure-failure refund.
- HMAC-signed worker callbacks and result persistence.

### Notifications, retention, and agents

- Per-document webhook or SSE configuration shared by single and batch submissions.
- AES-GCM encryption for webhook secrets, durable outbox rows, signed delivery, and retry.
- Authenticated SSE with event cursor and heartbeat.
- Result TTL served directly from Redis, `410 RESULT_EXPIRED`, periodic PostgreSQL payload cleanup, and best-effort S3 cleanup with a retained tombstone.
- MCP `2025-11-25` Streamable HTTP tools, resources, tasks, cancellation, and task/resource events.

### Developer experience

- One-command local S3 and OCR worker launcher in `local-dev`.
- Local worker downloads the real input before returning deterministic development output.
- Embedded guide site, Swagger UI at `/api/v1/docs`, and runtime-generated OpenAPI at `/api/v1/openapi.json` with MCP tool schemas.

## Contract decisions

- No public engine identification.
- No client-provided document IDs.
- No submission idempotency key.
- Batch body is a JSON array, not `{ "items": [...] }`.
- Batch correlation uses response array `index`.
- Batch IDs are not part of the public contract.
- Batch documents use the same queue and worker path as single documents.

## Verification commands

```bash
cd proxy
go test ./...
go vet ./...

cd ../local-dev/native
go test ./...
go vet ./...
```

The S3 integration test is opt-in and requires the local S3 service:

```bash
cd local-dev
npm install --prefix s3
npm start

cd ../proxy
TEST_S3=1 go test ./internal/repository/s3/tests -run TestRoundTrip -v
```

## Known limitations

- PostgreSQL schema setup is embedded SQL rather than versioned migration files.
- Multi-document rows are persisted in one PostgreSQL transaction; quota uses a separate reservation with compensating refund.
- Claimed processing documents do not yet have a lease/reaper for proxy or worker crashes.
- Worker callback events are signed; sequential duplicates are acknowledged by attempt/state, but event IDs are not durably deduplicated and concurrent duplicate transitions still require a database transaction guard.
- Client webhook outbox events are idempotent per document terminal state, but document transition and outbox insertion are not yet one database transaction.
- Notification event retention cleanup and registered/verified reusable webhook endpoints are not implemented; raw per-document endpoints are accepted as required by the current API contract.
- MCP task support is the experimental `2025-11-25` protocol surface and uses stateless API-key authentication.
- PDF validation is defensive marker/structure validation, not a full PDF parser.
- TIFF and WebP receive signature/truncation checks but not full decoder validation.
- The development worker does not represent production recognition accuracy.
