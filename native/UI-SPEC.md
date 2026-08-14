# Mac OCR Native menu-bar UI contract

## Purpose

Provide a small operator control surface comparable to a VPN/WARP menu-bar app. The UI controls only the local native OCR listener; stopping it stops new work from being accepted while already accepted OCR jobs finish and deliver callbacks.

## States

- `offline`: listener is not accepting requests.
- `starting`: listener is binding the configured port.
- `online`: listener is ready; capacity shows active/limit.
- `stopping`: listener is closed and accepted work is draining.
- `error`: listener could not start or failed at runtime; the error is visible in the panel and logs.

The app always launches `offline`; it never starts the listener automatically. The primary toggle is disabled only during `starting` and `stopping`. The first Start runs a signed proxy connection test automatically and continues when it succeeds. A successful test is remembered for the exact configuration fingerprint, so reopening the unchanged app does not require another test. Any setting change invalidates verification.

## Menu-bar panel

- Monochrome system icon with a small status indicator.
- White, compact, native macOS surface; no decorative gradients.
- Editable proxy URL, local port, concurrency, and native shared key. Runtime mode and the protocol-only worker identity come from environment/build configuration and are not editable or shown in the UI. Debug mode persists `DEBUG` request/download diagnostics in addition to normal operational logs; all modes still redact secrets, signed URLs, and file contents.
- The shared key is persisted in macOS Keychain; non-secret settings use application preferences.
- A signed side-effect-free handshake validates proxy reachability, clock, node identity, and the shared HMAC key before Start is enabled.
- Service toggle, endpoint, connection result, and active/available capacity.
- Actions: View logs and Quit. Admin and documentation remain separate web surfaces.
- Launch at Login control using the macOS ServiceManagement API when running from the installed app bundle.

## Logs

- Dedicated resizable window rather than compressing full logs into the small popover.
- Live timestamped `DEBUG`, `INFO`, `WARN`, and `ERROR` entries.
- Filter by level, auto-scroll, copy visible logs, clear logs, and reveal the persistent log file.
- Keep the latest 2,000 entries in memory. Persist to `~/Library/Logs/MacOCR/native.log`, rotate at 5 MiB, and retain up to 10 archives (about 55 MiB total). Production keeps `View logs` available; files older than `MACOCR_LOG_RETENTION_DAYS` (default 30 days) are removed automatically at launch and during operation.
- Never log bearer secrets, presigned URLs, file contents, or webhook secrets.

## Accessibility and behavior

- Controls use native labels and keyboard focus.
- Status is communicated by text as well as color.
- Quitting with active work requires confirmation.
- Closing the log window does not stop the worker.
