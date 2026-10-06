# Manually shared outbound Mac app (v2)

Build with `bash native/scripts/build-app.sh`. The app uses production
`https://ocr.dungxbuif.com` and outbound WSS by default, with ad-hoc signing.
No App Store submission, Developer ID, notarization, tunnel sidecar, or local
port is needed. Share the resulting `.app` in a ZIP; move it to Applications
and open it. macOS may require Open from the context menu or explicit approval
in Privacy & Security for a downloaded, unnotarized app; this system check
cannot be bypassed by the app itself.

Shared-key mode is the default for this manually distributed app. The private
release build receives `MACOCR_DEFAULT_AUTH_SECRET` from the production
configuration and embeds it in the bundle, as requested. Each Mac derives
its node ID from a SHA-256 hash of its machine platform UUID (`mac_` plus 32
hex characters), with a persisted random UUID fallback. The raw hardware
identifier is never sent to the proxy. Apps auto-connect on first launch
when their shared key is present; no enrollment is needed. Node name remains
editable while stopped. Shared callbacks/uploads send the bearer key over
HTTPS plus node/event identity headers. Copies of this release share the
same credential, and rotating the server key requires rebuilding/reconfiguring
all copies. A supplied bundle/runtime key takes precedence over cached Keychain
credentials so installing a newly keyed release rotates the Mac key. Keychain
is a fallback only when no real bundle/runtime key is supplied. Keep the
release within your intended distribution group.

For optional per-node credentials build with
`MACOCR_DEFAULT_AUTH_MODE=enrollment`. Create a short-lived single-use code
in the proxy admin dashboard, paste it in the app, set a name, and choose
**Enroll and connect**. In this mode credentials remain in Keychain outside
the bundle and callbacks use per-node HMAC signatures. Enrollment UUIDs are
persisted outside the bundle; copying just the app does not copy identity.

The Mac needs outbound HTTPS/WSS on port 443 to the proxy and HTTPS access to
input/output URLs. Vision OCR and document scan run directly in-process.
Capacity and heartbeats are sent every five seconds; jobs reserve capacity
before ACK and execute after the ACK is sent. Disconnections retry with
bounded exponential backoff and jitter; wake triggers a reconnect. Online is shown
only after a server `welcome`. Unauthorized/revoked credentials stop reconnect and display an error. Redirects cannot send credentials
to another origin, and production rejects unencrypted endpoints. Pause and
drain stop accepting jobs, resume restores the configured ceiling. Turning
the worker off drains accepted work before going offline. Keep the app open
until work and callbacks finish.

Rollback is explicit: build with `MACOCR_DEFAULT_TRANSPORT=http` or launch
with `MACOCR_TRANSPORT=http`. Configure the legacy shared key in the app;
the legacy key can be configured in the UI. Legacy HTTP retains the local
listener/tunnel architecture below. Production outbound requires HTTPS;
local development may use HTTP/WS. Supported hosts currently require macOS
13 or later, and the default build includes both Apple Silicon (`arm64`) and Intel (`x86_64`).
Set `MACOCR_ARCHS=arm64` or `MACOCR_ARCHS=x86_64` for a single architecture.

Protocol and operational reference: [OUTBOUND_NODES.md](../docs/architecture/OUTBOUND_NODES.md).

## Legacy HTTP reference

### Legacy worker details (HTTP transport only)

Native macOS OCR Worker service powered by Apple Vision Framework (`VNRecognizeTextRequest`) and `PDFKit`.

## Requirements

- macOS 13.0 (Ventura) or macOS 14.0+ (Sonoma / Sequoia)
- Apple Silicon (M1, M2, M3, M4) or Intel Mac
- Swift 5.9+ / Xcode 15+
- For document scanning (`POST /scan`): Vision `VNDetectRectanglesRequest` and Core Image filters, available on macOS 13.0+

## Menu-bar app

The native worker includes a minimal menu-bar UI for manually starting and stopping the local OCR listener. It always opens offline and never starts the worker automatically. Configure only the proxy URL, local port, concurrency ceiling, and native shared key. The first Start automatically runs the signed connection test and continues to start when the proxy confirms that the URL, clock, worker identity, and shared key match. You can also run the test separately. Local `http://localhost` proxy URLs are supported and do not require HTTPS.

The successful test is remembered for that exact connection configuration. Reopening an unchanged app does not require another test; changing a connection setting requires testing again. The shared key is stored in macOS Keychain. The UI stays focused on basic worker configuration, live capacity, and a full resizable log viewer with filtering, copy, clear, and persistent file access. Admin and documentation links are intentionally not exposed in the native app.

Build an installable app bundle:

```bash
./scripts/build-app.sh
open "dist/Mac OCR Native.app"
```

Install for the current user and launch it:

```bash
./scripts/install-app.sh
```

The installed path is `~/Applications/Mac OCR Native.app`. Use the app's **Launch at Login** switch after installation if the control panel should appear with the macOS session; this still leaves the worker offline until manually started.

Build-time defaults can be overridden without editing source:

```bash
MACOCR_DEFAULT_PROXY_URL=https://ocr.dungxbuif.com \
MACOCR_DEFAULT_MODE=production \
MACOCR_DEFAULT_PORT=8787 \
MACOCR_DEFAULT_LIMIT=6 \
MACOCR_ADAPTIVE_CONCURRENCY=true \
MACOCR_RESERVE_CORES=2 \
MACOCR_RESERVE_MEMORY_GB=10 \
MACOCR_MEMORY_PER_UNIT_GB=2 \
MACOCR_IMAGE_JOB_UNITS=1 \
MACOCR_PDF_JOB_UNITS=3 \
MACOCR_CAPACITY_RECOVERY_SAMPLES=5 \
MACOCR_DEFAULT_NODE_ID=ocr-mac-01 \
./scripts/build-app.sh
```

Runtime mode is not selectable in the UI; the control panel shows the resolved value (for example `Mode: production`). For a development binary, set `MACOCR_MODE=debug` or `APP_ENV=debug`. For a built app bundle, set `MACOCR_DEFAULT_MODE=debug` at build time. The bundle value is the deployment authority and wins over an ambient `APP_ENV`, so a production bundle never silently flips to development on a host that exports `APP_ENV`; use `MACOCR_MODE` (the native-specific knob) to override a bundle's mode at runtime. Debug mode records additional request/download diagnostics without logging secrets, signed URLs, or file contents. The UI labels the shared value **Connection key (HMAC)**. It must match the proxy's `NATIVE_AUTH_SECRET`; it is not an end-user API key, username, or password. By default, enter it once in the UI and it is stored in Keychain. `MACOCR_DEFAULT_AUTH_SECRET` may seed the bundle key for explicitly requested manual shared-key distribution.

Adaptive values supplied when `build-app.sh` runs are embedded as node defaults. Runtime environment variables with the same names take precedence, so a differently sized Mac can override capacity policy without rebuilding. The UI concurrency field remains the operator ceiling; live CPU, reclaimable memory, and thermal pressure may safely reduce the effective limit below it.

Logs are visible inside the app and persisted to `~/Library/Logs/MacOCR/native.log`, with one rotated file at 5 MiB. Secrets, presigned URLs, and file contents are not logged.

### Resetting saved settings

The menu-bar app persists the proxy URL, port, and concurrency ceiling in `~/Library/Preferences/com.macocr.native.plist` (UserDefaults) and the connection key in Keychain (service `com.macocr.native`, account `native-auth-secret`). Reinstalling or rebuilding the app does **not** clear these: a previous dev build that saved `http://localhost:8080` keeps showing that URL after a production bundle is installed, because saved values override the bundle defaults on launch. To make a freshly built bundle reseed every field from its build-time defaults, clear the saved state before relaunching:

```bash
defaults delete com.macocr.native
security delete-generic-password -a native-auth-secret -s com.macocr.native
```

After relaunch, the bundle seeds `Proxy URL`, `Mode`, the connection key, and capacity defaults from `Info.plist`.

## Build & Run from the workspace

```bash
cd native
swift build -c release
.build/release/mac-ocr-native
```

The executable opens the same menu-bar UI. Or run directly in development mode:

```bash
swift run
```

## Environment Variables

`MACOCR_TRANSPORT` or build-time `MACOCR_DEFAULT_TRANSPORT` selects `websocket` (default) or legacy `http`. Port/shared-key/node-ID variables below only apply to legacy HTTP. `MACOCR_DEFAULT_AUTH_MODE=shared|enrollment` controls outbound authentication. Shared mode uses a hashed machine ID and the private build key; enrollment mode uses installation credentials.

| Variable | Default | Description |
|---|---|---|
| `NATIVE_PORT` | `8787` | HTTP listening port |
| `NATIVE_LIMIT` / `MACOCR_DEFAULT_LIMIT` | `6` | Operator ceiling in capacity units; `0` pauses new work |
| `MACOCR_ADAPTIVE_CONCURRENCY` | `true` | Let native reduce effective capacity from live memory and thermal pressure |
| `MACOCR_RESERVE_CORES` | `2` | CPU cores kept available for macOS and other applications |
| `MACOCR_RESERVE_MEMORY_GB` | `10` | Reclaimable-memory safety reserve before accepting more OCR work |
| `MACOCR_MEMORY_PER_UNIT_GB` | `2` | Memory headroom represented by one capacity unit |
| `MACOCR_IMAGE_JOB_UNITS` | `1` | Capacity units charged to an image job |
| `MACOCR_PDF_JOB_UNITS` | `3` | Capacity units charged to a PDF job |
| `MACOCR_CAPACITY_RECOVERY_SAMPLES` | `5` | Consecutive healthy samples required before capacity increases |
| `MACOCR_MODE` / `APP_ENV` | `development` | Runtime mode (`debug`/`development`/`production`). `MACOCR_MODE` always wins and overrides the bundle default. `APP_ENV` is honored only for the development binary; a built bundle's `MacOCRDefaultMode` takes precedence over an ambient `APP_ENV` so a production bundle cannot silently flip to development |
| `NATIVE_AUTH_SECRET` | `change-me-in-production` | Bearer token for dispatch/config and HMAC key for callback signing; set at least 32 random bytes in production |
| `NATIVE_NODE_ID` | `ocr-native-01` | Optional build/runtime-only protocol identifier; it is not shown or editable in the UI |
| `MACOCR_LOG_RETENTION_DAYS` | `30` | Local redacted-log retention, clamped to 1–365 days and embedded by `build-app.sh` |

## Proxy contract

- `POST /ocr`, `POST /scan`, and `PUT /runtime/config` require `Authorization: Bearer <NATIVE_AUTH_SECRET>` and `application/json`.
- `POST /scan` dispatch is image-only: the worker runs `VNDetectRectanglesRequest` perspective correction and CIImage contrast enhancement, encodes a clean JPEG, and uploads it back to the proxy-issued PUT URL, then sends a signed `scan.completed` or `scan.failed` JSON callback. Uploads use the same node/timestamp/event/body HMAC format as callbacks and are capped at 100 MiB; the secret itself is never sent on that PUT.
- Dispatches are accepted with `202 {"attemptId":"...","status":"accepted"}`; capacity exhaustion returns `503` with `Retry-After`.
- `/capacity` reports the operator ceiling, adaptive effective limit, active jobs, available units, and image/PDF unit costs. The proxy uses these weights to skip jobs that do not currently fit without blocking smaller eligible work. Pressure decreases apply immediately; recovery is deliberately gradual and never cancels accepted work.
- The worker downloads the proxy-issued HTTP(S) object URL, caps it at 100 MiB, and verifies `input.sha256` before recognition.
- Completion and failure (OCR) callbacks are capped at 1 MiB and signed over `nodeId.timestamp.eventId.<raw-body>` using HMAC-SHA256.
- Request JSON rejects unknown fields, unsupported media types, malformed identifiers, oversized options, and non-HTTP(S) URLs.
