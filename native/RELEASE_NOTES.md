# Mac OCR Native — Release Notes

## 2.0.0 build 8 — Capacity tuning

**Date:** October 6, 2026

The updated worker build (2.0.0 build 9) disables adaptive CPU/memory/thermal gating by default.
Workers use the configured operator limit and weighted job units, so transient
macOS free-memory readings do not report zero capacity. Adaptive protection
remains available through `MACOCR_ADAPTIVE_CONCURRENCY=true`.

## v1.0.3 — Document Scanning

**Date:** August 18, 2026

### Highlights

- **New `POST /scan` worker endpoint.** The proxy can now dispatch a document as a *scan* (perspective-corrected, contrast-enhanced JPEG output) instead of running OCR text recognition. The native app downloads the input image, runs Vision `VNDetectRectanglesRequest` to detect the document boundary, applies CIImage perspective-correction and contrast-enhancement filters, encodes a clean JPEG, and uploads the result back to the proxy.
- **Signed scan-output upload.** The enhanced JPEG is uploaded through an HTTP `PUT` to `/webhooks/native/events/scans/{scanId}/{attemptId}/output`. The request is HMAC-SHA256 signed over `<scanId>.<attemptId>` (event ID `out_<scanId>_<attemptId>`) using the same `NATIVE_AUTH_SECRET` as OCR callbacks, and carries `Content-Type: image/jpeg`. The proxy verifies the signature, enforces a 100 MiB ceiling, and stores the object. The PUT only stores the object; the native app then POSTs a `scan.completed` callback carrying `{mediaType, sizeBytes, sha256}` to the same `/webhooks/native/events` route used by OCR, which finalizes the scan (status, result key, TTL, notification).
- **New `DocumentScanEngine`.** A dedicated Swift engine wraps Vision rectangle detection (`VNDetectRectanglesRequest` with aspect-ratio, min/max-size, and precision tuning) plus Core Image filters (perspective transform, crop, exposure/highlight/shadow/contrast adjustment, sharpening). Returns a JPEG `Data` ready for upload.
- **`NativeScanRequest`/`NativeScanResult` types.** Passed over the `/scan` wire and in the completion callback so the proxy can distinguish scan jobs from OCR jobs via the `processingMode == "scan"` discriminator.
- **Server menu-bar UI acts as scan source too.** The menu-bar panel still shows live capacity and logs only; scan jobs appear in the log as `scan.completed`/`scan.failed` entries with the same redaction rules as OCR (no secrets, presigned URLs, or file contents are logged).

### Configuration

Scan jobs reuse the existing worker settings — there is no separate scan env var. The scheduler only dispatches a job to `/scan` instead of `/ocr` when `processingMode == "scan"` was set by the proxy on submit. The same `NATIVE_PORT`, `NATIVE_LIMIT` (operator capacity ceiling), `NATIVE_AUTH_SECRET`, adaptive concurrency, and per-job unit-weight knobs apply.

### Build improvements

- `scripts/build-app.sh` now reads `MACOCR_VERSION` (default `1.0.3`) and `MACOCR_BUILD_NUMBER` (default `3`) and writes them into `CFBundleShortVersionString` / `CFBundleVersion` in `Info.plist`. Previous builds always wrote the hard-coded `1.0.0` regardless of release.
- Build this exact release:

  ```bash
  MACOCR_DEFAULT_PROXY_URL=https://ocr.dungxbuif.com \
  MACOCR_DEFAULT_MODE=production \
  MACOCR_VERSION=1.0.3 \
  MACOCR_BUILD_NUMBER=3 \
  ./scripts/build-app.sh
  ```

  The auth secret is intentionally left empty in the bundle; enter it once in the UI and it is stored in macOS Keychain.

### Requirements

- macOS 13.0 (Ventura) or later. Document scanning uses Vision `VNDetectRectanglesRequest` and Core Image filters available on macOS 13.0+.
- Apple Silicon (arm64) recommended; Intel Macs are supported but untested for scan throughput.

---

## v1.0.0 — Initial menu-bar OCR worker

- Menu-bar UI for start/stop, proxy URL, port, concurrency ceiling, connection key, live capacity, and persistent logs.
- `POST /ocr` dispatch handler with SHA-256 input verification and HMAC-SHA256 signed completion/failure callbacks.
- `/capacity` and `PUT /runtime/config` control plane.
- Adaptive concurrency from live CPU, reclaimable memory, and thermal pressure; gradual recovery, never cancels accepted work.
- macOS Keychain secret storage, redacted logging, `Launch at Login` support.
