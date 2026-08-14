---
title: Production deployment
sidebar_position: 1
---

# Production deployment

This is a release checklist, not a provider-specific infrastructure template. The OCR worker requires macOS; deploy the Go proxy and its stateful dependencies according to your platform, then provide routable private connectivity to the native worker.

## Required topology

```text
Internet clients
    │ HTTPS
    ▼
Load balancer / ingress
    │
    ▼
Go proxy ── PostgreSQL
    ├────── Redis
    ├────── private S3 bucket
    └────── macOS native worker
```

Run one scheduler-capable proxy replica first. Multi-replica queue claiming is supported by PostgreSQL row locking, but rollout and capacity should still be load-tested with the intended database and worker concurrency.

## Build artifacts

### Local / Bare-metal build

```bash
cd proxy/admin-ui
npm ci
npm run build

cd ../../docs-site
npm ci
npm run build

cd ../proxy
go test ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o bin/macocr-proxy ./cmd/proxy
go build -trimpath -buildvcs=false -o bin/macocr-admin ./cmd/admin

cd ../native
swift test
swift build -c release
```

Build web assets before the Go binary because both are embedded at compile time.

For the menu-bar app bundle, pass the production connection defaults to `native/scripts/build-app.sh` (for example `MACOCR_DEFAULT_PROXY_URL=https://ocr.dungxbuif.com`, `MACOCR_DEFAULT_MODE=production`, `MACOCR_DEFAULT_AUTH_SECRET=<shared NATIVE_AUTH_SECRET>`). A previous dev build that saved `http://localhost:8080` in UserDefaults or an old connection key in Keychain keeps those values after installing the production bundle, because saved settings override the bundle defaults on launch. Clear the saved state before relaunching so the production bundle reseed from `Info.plist`:

```bash
defaults delete com.macocr.native
security delete-generic-password -a native-auth-secret -s com.macocr.native
```

See `native/README.md` for the full build-time default inventory and the mode precedence rules.

### Docker Container build

Alternatively, build the containerized proxy image using the multi-stage `Dockerfile` from the repository root:

```bash
docker build \
  --build-arg PUBLIC_API_BASE_URL="https://ocr.dungxbuif.com" \
  --build-arg PUBLIC_DOCS_BASE_URL="https://ocr.dungxbuif.com" \
  --build-arg APP_ENV="production" \
  --build-arg VERSION="1.0.0" \
  -t macocr-proxy:latest .
```


## Infrastructure prerequisites

- PostgreSQL with automated backups, point-in-time recovery, TLS, and capacity for queue locks and document retention.
- Redis with authentication, TLS, memory policy compatible with result TTLs, and monitoring for eviction pressure.
- A private S3 bucket with encryption at rest, blocked public access, lifecycle cleanup, CORS restricted to intended upload origins, and enforced SigV4 signed headers.
- A macOS worker host with the release binary supervised by a service manager and reachable only from the proxy network.
- HTTPS ingress with request timeouts that permit SSE and MCP streams.
- Centralized logs and alerts for readiness failures, queue growth, callback signature errors, notification retries, and retention failures.

Use `request_id`, `user_id`, `api_key_id`, `documentID`, `attemptID`, and `eventID` as correlation fields across HTTP, scheduler, native callback, notification, and retention logs. Never ingest Authorization headers, API keys, HMAC secrets, presigned URL query strings, webhook secrets, OCR input bytes, or OCR result payloads. Keep centralized audit-log retention independent from `INPUT_TTL` and `RESULT_TTL`; extending trace history must not extend storage of customer documents.

## Secrets

Generate independent high-entropy values for database, Redis, S3, native authentication, notification encryption, and administrator passwords. Do not reuse the local examples. Rotate credentials through the provider secret manager and restart affected processes deliberately.

## Pre-deploy gate

1. Build admin, docs, Go, and Swift artifacts from a clean checkout using lockfiles.
2. Run Go unit tests and repository integrations against production-equivalent PostgreSQL, Redis, and S3. Set `TEST_DB_REDIS=1`, `TEST_S3=1`, and `EXPECT_S3_SIGNED_CONTENT_LENGTH=1` for the provider enforcement gate.
3. Run `scripts/prod_readiness_e2e.sh` with the real native worker.
4. Verify public `GET /v1/documents` and `DELETE /v1/documents/{id}` return `404`.
5. Verify OpenAPI advertises `apiKeyAuth`, only document submit/read-by-ID, and three MCP tools.
6. Verify presigned upload at the size boundary succeeds, `max+1` fails, a mismatched signed content length is rejected by the production S3 provider, and submit-time metadata rejects oversized objects without consuming document quota.
7. Set a nonzero per-account `storage_quota_bytes`; verify concurrent presign calls cannot exceed it, successful submit converts reserved bytes to used bytes, and `UPLOAD_TTL` cleanup refunds abandoned reservations.
8. Verify rate limits at both API-key and account levels and document quota reservation under concurrent requests.
9. Verify the native worker reports adaptive weighted capacity, immediately reduces capacity under simulated pressure, recovers only after the configured healthy-sample window, and never cancels accepted work. Confirm the proxy skips PDFs when fewer than `pdfJobUnits` are free while still dispatching eligible images.
10. Verify webhook signatures, SSE resume, MCP resource/task reads, and account isolation.
11. Verify `RESULT_TTL`, `INPUT_TTL`, `UPLOAD_TTL`, `DOCUMENT_TTL`, and `NOTIFICATION_TTL` in a shortened staging run.

## Rollout

- Apply backward-compatible database migrations before increasing traffic.
- Start the native worker and confirm its health before starting proxy schedulers.
- Deploy the proxy, wait for `/readyz`, then shift traffic gradually.
- Watch error rate, Redis memory, database connections, queued/processing counts, worker latency, and callback failures.
- Keep the prior proxy and native binaries available for rollback. Database migrations in the current startup path must remain backward-compatible with the prior binary for a safe application rollback.

## Backup and recovery test

At least once per release cycle, restore PostgreSQL to an isolated environment, verify account/key metadata, and confirm queued/processing rows can be reconciled. Object storage and Redis result cache have different retention roles: PostgreSQL backup is durable metadata recovery; Redis result payloads are intentionally ephemeral.
