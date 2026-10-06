# Mac worker protocol v1

Transport: outbound WSS on HTTPS port 443. Initial supported topology: one scheduler/proxy replica owns live worker sessions. This file is a structured reference; see the human guide at /guides/mac-workers.

## Shared mode (selected for manual distribution)
WORKER_AUTH_MODE=shared; NATIVE_TRANSPORT=websocket.
Server and private app builds use the same NATIVE_AUTH_SECRET.
Macs auto-register on authenticated WSS connection. Node IDs are stable host-identity hashes; raw hardware identity is not sent.
Callbacks and scan outputs authenticate via Authorization: Bearer <shared secret> with X-Native-Node-Id and X-Native-Event-Id. HMAC is not required in this mode. Assigned-node/current-attempt checks remain enforced.
Revocation blocks a registered node identity; rotate the common key on all devices to replace it.

## Optional enrollment mode
POST /v1/admin/workers/enrollment
Authentication: administrator session cookie and X-CSRF-Token.
Response: {"token":"<single-use enrollment code>","expiresAt":"<RFC3339>"}.
Lifetime: 15 minutes. One code per installation.

POST /v1/workers/enroll
Body: {"token":"<code>","nodeId":"<installation UUID>","name":"<display name>"}.
Response: {"nodeId":"<node id>","secret":"<per-node credential>"}.
Store credential in macOS Keychain. Do not embed credentials in distributed app files.

## Worker connection
GET /v1/workers/connect
Upgrade: websocket.
Authorization: Bearer <per-node credential>
X-Native-Node-Id: <node id>

JSON envelope fields: type, requestId, capacity, ocr, scan, accepted, error, name, version, capabilities, action, metadata (all except type are event-dependent).
Worker sends hello with name/version/capabilities then capacity every 5 seconds.
Hello metadata fields: model, user, hostname, os, architecture, cpuCores, memoryBytes. Identity strings are bounded; raw hardware UUID/serial and secrets are not included. The display name is automatic from the fingerprint. Metadata is persisted and visible only through authenticated administrator worker listing; the app has no naming/identity panel.
Capabilities: ocr, scan.
Server sends type=ocr or type=scan, requestId=attemptId, and existing native request payload.
Worker reserves capacity, sends type=ack with requestId and accepted, then executes engine directly in process.
Server control actions: pause, resume, drain.
The selected action persists per node. Pause/drain survives proxy restart and reconnect; an offline registered node accepts a desired control action.
Input/output file bytes use job-specific HTTPS URLs rather than the socket.
Shared mode uses Bearer-authenticated HTTPS completion/output; optional enrollment mode uses signed HTTPS native callback routes with the per-node secret.

## Administration
Admin UI routes: /admin/users, /admin/workers, /admin/api-keys. The ordinary self-service route is /admin/portal. Direct loading, refresh, and browser back/forward are supported. User Management and Mac Workers & Routing render only for administrators, and the backing APIs independently enforce administrator sessions.
GET /v1/admin/workers returns nodes, routes, and authMode (shared or enrollment).
Node fields: nodeId, name, version, state, connected, paused, lastSeen, capacity, capabilities, revoked, metadata.
Route fields: nodeId, documentId, attemptId, kind, status, createdAt.
POST /v1/admin/workers/:nodeId/control
Body: {"action":"pause|resume|drain|revoke"}.
Authentication: administrator session cookie and X-CSRF-Token.
The protected roster keeps machine metadata expanded and derives one effective displayed status from revocation, connectivity, pause state, and worker state. Routing history is bounded operational history, not a permanent audit archive.

## Deployment
NATIVE_TRANSPORT=websocket selects outbound workers.
NATIVE_TRANSPORT=http retains the legacy NATIVE_BASE_URL/shared-secret path for rollback.
The WebSocket mode requires no Mac TCP listener or tunnel sidecar.
Node metadata and optional credential/enrollment state persist in PostgreSQL. The ingress must permit socket upgrades and long-lived connections.
Do not horizontally scale proxy replicas until cross-replica live-session dispatch is implemented.
Mac sleep/offline or insufficient resources removes usable capacity. Reconnect never authorizes a stale result for another active attempt or node.

## Distributed app UI

The shared production release retains a status/capacity card plus logs, Launch at Login, quit and an on/off switch. It does not expose its endpoint, credentials, connection configuration, node identity or machine metadata. Endpoint, authentication, operating mode and resource policy are build-time bundle values; stored editable preferences do not override them. Worker identity, metadata, connection state and routing are available only through administrator-authorized APIs/UI. Anonymous access returns 401; authenticated non-administrators receive 403.
