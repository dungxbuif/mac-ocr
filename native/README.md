# Mac OCR Native Worker (Apple Silicon / macOS)

Native macOS OCR Worker service powered by Apple Vision Framework (`VNRecognizeTextRequest`) and `PDFKit`.

## Requirements

- macOS 13.0 (Ventura) or macOS 14.0+ (Sonoma / Sequoia)
- Apple Silicon (M1, M2, M3, M4) or Intel Mac
- Swift 5.9+ / Xcode 15+

## Build & Run

```bash
cd native
swift build -c release
.build/release/mac-ocr-native
```

Or run directly in development mode:

```bash
swift run
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `NATIVE_PORT` | `8787` | HTTP listening port |
| `NATIVE_LIMIT` | `2` | Max concurrent Vision OCR operations |
| `APP_ENV` | `development` | In `production`, enables strict secret validation |
| `NATIVE_AUTH_SECRET` | `change-me-in-production` | Bearer token for dispatch/config and HMAC key for callback signing; set at least 32 random bytes in production |
| `NATIVE_NODE_ID` | `ocr-native-01` | Node identifier reported in callback events |

## Proxy contract

- `POST /ocr` and `PUT /runtime/config` require `Authorization: Bearer <NATIVE_AUTH_SECRET>` and `application/json`.
- Dispatches are accepted with `202 {"attemptId":"...","status":"accepted"}`; capacity exhaustion returns `503` with `Retry-After`.
- The worker downloads the proxy-issued HTTP(S) object URL, caps it at 100 MiB, and verifies `input.sha256` before recognition.
- Completion and failure callbacks are capped at 1 MiB and signed over `nodeId.timestamp.eventId.<raw-body>` using HMAC-SHA256.
- Request JSON rejects unknown fields, unsupported media types, malformed identifiers, oversized options, and non-HTTP(S) URLs.
