# Scan API Audit - 2026-10-08

## Scope

Audit the document-scan API after the `/v1/scans` contract was introduced, covering the public REST route, runtime OpenAPI contract, scheduler dispatch, native worker callback flow, and production reachability.

## Findings before the fix

- The production route is present: an unauthenticated `POST /v1/scans` reaches the application and returns `401`, not `404`.
- The runtime OpenAPI document omitted `/v1/scans`, `/v1/scans/{scanId}`, and their response/request schemas. This made Swagger and generated clients disagree with the live router.
- The production smoke test could not authenticate with the API key stored in the local credential note; the application returned `401 UNAUTHORIZED`. This must be re-tested with a currently active key before declaring a full production scan successful.
- Go and Swift unit suites passed before the change, but there was no OpenAPI assertion protecting the scan contract.

## Intended behavior

- Keep `POST /v1/scans` image-only and asynchronous.
- Keep `GET /v1/scans/{scanId}` authenticated and scoped to the owning API-key account.
- Advertise both routes and their actual JSON shapes in the generated OpenAPI document.
- Normalize the public API base URL before constructing the `Location` header.

## Verification plan

1. Add OpenAPI path/schema assertions for scan submission and polling.
2. Run the complete Go proxy test suite.
3. Run the complete native Swift test suite.
4. Re-check production route behavior without exposing credentials; authenticated end-to-end scan verification remains pending a valid active API key.

## Deployment intent

- Build the current proxy worktree as `homelab/macocr-proxy:scan-api-20261008` for the existing one-replica Swarm service.
- Do not rebuild or replace the native Mac worker; this release changes the proxy/OpenAPI contract only.
- Preserve the current VM100 placement, `homelab-net`, `edge-http-net`, secrets, datastore endpoints, healthcheck, and public `ocr.dungxbuif.com` route.
- Verify the service task, `/healthz`, `/readyz`, generated scan OpenAPI paths, and anonymous API-key rejection after rollout. An authenticated scan remains pending a currently active API key.

## Deployment result

- Go proxy tests: 121 passed in 32 packages; `go vet` reported no issues.
- Image `homelab/macocr-proxy:scan-api-20261008` built for `linux/amd64` and rolled out through the existing Swarm manager.
- `macocr-proxy` converged to one healthy task on `vm100-prod`; placement, secrets, healthcheck, and overlay networks were preserved.
- Public `/healthz` and `/readyz` return `200`; the generated OpenAPI now exposes both scan paths and `ScanSubmission`, `ScanReceipt`, and `ScanStatus` schemas.
- Anonymous `POST /v1/scans` still returns `401`. A full authenticated scan is still pending a valid active API key; the previously stored local key was rejected and was not printed or changed.
- Native Mac worker was not rebuilt or restarted.
