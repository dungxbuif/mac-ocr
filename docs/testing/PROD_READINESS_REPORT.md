# Production Readiness Test Report — 2026-08-15

## Scope

This report covers the current local production-readiness pass for:

- Go proxy API, admin CLI, scheduler, webhook callback handler, SSE, MCP, Redis result serving.
- PostgreSQL and Redis from `~/dev-env`.
- Local S3-compatible storage from `local-dev/s3`.
- Native macOS OCR app from `native/.build/release/mac-ocr-native`, connected to the proxy through the real worker API.
- New large-file presigned upload flow:
  1. `POST /v1/uploads/presign`
  2. direct `PUT uploadUrl`
  3. `POST /v1/documents` with returned app-owned `sourceUrl`
- Embedded React/Vite administrator UI and compiled Docusaurus documentation.
- Exact-document-only public API, MCP tasks/resources, and periodic Redis/S3/PostgreSQL retention.

## Environment

- Proxy: `http://localhost:8080`
- Native app: `http://localhost:8787`
- S3 local: `http://localhost:9000`
- PostgreSQL: `localhost:5432`, database `macocr`
- Redis: `localhost:6379`
- Config limit: `MAX_UPLOAD_BYTES=104857600`

## Implemented since the previous pass

- Added authenticated presigned upload endpoint: `POST /v1/uploads/presign`.
- Added S3 adapter support for:
  - presigned `PUT`
  - `HEAD` metadata
  - app-owned `sourceUrl` generation
  - own-S3 URL detection and authenticated-user key-prefix validation
- Added document submission support for app-owned `s3://bucket/uploads/{userId}/...` URLs.
- Enforced upload file-size limit:
  - before presign issuance using declared `sizeBytes`
  - again at OCR submission using storage metadata and bounded streaming
- Preserved quota/rate behavior: document quota is reserved only after the uploaded object is present, size-checked, content-sniffed, and structurally validated.
- Updated OpenAPI, API reference, SRS, technical design, and manual test guide.
- Bound the declared upload size into the S3 SigV4 `Content-Length` signed header.
- Added orphan presigned-upload cleanup, input/result object cleanup, and terminal document-row deletion.
- Removed public document listing, deletion, and cancellation; MCP also omits task list/cancel.
- Added detailed Apple Vision response and MCP integration guides.
- Fixed explicit `false` OCR booleans so they survive proxy-to-native serialization.
- Changed PDF rendering failures from silent page omission to a terminal OCR failure.

## Edge cases covered

| Area | Case | Result |
|---|---|---|
| Auth | Missing Bearer key | `401 UNAUTHORIZED` |
| Auth | Disabled user key | `401` |
| Auth | Revoked key | `401` |
| Multi-user isolation | Independent user tries another user's `sourceUrl` | `404`, object hidden |
| Presign limit | `sizeBytes > MAX_UPLOAD_BYTES` | `413 URL_CONTENT_TOO_LARGE` |
| Presign boundary | exactly 100 MiB / 1 byte over / 1 GiB | `201` / `413` / `413` |
| S3 signed request | response contains exact signed `Content-Length` | passed; provider enforcement gate remains required |
| Upload race/missing object | Submit `sourceUrl` before upload exists | `400 INVALID_URL`, no queue |
| Submit-time limit | Uploaded object larger than limit | covered by REST unit test |
| Input contract | multipart upload to OCR endpoint | `415 UNSUPPORTED_CONTENT_TYPE` |
| Input contract | both `url` and `base64` | `400 INVALID_SOURCE` |
| Input contract | invalid Base64 | `400 INVALID_BASE64` |
| Input contract | unknown field | `400 INVALID_INPUT` |
| Input contract | trailing JSON | `400 INVALID_INPUT` |
| SSRF | private/loopback public URL | `400 SSRF_BLOCKED` |
| Batch | valid 2-item batch | `202`, independent document IDs |
| Batch | invalid item 1 | `400 INVALID_BASE64`, detail includes `batch item 1` |
| Notifications | REST SSE stream | terminal event received |
| MCP | initialize/tools/list | protocol `2025-11-25`, tools present |
| MCP | submit document via `tools/call` | completed |
| MCP | `tasks/result` | completed result returned |
| MCP | `resources/read` | document resource returned |
| MCP | GET event stream | task/resource event received |
| Response model | native PDF result | text, page count, page array, blocks, confidence range, and four-value bbox validated |
| Public routes | list/delete attempts | both `404`; no Docusaurus fallback |
| Documentation | direct Docusaurus OCR/MCP routes | both `200` with route-specific HTML |
| Admin | login/dashboard/users/documents/logout | all successful with session and CSRF |
| Retention | terminal DB row + input/result objects | deleted by worker test |
| Retention | orphan/referenced presigned upload | orphan deleted; referenced object preserved |

## Manual/full-stack evidence

Command:

```bash
rtk scripts/prod_readiness_e2e.sh
```

Result:

```text
PASS prod_readiness_e2e
```

Notable evidence from the final run:

- Main user: `user=28`
- Independent user: `user=29`
- Presigned upload:
  - oversized presign rejected: `413 URL_CONTENT_TOO_LARGE`
  - presigned `PUT` succeeded
  - independent user could not use main user's `sourceUrl`: `404`
  - missing uploaded `sourceUrl`: `400 INVALID_URL`
  - submitted presigned `sourceUrl`: `202`, completed by native app
- Native app processed real PDF/image and app-owned S3 document flows through proxy dispatch/callback. The final PDF response contained text plus structurally valid Apple Vision page/block/confidence/bbox output.
- SSE and MCP streams timed out intentionally after receiving expected events; curl timeout after event receipt is expected for streaming endpoints.
- The React admin session flow and all data endpoints, including the previously failing legacy-nullable document list, returned success.
- The Docusaurus root, OCR response guide, MCP guide, Swagger, and static admin app all served from the proxy.

## Automated gates

```bash
rtk go test ./...
TEST_DB_REDIS=1 rtk go test ./internal/repository/integration -count=1 -v
TEST_S3=1 rtk go test ./internal/repository/s3/tests -count=1 -v
rtk go vet ./...
rtk go test -race ./...
```

Result:

```text
Go test: 63 passed in 31 packages
PostgreSQL/Redis integration: passed
S3 round-trip, presigned PUT, signed header, and orphan listing: passed
Go vet: No issues found
Go race detector: 63 passed in 31 packages
```

```bash
cd local-dev/native
rtk go test ./...
rtk go vet ./...
```

Result:

```text
Go test: 3 passed in 1 packages
Go vet: No issues found
```

```bash
cd native
rtk swift build -c release
```

Result:

```text
ok (build complete)
```

`swift test` reports that the package has no test target. Native behavior is exercised through release compilation and the full-stack E2E against the running macOS app.

Compiled web artifact gate:

```bash
cd proxy
rtk make build-all
cd admin-ui && rtk npm audit --omit=dev
cd ../../docs-site && rtk npm audit --omit=dev
```

Result: React/Vite and Docusaurus production builds succeeded, embedded Go build/tests/vet succeeded, and both runtime dependency audits reported zero vulnerabilities. Docusaurus build tooling currently reports 24 development-only transitive advisories; the build output is static and those packages are not shipped in the Go runtime image.

Production config validation with non-default production secrets:

```bash
APP_ENV=production \
NATIVE_BASE_URL=http://localhost:8787 \
NATIVE_AUTH_SECRET=prod-native-secret-012345678901234567890 \
NOTIFICATION_ENCRYPTION_KEY=prod-notification-key-012345678901234567890 \
rtk go test ./internal/config/tests -run TestLoadValid -v
```

Result:

```text
Go test: 1 passed in 1 packages
```

## Runtime readiness and resources

Readiness:

```text
GET /healthz -> {"status":"ok"}
GET /readyz  -> {"status":"ready"}
GET /api/v1/openapi.json includes /v1/uploads/presign and PresignUploadResponse
```

Native capacity:

```json
{"configVersion":1,"active":0,"effectiveLimit":2,"available":2,"state":"ready","operatorLimit":2}
```

Micro-benchmark inside the e2e run:

```text
20 health requests: 0.11s real, 0.03s user, 0.04s sys
client max RSS: ~5.3 MiB
```

## Production readiness status

Status: deployment candidate. Application-level gates pass; the release is not fully production-approved until the provider and visual gates below pass in staging.

Required production settings before deploy:

- Use production PostgreSQL, Redis, and S3-compatible storage credentials.
- Set `APP_ENV=production`.
- Set non-default `NATIVE_AUTH_SECRET` with at least 32 bytes.
- Set non-default `NOTIFICATION_ENCRYPTION_KEY` with at least 32 characters.
- Set production `PUBLIC_API_BASE_URL` and `PUBLIC_DOCS_BASE_URL`.
- Set `MAX_UPLOAD_BYTES` to the product/business limit for one uploaded file.
- Ensure the native worker can reach proxy callback URL and object presigned GET URLs.
- Use HTTPS at the ingress/load balancer even if internal services bind HTTP locally.
- Run the S3 integration with `EXPECT_S3_SIGNED_CONTENT_LENGTH=1` against the actual production provider. Local `s3rver` signs and returns `Content-Length` but does not reject a mismatched body length, so it cannot prove provider enforcement.
- Complete a visual desktop/mobile click-through of the compiled admin and Docusaurus pages. The browser backend was unavailable in this test session; HTTP routes, route-specific HTML, assets, session APIs, and responsive CSS build were verified.

Known limitation:

- Local full webhook delivery to a public HTTPS receiver was not proven because no public webhook receiver/tunnel was provided. Webhook signing and blocked-target behavior are covered by automated tests, and local API validation rejects private webhook URLs as designed.
- Docusaurus has development-only transitive audit findings. They do not appear in `npm audit --omit=dev` and are not part of the embedded runtime artifact, but the lockfile should be reviewed on each Docusaurus upgrade.
