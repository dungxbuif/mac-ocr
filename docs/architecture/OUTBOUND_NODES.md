# Outbound Mac workers

Implementation work note, 2026-10-06. Approved goal: distribute one Mac app manually, connect directly to production from LAN or Internet, remove Mac-side tunnels and local HTTP listener, observe and route multiple nodes in React admin. Keep legacy transport for rollback; do not submit to App Store or require Developer ID/notarization for this manual build. Use ad-hoc signing. User subsequently selected shared-key mode: production and manually shared bundles use one secret, Bearer authentication over TLS replaces HMAC for outbound job callbacks, and node identity derives from a hash of the host hardware identity. Enrollment remains an optional mode.

User follow-up authorizes removing the existing local OCR app and only the OCR mapping from `mac-service-tunnel`. Existing app path: `/Users/dungxbuif/Applications/Mac OCR Native.app`; tunnel also serves LM Studio, so retain its `mac-llm` section. Before local removal check active capacity, stop the idle old process, retain a private rollback copy of tunnel config, and replace the old app with the new outbound bundle. Confirm TCP 8787 closes and LLM tunnel remains running.

## Version 1 contract

- Go proxy and Swift app; React admin. One proxy replica owns sockets initially.
- `POST /v1/admin/workers/enrollment` (admin session+CSRF): creates a 15-minute single-use enrollment code. Returns `{token,expiresAt}`.
- `POST /v1/workers/enroll`: `{token,nodeId,name}` returns `{nodeId,secret}`. Optional enrollment mode persists per-node secret server-side and in Mac Keychain; shared mode auto-registers on first authenticated socket connection. Node ID derives from a stable hash of host hardware identity with a persisted random fallback; it is not copied from the app bundle.
- `GET /v1/workers/connect`: WebSocket upgrade, headers `Authorization: Bearer <secret>` and `X-Native-Node-Id`. Production URL uses WSS. No secrets in URLs.
- JSON messages: `{type,requestId?,capacity?,ocr?,scan?,accepted?,error?,name?,version?,capabilities?}`. Worker sends `hello` then `capacity` every 5 seconds. `capabilities` is an array containing `ocr` and/or `scan`. Server sends `ocr` or `scan` with the existing NativeOCRRequest/NativeScanRequest payload and requestId=attemptId. App reserves capacity before answering `{type:"ack",requestId,accepted:true|false}` and runs the existing engine in-process. Server sends `control` with `action:"pause"|"resume"|"drain"`; worker updates its operator limit and reports capacity. Shared mode completion and scan output use HTTPS Bearer authentication with node/event headers; assignment/attempt validation still applies. Optional enrollment mode retains per-node HMAC callbacks. No binary file streams over WSS.
- `GET /v1/admin/workers`: `{nodes,routes}`. Nodes: `nodeId,name,version,state,connected,paused,lastSeen,capacity,capabilities,revoked`; routes: `nodeId,documentId,attemptId,kind,status,createdAt`. History initially bounded and operational, not an audit archive.
- `POST /v1/admin/workers/:nodeId/control`: `{action:"pause"|"resume"|"drain"|"revoke"}`.
- `NATIVE_TRANSPORT=websocket|http` (legacy default `http` until cutover). WebSocket mode does not require NATIVE_BASE_URL or legacy shared secret. `WORKER_AUTH_MODE=shared|enrollment` selects authentication. Shared mode uses `NATIVE_AUTH_SECRET`, distributed only in ignored/private app artifacts; enrollment mode needs no shared secret. Node metadata and optional credentials/enrollment persist in PostgreSQL. Scheduler dispatches weighted capacity without exceeding reservations. Disconnected/revoked nodes are ineligible. Late callbacks must match current DB attempt and assigned node. Unknown ACK outcomes retain existing lease to prevent immediate duplicate execution.

## Verification plan

Backend race tests: enrollment expiry/reuse, bad credential, revoke, multi-node reservations, pause/drain, stale session, stale/wrong-node completion and capacity recovery. Native tests: protocol encoding, unique node identity, URL conversion, no listener in outbound mode; compile and Swift suite. React build and admin auth checks. Build manual share ZIP/DMG with production endpoint and only the explicitly authorized shared secret in private build artifacts. Synthetic OCR/scan E2E before production cutover; check WSS upgrade through real ingress. Document any live checks or deployment gates not exercised.

## Documentation scope

Update internal architecture/operations and human native installation/admin/deployment guides. Publish an agent-readable Markdown protocol reference alongside public docs, plus API reference endpoints. Reconcile implementation details and limitations below before completion.

## Shared-key decision

User explicitly approved a shared production key embedded in manually distributed app builds. Do not commit that key or print it in tools/logs. Configure `WORKER_AUTH_MODE=shared` and `NATIVE_AUTH_SECRET` privately. Shared-mode app auto-connects without enrollment. Node revocation blocks the registered node identity; key rotation replaces the shared key on the server and all app installations. Event/attempt/node matching remains required even though per-message HMAC is removed. Optional enrollment mode remains available for future per-node keys. Verification adds missing/bad Bearer, unauthorized registration, revoked host, and direct-secret callback tests.
