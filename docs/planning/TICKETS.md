# Engineering Backlog

Only current, evidence-backed gaps are listed here.

## P1 — Make quota reservation and multi-document persistence atomic

All documents from one batch request are inserted atomically without creating a batch record. Quota reservation remains a separate operation with compensating refund on persistence failure. Prepared input objects are deleted best-effort after validation, quota, or persistence failure; move quota reservation and document persistence into one durable transaction to remove the remaining crash window.

## P0 — Make worker callbacks idempotent

The handler verifies that `attemptId` matches a processing document, rejects stale events, and acknowledges sequential terminal duplicates. Persist `eventId` and make the terminal document transition conditional so concurrent duplicate callbacks cannot race.

## P1 — Add processing leases and recovery

Claimed documents can remain `processing` after a proxy crash or ambiguous dispatch timeout. Add attempt deadlines, a lease/reaper, bounded retry policy, and terminal failure rules.

## P1 — Complete file-parser validation

Use maintained parsers for PDF, TIFF, and WebP. Enforce PDF page limits, image dimensions for every supported raster format, encrypted-document rejection, and decompression-bomb protections. Keep parsing isolated from request-serving resources.

## P1 — Harden webhook authentication configuration

Require worker URL and callback secret outside explicit test mode. Reject startup in production when the secret is missing. Add secret-rotation support if needed.

## P2 — Expand generated OpenAPI contract tests

The contract is generated from Go schemas and has path/MCP smoke tests. Add CI validation for examples and deeper contract tests for direct-array batches, exactly-one-source input, every stable error code, and response/schema compatibility.

## P2 — Add result pagination

Current result responses expose text, pages, and blocks from Redis. Define bounded pagination before allowing worker callback/result payloads above the current 1 MiB callback envelope.

## P2 — Add cleanup policies

Result objects now expire through Redis `RESULT_TTL`, return `410`, and are removed from PostgreSQL/object storage by the cleanup worker. Add retention for input objects, notification events, and failed submissions.

## P1 — Make terminal transition and notification outbox atomic

The outbox has a uniqueness guard and callback retries can repair a missing event. Move document terminal transition and outbox insert into one PostgreSQL transaction to remove the remaining crash window.

## P2 — Improve local launcher verification

Add a process-level smoke test that starts `local-dev`, writes and reads an S3 object, dispatches one local OCR request, verifies the signed callback, and shuts down both children cleanly.
