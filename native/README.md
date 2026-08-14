# Mac OCR Native Worker (Apple Silicon / macOS)

Native macOS OCR Worker service powered by Apple Vision Framework (`VNRecognizeTextRequest`) and `PDFKit`.

## Requirements

- macOS 13.0 (Ventura) or macOS 14.0+ (Sonoma / Sequoia)
- Apple Silicon (M1, M2, M3, M4) or Intel Mac
- Swift 5.9+ / Xcode 15+

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

Runtime mode is not selectable in the UI. Set `MACOCR_MODE=debug` (or `APP_ENV=debug`) when launching a development binary, or set `MACOCR_DEFAULT_MODE=debug` while building an app bundle. Debug mode records additional request/download diagnostics without logging secrets, signed URLs, or file contents. The UI labels the shared value **Connection key (HMAC)**. It must match the proxy's `NATIVE_AUTH_SECRET`; it is not an end-user API key, username, or password. The key is deliberately not embedded in the app bundle; enter it once in the UI and it is stored in Keychain.

Adaptive values supplied when `build-app.sh` runs are embedded as node defaults. Runtime environment variables with the same names take precedence, so a differently sized Mac can override capacity policy without rebuilding. The UI concurrency field remains the operator ceiling; live CPU, reclaimable memory, and thermal pressure may safely reduce the effective limit below it.

Logs are visible inside the app and persisted to `~/Library/Logs/MacOCR/native.log`, with one rotated file at 5 MiB. Secrets, presigned URLs, and file contents are not logged.

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
| `MACOCR_MODE` / `APP_ENV` | `development` | Enables debug diagnostics or strict production secret validation; not editable in the UI |
| `NATIVE_AUTH_SECRET` | `change-me-in-production` | Bearer token for dispatch/config and HMAC key for callback signing; set at least 32 random bytes in production |
| `NATIVE_NODE_ID` | `ocr-native-01` | Optional build/runtime-only protocol identifier; it is not shown or editable in the UI |
| `MACOCR_LOG_RETENTION_DAYS` | `30` | Local redacted-log retention, clamped to 1–365 days and embedded by `build-app.sh` |

## Proxy contract

- `POST /ocr` and `PUT /runtime/config` require `Authorization: Bearer <NATIVE_AUTH_SECRET>` and `application/json`.
- Dispatches are accepted with `202 {"attemptId":"...","status":"accepted"}`; capacity exhaustion returns `503` with `Retry-After`.
- `/capacity` reports the operator ceiling, adaptive effective limit, active jobs, available units, and image/PDF unit costs. The proxy uses these weights to skip jobs that do not currently fit without blocking smaller eligible work. Pressure decreases apply immediately; recovery is deliberately gradual and never cancels accepted work.
- The worker downloads the proxy-issued HTTP(S) object URL, caps it at 100 MiB, and verifies `input.sha256` before recognition.
- Completion and failure callbacks are capped at 1 MiB and signed over `nodeId.timestamp.eventId.<raw-body>` using HMAC-SHA256.
- Request JSON rejects unknown fields, unsupported media types, malformed identifiers, oversized options, and non-HTTP(S) URLs.
