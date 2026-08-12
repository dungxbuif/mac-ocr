# Implementation Plan — Mac OCR Platform

**Status:** Ready for execution planning
**Sources of truth:** [SRS.md](SRS.md), [TECH_DESIGN.md](TECH_DESIGN.md), [BENCHMARK.md](BENCHMARK.md)
**Tracking unit:** One ticket should be one reviewable PR unless explicitly marked otherwise.

---

## 1. Tracking rules

Ticket statuses:

```text
TODO → IN_PROGRESS → IN_REVIEW → VERIFIED → DONE
                       └────────→ CHANGES_REQUESTED
```

A ticket is `DONE` only when:

- All acceptance criteria (AC) pass.
- Automated tests required by the ticket pass in CI.
- Verification evidence is attached to the PR or stored under `evidence/<ticket-id>/`.
- At least one reviewer approves.
- Security/contract/operations review is completed when the ticket marks that specialty gate.
- Documentation/OpenAPI changes are included in the same PR when behavior changes.

Definition of Ready:

- Dependencies are `DONE` or explicitly mocked behind a reviewed interface.
- API/data contracts referenced by the ticket are stable enough to implement.
- No unresolved decision changes the ticket boundary.

Common verification commands will be finalized after scaffolding, but tickets must converge on:

```text
go test ./...
swift test
xcodebuild test
docker compose config
integration test suite
Docusaurus build/link check
OpenAPI contract check
secret scan
```

---

## 2. Dependency waves

| Wave | Tickets | Exit gate |
|---:|---|---|
| 0 | OCR-001–003 | Repo, local infra and CI boot reliably |
| 1 | OCR-004–008 | Shared contracts and durable storage adapters verified |
| 2 | OCR-009–013, MAC-001–004 | Native OCR plus packaged menu app/Agent lifecycle verified independently |
| 3 | OCR-014–018 | Proxy ingestion, durable jobs and Native dispatch work end-to-end |
| 4 | OCR-019–022 | Public polling, result, batch and retention flows complete |
| 5 | OCR-023–025 | Client notifications and MCP adapters complete |
| 6 | OCR-026–028 | Auth/quota, observability and security hardening complete |
| 7 | OCR-029–036 | Root docs, admin console, resilience and performance/release review pass |

Tickets in the same wave may run in parallel only when their dependency field allows it.

---

## Wave 0 — Foundation

### OCR-001 — Scaffold Go Proxy, Swift Native and Docusaurus

**Depends on:** None
**Delivers:** Buildable project skeleton and ownership boundaries.

Acceptance criteria:

- AC-1: Go module contains `cmd/proxy` and internal package roots from technical design.
- AC-2: Swift workspace contains shared contracts, menu app, LaunchAgent and test targets.
- AC-3: `docs-site` builds in Docusaurus docs-only mode at `/`, with versioning disabled and a reserved `/admin` page.
- AC-4: Root task runner/Makefile exposes deterministic build/test commands.
- AC-5: No production behavior is stubbed behind undocumented globals.

Tests:

- Clean Go compile.
- Clean Swift compile/test discovery.
- Docusaurus production build.

Verification evidence:

- CI/local command transcript showing three builds exit `0`.

Review gate: Architecture review for package boundaries.

### OCR-002 — Local PostgreSQL, Redis and MinIO environment

**Depends on:** OCR-001
**Delivers:** Docker Compose development dependencies.

Acceptance criteria:

- AC-1: Compose defines PostgreSQL, Redis and MinIO with health checks.
- AC-2: MinIO exposes an S3-compatible bucket initialized for local use.
- AC-3: Proxy config supports path-style S3 for MinIO without production-only branches.
- AC-4: Volumes, ports and credentials are development-scoped and documented.
- AC-5: `docker compose down/up` preserves expected durable data; explicit clean command removes it.

Tests:

- Compose config validation.
- Health-check smoke test.
- S3 put/get/delete smoke test.

Verification evidence:

- Container health output and successful S3 round trip.

Review gate: DevOps/config review.

### OCR-003 — CI quality baseline

**Depends on:** OCR-001, OCR-002
**Delivers:** Repeatable build/test/lint/security pipeline.

Acceptance criteria:

- AC-1: CI runs Go tests/lint, Swift tests, Docusaurus build and Markdown/link checks.
- AC-2: Integration job boots local dependencies and waits on health rather than sleeps.
- AC-3: Secret scanning covers code, configs and generated docs.
- AC-4: Failed required checks block merge.
- AC-5: CI caches do not contain credentials or uploaded OCR fixtures with sensitive data.

Tests:

- Intentionally broken fixture proves each required check fails the job, then is reverted.

Verification evidence:

- Link to a fully green pipeline and required-check configuration.

Review gate: Maintainer review.

---

## Wave 1 — Shared contracts and storage

### OCR-004 — Canonical IDs, states and error model

**Depends on:** OCR-001
**Delivers:** Typed resource/state contracts shared by REST, MCP and workers.

Acceptance criteria:

- AC-1: Typed IDs exist for batch, document, page, attempt and event.
- AC-2: Document/batch/attempt state transitions reject invalid terminal transitions.
- AC-3: RFC 9457-style problem type includes stable `code`, limits and HATEOAS `_links`.
- AC-4: Absolute links derive only from configured public base URLs.
- AC-5: Serialization fixtures cover queued, processing, completed, failed and expired resources.

Tests:

- Table-driven state-machine tests.
- Link-builder tests including malicious `Host` headers.
- JSON golden tests.

Verification evidence:

- Golden response files reviewed against SRS examples.

Review gate: API contract review.

### OCR-005 — OCR option schema, defaults and validation

**Depends on:** OCR-004
**Delivers:** One strict options model for REST/MCP and resolved effective options.

Acceptance criteria:

- AC-1: Typed schema covers recognition level/languages, automatic detection, correction, custom words, minimum height, ROI and revision.
- AC-2: Unknown fields, invalid enums/ranges/ROI and bounded custom-word violations are rejected.
- AC-3: Merge order is server profile → batch patch → item patch.
- AC-4: Effective options and capability version are immutable per accepted document.
- AC-5: Platform-controlled Vision/resource settings cannot be overridden by clients.

Tests:

- Table/fuzz tests for JSON and validation boundaries.
- Batch option merge tests.
- REST/MCP schema equivalence test.

Verification evidence:

- Generated schema and exhaustive validation matrix.

Review gate: API + Native contract review.

### OCR-006 — PostgreSQL schema and repositories

**Depends on:** OCR-004
**Delivers:** Migrations/sqlc repositories for documents, batches, pages, attempts, uploads, idempotency and outbox.

Acceptance criteria:

- AC-1: Forward migration creates all required constraints/indexes.
- AC-2: Rollback works on an empty test database where safe.
- AC-3: Tenant/client-document uniqueness and idempotency constraints are enforced by DB.
- AC-4: Work claiming supports `FOR UPDATE SKIP LOCKED` without double claim.
- AC-5: State update and outbox insert share one transaction.

Tests:

- Migration up/down integration tests.
- Concurrent claim/transition tests under race detector.
- Constraint tests for duplicates and invalid references.

Verification evidence:

- Schema dump and concurrent test output.

Review gate: Database review.

### OCR-007 — S3 storage adapter and object lifecycle

**Depends on:** OCR-002
**Delivers:** Production-compatible S3 abstraction verified against MinIO.

Acceptance criteria:

- AC-1: Streaming put/get/delete and multipart abort are implemented.
- AC-2: Object keys are opaque and tenant/document scoped.
- AC-3: Checksums and byte limits are enforced during streaming.
- AC-4: Presigned URLs are short-lived and never logged.
- AC-5: Adapter supports external production S3 and local MinIO using configuration only.

Tests:

- MinIO integration tests for all operations and interrupted multipart upload.
- Fault-injection test for partial writes.

Verification evidence:

- MinIO object listing before/after lifecycle test.

Review gate: Storage/security review.

### OCR-008 — Redis cache and invalidation

**Depends on:** OCR-004, OCR-006
**Delivers:** Bounded status/result cache; PostgreSQL/S3 remain authoritative.

Acceptance criteria:

- AC-1: Status and eligible small results use distinct TTLs.
- AC-2: Cache miss rehydrates from durable stores.
- AC-3: Transition, expiry, delete and permission changes invalidate relevant keys.
- AC-4: Oversized results are never inserted as one unbounded Redis value.
- AC-5: Redis flush/restart does not lose accepted status/result truth.

Tests:

- Hit/miss/TTL/invalidation integration tests.
- Redis flush during active result reads.

Verification evidence:

- Test trace showing durable fallback after `FLUSHDB`.

Review gate: Cache/operations review.

---

## Wave 2 — Native OCR subsystem

### OCR-009 — Native HTTP/auth/health skeleton

**Depends on:** OCR-001, OCR-004
**Delivers:** Authenticated Native service endpoints and boot identity.

Acceptance criteria:

- AC-1: `/health`, `/ocr` and `/runtime/config` routes exist with typed bodies.
- AC-2: Node ID, unique-per-process `bootId`, sequence and config version are reported.
- AC-3: Proxy-to-Native authentication rejects invalid credentials.
- AC-4: Body limits and request deadlines are enforced.
- AC-5: Health distinguishes starting, ready, busy, draining, paused and unhealthy.

Tests:

- Swift route/auth/body-limit tests.
- Boot ID and health-state tests.

Verification evidence:

- Sanitized request/response transcript.

Review gate: Native API/security review.

### OCR-010 — Apple Vision execution and serialization

**Depends on:** OCR-005, OCR-009
**Delivers:** Image preprocessing, typed Vision mapping and OCR result output.

Acceptance criteria:

- AC-1: Every effective client OCR option maps to the documented Vision property.
- AC-2: Native rejects unsupported revision/language before acquiring capacity.
- AC-3: EXIF orientation and configured downsample are applied deterministically.
- AC-4: Text, candidates, confidence and bounding boxes serialize with documented bottom-left normalized coordinates.
- AC-5: Output option limits prevent unbounded candidate/result growth.

Tests:

- Unit tests for option mapping and coordinate serialization.
- Golden OCR fixtures in `fast` and `accurate` modes.
- Unsupported option/language tests.

Verification evidence:

- Fixture outputs and benchmark harness compatibility.

Review gate: Native/Vision specialist review.

### OCR-011 — Native capability discovery

**Depends on:** OCR-005, OCR-010
**Delivers:** Runtime-derived revisions/languages/default constraints.

Acceptance criteria:

- AC-1: Native queries supported revisions and languages by level/revision from installed Vision.
- AC-2: Capability version changes when relevant runtime/default constraints change.
- AC-3: Startup capability event is signed and sequence ordered.
- AC-4: Proxy-facing representation contains no hardcoded claim that contradicts Native runtime.
- AC-5: Failure to query capabilities makes Native non-accepting with actionable reason.

Tests:

- Capability schema/golden tests.
- Simulated capability-version change tests.

Verification evidence:

- Captured capability response from target Mac.

Review gate: REST/MCP/Native contract review.

### OCR-012 — Dynamic ResourceGovernor and atomic admission

**Depends on:** OCR-009, BENCHMARK.md
**Delivers:** Shared-host-safe concurrency and resource policy.

Acceptance criteria:

- AC-1: `effectiveLimit` is `0` when locally paused; otherwise it is `min(proxyDesiredLimit,localLimit,safetyLimit,hardCeiling)`.
- AC-2: `tryAcquire` atomically checks state and increments active count.
- AC-3: Default operator limit is `1`; `0` pauses admission.
- AC-4: Reducing below active enters draining and never kills active OCR.
- AC-5: Thermal/memory policy lowers safety capacity with cooldown/hysteresis for recovery.
- AC-6: Concurrent requests never exceed effective limit.
- AC-7: Local cap can only reduce remote capacity; releasing it returns to remote policy and emits a capacity event.

Tests:

- Swift concurrency stress tests.
- Config transitions `1→0`, `0→1`, `2→1` during active jobs.
- Simulated thermal/memory-pressure tests.

Verification evidence:

- Timeline showing active/available/state transitions and max observed concurrency.

Review gate: Concurrency + operations review.

### OCR-013 — Native durable callback outbox

**Depends on:** OCR-010, OCR-012
**Delivers:** At-least-once signed result/capacity webhooks.

Acceptance criteria:

- AC-1: Completion/failure event is persisted locally before slot release.
- AC-2: Slot release precedes the capacity snapshot attached to the event.
- AC-3: HMAC covers path, timestamp, event ID and exact body digest.
- AC-4: Retry/backoff continues until Proxy returns 2xx.
- AC-5: Native restart resumes undelivered events without rerunning OCR.
- AC-6: Outbox retention is bounded and observable.

Tests:

- Proxy unavailable/restart/duplicate ACK simulations.
- Signature tamper and timestamp tests.
- Crash-recovery test around completion/release/delivery boundaries.

Verification evidence:

- Failure-injection event delivery timeline.

Review gate: Reliability + security review.

### MAC-001 — macOS app bundle and LaunchAgent lifecycle

**Depends on:** OCR-001
**Delivers:** Installable app bundle whose OCR Agent survives the menu UI lifecycle.

Acceptance criteria:

- AC-1: App bundle contains `MacOCR.app`, the Agent executable and LaunchAgent plist at the Service Management location.
- AC-2: `SMAppService` registration/status is exposed without installing a root daemon.
- AC-3: Closing/quitting the menu app does not interrupt Agent HTTP, active OCR or callback outbox processing.
- AC-4: Launch at Login can be enabled/disabled and its actual authorization state is observable.
- AC-5: Explicit Stop Service drains/stops the Agent separately from Quit Menu App.
- AC-6: App and Agent share version/protocol metadata and reject an incompatible bundle pairing.

Tests:

- App-bundle structure and plist validation tests.
- Login/start/quit/relaunch/Agent-crash lifecycle tests on macOS.
- Active OCR and pending callback survive menu UI termination.

Verification evidence:

- Process/lifecycle timeline plus signed development build artifact inventory.

Review gate: macOS packaging + Native architecture review.

### MAC-002 — Signed local XPC status/control contract

**Depends on:** OCR-009, OCR-012, MAC-001
**Delivers:** Narrow local IPC between menu app and OCR Agent.

Acceptance criteria:

- AC-1: Typed XPC API exposes snapshot, connection check, draft activation, local override and Launch-at-Login control only.
- AC-2: Agent accepts only the signed companion application identity.
- AC-3: XPC DTOs never include callback secrets, raw OCR content or arbitrary filesystem/network commands.
- AC-4: Local pause/cap is persisted atomically and composed with remote/safety/hard limits as documented.
- AC-5: Every effective capacity change increments sequence and schedules a capacity callback.
- AC-6: UI disconnect/reconnect does not change Agent state.

Tests:

- Authorized/unauthorized peer tests and DTO compatibility fixtures.
- XPC interruption/reconnection and malformed-message tests.
- Limit precedence/property tests, including persisted pause after Agent restart.

Verification evidence:

- Sanitized XPC trace and capacity snapshots for every limiting source.

Review gate: Mandatory macOS IPC + security + concurrency review.

### MAC-003 — First-run Proxy setup and two-way connection check

**Depends on:** OCR-004, OCR-009, MAC-001
**Delivers:** Domain/IP onboarding that proves the real Proxy↔Native path before activation.

Acceptance criteria:

- AC-1: First run without active config asks for Proxy domain, IP or full base URL and shows one **Check connection** action.
- AC-2: URL normalization rejects user-info/query/fragment/unexpected path; production requires HTTPS and never bypasses TLS validation.
- AC-3: Check validates DNS, TCP, TLS, authenticated Proxy identity and protocol compatibility.
- AC-4: Proxy performs a signed, expiring, one-use reverse challenge to the configured Native node address.
- AC-5: Check creates no OCR document, quota reservation, capacity acquisition or durable queue work.
- AC-6: Failures identify `url`, `dns`, `tcp`, `tls`, `authentication`, `protocol` or `reverse_connectivity` and preserve prior active config.
- AC-7: A successful draft is activated atomically; credentials remain separately provisioned in Keychain.
- AC-8: After activation the Agent auto-starts at login when approved; pending macOS approval is explicit and links to Login Items settings.
- AC-9: Temporary Proxy outage retries with bounded backoff/jitter instead of reopening onboarding.

Tests:

- URL/domain/IPv4/IPv6 and certificate SAN validation matrix.
- DNS/TCP/TLS/auth/version/reverse-path fault injection.
- Replay/expiry/signature tests for reverse challenge.
- First run, failed edit preserving old config, successful activation and offline-login retry E2E.

Verification evidence:

- Connection report fixtures for all stages and a packet/process timeline proving both directions without OCR work.

Review gate: Mandatory Native/Proxy contract + network security review.

### MAC-004 — Menu bar status and lightweight controls

**Depends on:** MAC-002, MAC-003
**Delivers:** WARP/OpenVPN-style operational UX without coupling UI and OCR lifecycle.

Acceptance criteria:

- AC-1: SwiftUI `MenuBarExtra` shows ready, busy, draining, paused, disconnected and unhealthy states accessibly.
- AC-2: Controls include run/pause, conservative local cap, Check connection and Launch at Login.
- AC-3: View shows active/available jobs, Proxy identity/connectivity, limiting reason and callback-outbox count without OCR content/secrets.
- AC-4: Settings allow changing Proxy draft only through check-then-activate; failed checks leave active config untouched.
- AC-5: Quit Menu App and Stop OCR Service are visibly distinct and consequential stop requires confirmation.
- AC-6: Open Admin, Docs and Logs use configured safe URLs/locations.
- AC-7: Removing/terminating the menu UI does not stop the Agent; relaunch reconstructs state from XPC snapshot.

Tests:

- SwiftUI state/component and accessibility tests.
- UI automation for first run, check/activate, pause/resume, local cap, login toggle and reconnect.
- Process-kill E2E during active OCR and callback retry.

Verification evidence:

- UI automation report plus synthetic screenshots for every status and first-run state.

Review gate: Native UX + accessibility + operations review.

---

## Wave 3 — Proxy ingestion and dispatch

### OCR-014 — File validators and immutable input snapshot

**Depends on:** OCR-005, OCR-007
**Delivers:** Shared validation pipeline for multipart, URL, base64 and upload handles.

Acceptance criteria:

- AC-1: MIME/magic, bytes, pixels, PDF pages, encrypted/corrupt files are validated.
- AC-2: Base64 strict-decodes and uses decoded-byte limit.
- AC-3: Multipart streams without unbounded memory use.
- AC-4: URL fetch applies SSRF checks on DNS and every redirect, timeout and byte limit.
- AC-5: Successful inputs are immutable S3 snapshots with SHA-256.
- AC-6: Validation rejection occurs before quota reservation.

Tests:

- Unit/fuzz corpus for parsers and magic mismatch.
- SSRF/DNS-rebinding/redirect tests.
- Decompression/pixel bomb and streaming memory-bound tests.

Verification evidence:

- Security validation matrix with pass/fail fixtures.

Review gate: Mandatory security review.

### OCR-015 — Upload sessions and 413 recovery flow

**Depends on:** OCR-004, OCR-007, OCR-014
**Delivers:** Direct-limit error and resumable S3 upload lifecycle.

Acceptance criteria:

- AC-1: Oversized direct upload returns `413 DIRECT_UPLOAD_TOO_LARGE`.
- AC-2: Error contains absolute `createUpload` and documentation links.
- AC-3: Upload session exposes HATEOAS self/parts/complete/abort actions.
- AC-4: Only completed, validated upload IDs can create documents.
- AC-5: Abort/expiry removes multipart state and objects idempotently.
- AC-6: Error response does not silently create an orphan upload.

Tests:

- End-to-end multipart resume/complete/abort/expire tests.
- Boundary tests immediately below/above configured direct limit.

Verification evidence:

- Full HTTP transcript following error link through successful completion.

Review gate: API + storage review.

### OCR-016 — Durable single-document creation

**Depends on:** OCR-006, OCR-014
**Delivers:** Always-async `POST /v1/documents`.

Acceptance criteria:

- AC-1: Valid submission always returns `202`, `Location`, `Retry-After`, document ID and HATEOAS links.
- AC-2: Input, effective OCR options, quota reservation, pages and outbox enqueue commit atomically.
- AC-3: Same idempotency key returns the same representation.
- AC-4: Same client document ID/input digest returns existing document; different digest returns `409`.
- AC-5: Client disconnect after commit does not cancel accepted work.

Tests:

- Handler/service/transaction integration tests.
- Concurrent duplicate submission tests.
- Disconnect/retry test.

Verification evidence:

- HTTP golden responses and database transaction trace.

Review gate: API/database review.

### OCR-017 — Fair scheduler, attempts and Native dispatch

**Depends on:** OCR-006, OCR-009, OCR-012, OCR-016, MAC-003
**Delivers:** Capacity-aware durable dispatch.

Acceptance criteria:

- AC-1: Scheduler fairly interleaves tenants/batches using claimed page jobs.
- AC-2: Fresh capacity webhook snapshot wakes dispatch; no `/health` preflight per job.
- AC-3: Each retry creates a new attempt ID while preserving page/document IDs.
- AC-4: Native `202` marks processing; `503` transitions to retry wait without failing/charging again.
- AC-5: Atomic Native admission is treated as final capacity authority.
- AC-6: Circuit breaker avoids tight loops while Native is offline/stale.

Tests:

- Scheduler fairness tests with one huge and several small batches.
- Native 202/503/network failure simulations.
- Concurrent dispatcher no-double-claim tests.

Verification evidence:

- Ordered dispatch trace and fairness metrics.

Review gate: Scheduler/concurrency review.

### OCR-018 — Native callback ingestion and completion transaction

**Depends on:** OCR-006, OCR-008, OCR-013, OCR-017
**Delivers:** Secure idempotent page/document completion.

Acceptance criteria:

- AC-1: Signature/freshness/event ID validation occurs before mutation.
- AC-2: Duplicate event returns success without duplicate state/quota/outbox effects.
- AC-3: Old sequence/boot/attempt cannot overwrite newer terminal state.
- AC-4: Result reaches S3 before document is exposed as completed.
- AC-5: Page/document/batch state and notification outbox update transactionally.
- AC-6: Capacity snapshot update wakes scheduler after commit.

Tests:

- Duplicate, reordered, late and new-boot callback tests.
- S3 failure before completion transaction.
- Crash/retry transaction tests.

Verification evidence:

- Event/state timeline for all callback race scenarios.

Review gate: Reliability/security review.

---

## Wave 4 — Public lifecycle, batch and retention

### OCR-019 — Status/result polling and HATEOAS representations

**Depends on:** OCR-008, OCR-016, OCR-018
**Delivers:** Durable `GET` behavior for every document state.

Acceptance criteria:

- AC-1: Status supports ETag/`If-None-Match`; nonterminal resources return `Retry-After`.
- AC-2: Result returns `202`, `200`, terminal problem, `410` or `404` per SRS state table.
- AC-3: Small result can be cached; large result returns page/cursor links.
- AC-4: Links are absolute, state-appropriate and tenant-authorized.
- AC-5: Redis miss transparently rehydrates from PostgreSQL/S3.

Tests:

- Golden tests for every state and link set.
- ETag/cache/large-result pagination tests.
- Cross-tenant not-found behavior tests.

Verification evidence:

- Complete response-flow transcript queued → processing → completed → expired.

Review gate: API contract review.

### OCR-020 — Batch submission, partial failure and aggregation

**Depends on:** OCR-016, OCR-017, OCR-019
**Delivers:** Multiple files per batch with required client IDs.

Acceptance criteria:

- AC-1: Every item requires a unique `clientDocumentId`.
- AC-2: Batch idempotency returns stable item-to-document mappings.
- AC-3: Invalid envelope rejects atomically; item content errors can be represented as rejected items.
- AC-4: Batch summary/status aggregates accepted/rejected/queued/processing/completed/failed counts.
- AC-5: Results preserve manifest order and support cursor pagination.
- AC-6: One item failure does not retry or fail unrelated items.

Tests:

- Mixed multipart/URL/upload-handle batches.
- Duplicate IDs, partial validation, partial runtime failure and retry tests.
- Large-batch fairness integration test.

Verification evidence:

- Batch lifecycle transcript with `completed_with_errors`.

Review gate: API/scheduler review.

### OCR-021 — Cancellation semantics

**Depends on:** OCR-017, OCR-019, OCR-020
**Delivers:** Best-effort single/batch cancellation with valid terminal behavior.

Acceptance criteria:

- AC-1: Queued work is removed from eligibility and marked cancelled transactionally.
- AC-2: Active Native work may finish physically but cannot overwrite cancelled public state.
- AC-3: Terminal resources do not advertise cancel link.
- AC-4: Batch cancellation does not corrupt already terminal item results.
- AC-5: Quota refund policy is deterministic and idempotent.

Tests:

- Cancel before dispatch, during OCR, after completion and repeated cancel.

Verification evidence:

- State-transition matrix with observed responses.

Review gate: Product/API review.

### OCR-022 — Retention, cleanup and tombstones

**Depends on:** OCR-007, OCR-008, OCR-019
**Delivers:** Independent TTL cleanup for uploads, inputs, results, events and idempotency.

Acceptance criteria:

- AC-1: Cleanup claim/recheck/delete/finalize is retry-safe.
- AC-2: Result expiry deletes S3/Redis result and leaves metadata tombstone.
- AC-3: Result endpoint returns `410 RESULT_EXPIRED` until tombstone expiry.
- AC-4: Abandoned uploads/multipart parts are removed.
- AC-5: Cleanup cannot delete a resource whose retention was extended concurrently.
- AC-6: Deleted objects/bytes/errors are observable.

Tests:

- Time-controlled expiry tests.
- Cleanup crash/retry/concurrency tests.
- S3 already-missing and Redis-down cases.

Verification evidence:

- Before/after DB/S3/Redis inventory.

Review gate: Data-retention/operations review.

---

## Wave 5 — Notifications and MCP

### OCR-023 — Client webhook registration and delivery

**Depends on:** OCR-004, OCR-018, OCR-019
**Delivers:** Optional verified completion webhook.

Acceptance criteria:

- AC-1: Clients register/verify endpoints before referencing endpoint IDs.
- AC-2: Raw per-request callback URLs are rejected.
- AC-3: Delivery is HMAC-signed, timestamped, at-least-once and deduplicable by event ID.
- AC-4: Retry history and manual replay are available.
- AC-5: Payload contains status/result links, not unbounded OCR output.
- AC-6: Polling works regardless of delivery failure.

Tests:

- Verification challenge, signature, retry, replay and endpoint disable tests.
- Webhook target SSRF validation tests.

Verification evidence:

- Signed delivery/retry transcript.

Review gate: Mandatory security review.

### OCR-024 — SSE account event stream

**Depends on:** OCR-018, OCR-019
**Delivers:** Outbound-friendly notifications for services behind NAT/firewalls.

Acceptance criteria:

- AC-1: One authenticated stream publishes authorized account/principal events.
- AC-2: Heartbeat, event ID, reconnect hint and `Last-Event-ID` resume work.
- AC-3: Slow-consumer buffers are bounded; disconnect/resume does not block publishers.
- AC-4: Expired cursor returns actionable reconciliation links.
- AC-5: SSE event is a notification; result remains retrievable via GET.

Tests:

- Reconnect/replay/heartbeat/slow consumer tests.
- Authorization isolation tests.

Verification evidence:

- SSE transcript across forced disconnect and resume.

Review gate: API/operations review.

### OCR-025 — MCP tools, tasks adapter and large results

**Depends on:** OCR-005, OCR-016, OCR-019, OCR-020
**Delivers:** MCP parity with REST document service.

Acceptance criteria:

- AC-1: Submit document/batch tools always return durable IDs immediately.
- AC-2: Get/cancel tools call the same service layer and enforce the same principal.
- AC-3: MCP Task maps to document/batch when client declares extension support.
- AC-4: Fallback get tools remain available without Tasks support.
- AC-5: Large results return resource/page/cursor handles, not full context payload.
- AC-6: MCP schema/defaults/options match REST capabilities contract.

Tests:

- MCP Inspector integration tests.
- Tasks-capable and fallback-client tests.
- Context-size/large-result tests.

Verification evidence:

- Recorded MCP tool/task flow and schema parity output.

Review gate: MCP/API review.

---

## Wave 6 — Control plane and hardening

### OCR-026 — Authentication, users, API keys and quota

**Depends on:** OCR-006, OCR-016, OCR-020
**Delivers:** Shared REST/MCP principal, scopes and atomic usage accounting.

Acceptance criteria:

- AC-1: User/admin sessions and password-change flow use Argon2id.
- AC-2: API key plaintext is shown once; DB stores prefix + HMAC hash.
- AC-3: Disabled user/revoked/expired key is blocked immediately.
- AC-4: REST/MCP share scopes and quota pool.
- AC-5: Quota reserve is atomic with accepted pages; validation rejection is free.
- AC-6: Infrastructure refund is idempotent; batch cost equals accepted page count.

Tests:

- Auth/scope/key lifecycle tests.
- Concurrent quota reservation/refund tests.
- REST/MCP shared-accounting tests.

Verification evidence:

- Quota ledger before/after success/rejection/infra failure.

Review gate: Mandatory security/database review.

### OCR-027 — Native runtime configuration control plane

**Depends on:** OCR-011, OCR-012, OCR-026, MAC-004
**Delivers:** Durable desired config and safe application to Native.

Acceptance criteria:

- AC-1: Admin can set operator limit, pause/drain and resource policy within hard ceiling.
- AC-2: Desired config is stored in PostgreSQL with monotonic version/audit record.
- AC-3: Native applies using conditional config version and emits capacity change.
- AC-4: New Native boot triggers latest config reapply.
- AC-5: Unauthorized/stale config updates are rejected.
- AC-6: HATEOAS representation exposes allowed update/resume/pause actions.

Tests:

- Config conflict/idempotency/restart/reapply tests.
- Live drain tests with active OCR.

Verification evidence:

- Config/state timeline across Native restart.

Review gate: Operations/security review.

### OCR-028 — Observability and security hardening

**Depends on:** OCR-018–027
**Delivers:** End-to-end correlation, metrics, alerts and abuse controls.

Acceptance criteria:

- AC-1: Logs correlate request, batch, document, page, attempt, Native boot/sequence and event IDs.
- AC-2: Logs redact API keys, signed URLs, callback signatures and OCR content by default.
- AC-3: Metrics cover queue age, capacity, callbacks, S3/Redis/DB, cleanup and notifications.
- AC-4: Alerts exist for stale Native capacity, oldest queue age, callback backlog, storage failure and cleanup lag.
- AC-5: Public/internal endpoints have separate rate/body/auth policies.
- AC-6: Threat model covers SSRF, replay, tenant isolation, malicious files and resource exhaustion.

Tests:

- Log-redaction tests.
- Metrics/alert rule tests.
- Abuse/load cases and automated security suite.

Verification evidence:

- Sanitized trace spanning submit → Native → result and alert test output.

Review gate: Mandatory security + operations review.

---

## Wave 7 — Documentation, admin and release gates

### OCR-029 — Docusaurus official documentation site

**Depends on:** OCR-015, OCR-019, OCR-020, OCR-023–027
**Delivers:** Complete human documentation at application root, not an OpenAPI renderer.

Acceptance criteria:

- AC-1: Docs cover quickstart, auth, all input types, uploads, single/batch, polling, webhook, SSE, MCP, options/capabilities, errors and retention.
- AC-2: Every documented response/error contains correct state-dependent HATEOAS links.
- AC-3: cURL, Go, TypeScript and Python examples follow links instead of constructing hidden routes where practical.
- AC-4: Docs-only mode builds without versioning and `/` resolves to the documentation home page.
- AC-5: OpenAPI remains published separately and cross-links to human guides.
- AC-6: Broken links, example drift and secrets fail CI.
- AC-7: Reserved `/admin`, `/v1`, `/mcp`, `/events` and machine paths cannot collide with docs slugs or static fallback.

Tests:

- Docusaurus production build/link check.
- Executable/snapshot API examples against local environment.
- OpenAPI/docs endpoint and error-code consistency test.

Verification evidence:

- Built-site artifact and example test report.

Review gate: Technical writing + API review.

### OCR-030 — Admin session, CSRF and audit foundation

**Depends on:** OCR-006, OCR-026, OCR-028
**Delivers:** Secure browser control-plane boundary before any admin feature is exposed.

Acceptance criteria:

- AC-1: Admin login/logout/session introspection use `HttpOnly`, `Secure`, `SameSite=Strict` cookies with bounded idle and absolute expiry.
- AC-2: `/v1/admin/*` requires role `admin`; API keys and non-admin sessions cannot escalate privilege.
- AC-3: Unsafe methods require a server-issued CSRF token bound to the active session.
- AC-4: Reusable audit service records actor, action, target, version, outcome, request ID and timestamp without secrets/OCR content.
- AC-5: Authenticated admin responses use `Cache-Control: no-store`; rate limits apply to login and session endpoints.
- AC-6: Session invalidation, password change and user disable take effect immediately.

Tests:

- Role/session/cookie/CSRF/expiry/session-fixation negative tests.
- Brute-force rate-limit and invalidation tests.
- Audit redaction and transaction-helper tests.

Verification evidence:

- Auth threat-case matrix plus one successful and rejected mutation audit trace.

Review gate: Mandatory security + database review.

### OCR-031 — Admin dashboard and resource read APIs

**Depends on:** OCR-019, OCR-023, OCR-027, OCR-030
**Delivers:** Bounded, read-only operational APIs.

Acceptance criteria:

- AC-1: Dashboard returns bounded dependency health, Native capacity, queue age/count and recent-failure summaries.
- AC-2: Documents/batches can be searched by server/client ID and state with cursor pagination and stable ordering.
- AC-3: Read APIs expose users, masked keys, quota policy/usage, Native desired/effective config, webhook deliveries and audit history.
- AC-4: Responses omit OCR content, signed URLs, password material and API-key secrets by default.
- AC-5: Queries have enforced page/filter/time-range bounds and avoid full-table counts on refresh.
- AC-6: Representations expose only currently valid HATEOAS actions.

Tests:

- Pagination/filter/stable-cursor tests.
- Query-bound/load tests on seeded high-cardinality data.
- Cross-tenant and sensitive-field redaction tests.

Verification evidence:

- Endpoint/query-plan matrix with representative bounded response fixtures.

Review gate: API + database + operations review.

### OCR-032 — Admin mutation APIs

**Depends on:** OCR-021, OCR-026, OCR-027, OCR-030, OCR-031
**Delivers:** Audited state-changing operations backed by existing domain services.

Acceptance criteria:

- AC-1: Admin can create/disable users, issue/revoke keys, change scopes/quota and update retention policy within configured safety bounds.
- AC-2: Admin can cancel eligible work, change Native operator limit/pause/drain/resume and replay eligible webhook delivery.
- AC-3: Mutations call existing domain services and preserve lifecycle, quota and conditional-version rules; no handler writes domain tables directly.
- AC-4: Consequential operations require expected resource/config version and return `409`/`412` when stale.
- AC-5: State change and immutable audit event commit atomically where feasible; failed outcomes are also auditable without leaking secrets.
- AC-6: Plaintext API key is returned exactly once and never appears in later reads/logs.
- AC-7: Responses expose updated state, stable problems and state-valid HATEOAS actions.

Tests:

- Mutation/state-machine/optimistic-concurrency tests.
- Transaction rollback and audit completeness tests for every action.
- Privilege, retention safety-bound and one-time-secret negative tests.

Verification evidence:

- Admin action matrix linking before/after state and audit records by request ID.

Review gate: Mandatory API + security + database + operations review.

### OCR-033 — Admin UI shell and read-only views

**Depends on:** OCR-001, OCR-029, OCR-030, OCR-031
**Delivers:** Login, navigation, dashboard and resource inspection inside the Docusaurus build.

Acceptance criteria:

- AC-1: `/` opens docs while `/admin` opens login/console without route or asset fallback collisions.
- AC-2: UI provides dashboard, document/batch lookup, user/quota, Native, webhook delivery and audit read views.
- AC-3: Pages are server-paginated and handle loading, empty, stale, partial failure, unauthorized and forbidden states.
- AC-4: Direct-route refresh and session expiry return to a safe login/recovery flow.
- AC-5: Keyboard navigation, visible focus, labelled controls and basic responsive layout pass automated accessibility checks.
- AC-6: Authenticated pages/responses use `no-store`; frontend logging/telemetry excludes OCR content and credentials.

Tests:

- Component tests for response states and pagination.
- Browser E2E for login/logout, dashboard, search, detail and direct-route refresh.
- Route-collision, automated accessibility and production bundle secret-scan tests.

Verification evidence:

- Browser E2E/accessibility reports and synthetic-data screenshots at desktop/narrow widths.

Review gate: Frontend + accessibility + security review.

### OCR-034 — Admin UI mutation workflows

**Depends on:** OCR-032, OCR-033
**Delivers:** Safe UI workflows for the mutation API.

Acceptance criteria:

- AC-1: UI renders only actions advertised by HATEOAS and confirms consequential changes with target/effect.
- AC-2: Workflows cover key issue/revoke, user disable, scope/quota/retention change, job cancel, Native pause/drain/resume and webhook replay.
- AC-3: Stale-resource conflicts preserve user context, refresh current state and never silently overwrite.
- AC-4: API key secret is shown once, never stored in browser storage and cleared on navigation/refresh.
- AC-5: Pending mutations prevent accidental double-submit and surface durable success/failure with request ID.
- AC-6: CSRF/session expiry is handled without replaying a mutation automatically.

Tests:

- Component tests for action visibility, confirmation and conflict recovery.
- Browser E2E for every mutation workflow, double-submit and expired-session cases.
- Browser-storage inspection proves no key/OCR content persistence.

Verification evidence:

- Action-by-action E2E report linked to resulting audit events.

Review gate: Frontend + mandatory security + operations review.

### OCR-035 — End-to-end resilience qualification

**Depends on:** OCR-022–034
**Delivers:** Failure-mode evidence for the complete system.

Acceptance criteria:

- AC-1: Single and mixed batch flows complete through REST and MCP.
- AC-2: Native busy/offline/restart and lost/duplicate/reordered webhook scenarios recover without corrupt state.
- AC-3: Proxy restart, Redis flush and transient S3/DB failures preserve documented guarantees.
- AC-4: Client disconnect/retry returns same IDs through idempotency.
- AC-5: Webhook/SSE failure never prevents polling result.
- AC-6: Expiry/cleanup produces `410` then eventual `404` as specified.
- AC-7: Admin actions preserve the same state, audit and authorization guarantees during component restart/failure.

Tests:

- Automated fault-injection E2E suite covering every AC.

Verification evidence:

- Scenario matrix with logs, final states and pass/fail links.

Review gate: Cross-component release review.

### OCR-036 — Performance, shared-host soak and release readiness

**Depends on:** OCR-028, OCR-035
**Delivers:** Production configuration recommendation and final go/no-go review.

Acceptance criteria:

- AC-1: Real corpus measures input validation, S3, queue, Native OCR, callback and result-read latency separately.
- AC-2: Soak test runs with representative co-resident workloads and resource-pressure events.
- AC-3: Default operator limit `1` is confirmed or changed only with measured evidence.
- AC-4: Pause/drain and resource governor prevent OCR from overrunning shared host.
- AC-5: Queue fairness, memory bounds, Redis/S3 behavior and cleanup throughput meet documented targets.
- AC-6: Admin dashboard polling uses bounded queries and does not materially reduce OCR throughput.
- AC-7: Remaining risks have owners, mitigation and acceptance decision.

Tests:

- Performance/soak suite with concurrency profiles 0/1/2 and large mixed batches.
- Regression comparison against [BENCHMARK.md](BENCHMARK.md).
- Admin dashboard refresh/load profile during an OCR soak run.

Verification evidence:

- Final benchmark report, dashboards and signed release checklist.

Review gate: Go/no-go review by API, Native, frontend, security and operations owners.

---

## 3. Cross-ticket quality gates

### Contract gate

Required for OCR-004, 005, 015, 016, 019, 020, 023, 024, 025, 027, 029, 031, 032, MAC-002 and MAC-003:

- REST JSON, OpenAPI, MCP schema and docs agree.
- Error codes and HATEOAS relations are stable and tested.
- Breaking contract changes update consumers/examples in the same wave.

### Security gate

Required for OCR-007, 009, 013, 014, 023, 026, 028, 030, 032, 033, 034 and MAC-001–004:

- Threat cases have automated negative tests.
- No raw secrets/signed URLs/OCR content in logs or evidence.
- Tenant authorization is tested at service and storage-link boundaries.

### Reliability gate

Required for OCR-006, 008, 013, 017, 018, 022, 023, 024, 035, MAC-001 and MAC-003:

- Duplicate/retry behavior is idempotent.
- Crash boundary is explicitly tested.
- Durable state is identified; cache/notification channels never become hidden truth.

### Review loop

For each ticket:

1. Author checks all AC and attaches evidence.
2. Reviewer checks scope, tests and source-of-truth consistency.
3. If changes requested, ticket returns to `CHANGES_REQUESTED` with failed AC IDs.
4. Author fixes and reruns all affected tests, not only the failed assertion.
5. Maximum three review/fix cycles before escalation to an architecture decision.

---

## 4. Progress dashboard

Update this table as work proceeds:

| Ticket | Status | Depends on | PR | Evidence | Reviewer |
|---|---|---|---|---|---|
| OCR-001 | TODO | — | — | — | — |
| OCR-002 | TODO | 001 | — | — | — |
| OCR-003 | TODO | 001,002 | — | — | — |
| OCR-004 | TODO | 001 | — | — | — |
| OCR-005 | TODO | 004 | — | — | — |
| OCR-006 | TODO | 004 | — | — | — |
| OCR-007 | TODO | 002 | — | — | — |
| OCR-008 | TODO | 004,006 | — | — | — |
| OCR-009 | TODO | 001,004 | — | — | — |
| OCR-010 | TODO | 005,009 | — | — | — |
| OCR-011 | TODO | 005,010 | — | — | — |
| OCR-012 | TODO | 009,benchmark | — | — | — |
| OCR-013 | TODO | 010,012 | — | — | — |
| MAC-001 | TODO | OCR-001 | — | — | — |
| MAC-002 | TODO | OCR-009,OCR-012,MAC-001 | — | — | — |
| MAC-003 | TODO | OCR-004,OCR-009,MAC-001 | — | — | — |
| MAC-004 | TODO | MAC-002,MAC-003 | — | — | — |
| OCR-014 | TODO | 005,007 | — | — | — |
| OCR-015 | TODO | 004,007,014 | — | — | — |
| OCR-016 | TODO | 006,014 | — | — | — |
| OCR-017 | TODO | 006,009,012,016,MAC-003 | — | — | — |
| OCR-018 | TODO | 006,008,013,017 | — | — | — |
| OCR-019 | TODO | 008,016,018 | — | — | — |
| OCR-020 | TODO | 016,017,019 | — | — | — |
| OCR-021 | TODO | 017,019,020 | — | — | — |
| OCR-022 | TODO | 007,008,019 | — | — | — |
| OCR-023 | TODO | 004,018,019 | — | — | — |
| OCR-024 | TODO | 018,019 | — | — | — |
| OCR-025 | TODO | 005,016,019,020 | — | — | — |
| OCR-026 | TODO | 006,016,020 | — | — | — |
| OCR-027 | TODO | 011,012,026,MAC-004 | — | — | — |
| OCR-028 | TODO | 018–027 | — | — | — |
| OCR-029 | TODO | 015,019,020,023–027 | — | — | — |
| OCR-030 | TODO | 006,026,028 | — | — | — |
| OCR-031 | TODO | 019,023,027,030 | — | — | — |
| OCR-032 | TODO | 021,026,027,030,031 | — | — | — |
| OCR-033 | TODO | 001,029–031 | — | — | — |
| OCR-034 | TODO | 032,033 | — | — | — |
| OCR-035 | TODO | 022–034 | — | — | — |
| OCR-036 | TODO | 028,035 | — | — | — |
