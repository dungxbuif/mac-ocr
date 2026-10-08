# Mac OCR worker

The manually distributed production app retains its menu-bar status card with
worker state, capacity, logs, Launch at Login, quit, and an on/off switch. It
connects automatically when opened. OFF cancels an ongoing connection attempt
and stops new work; accepted work drains before disconnecting. Endpoint,
Connection/configuration controls, credentials, identity and machine metadata do
not appear in the app. Monitor nodes and routing in the authenticated admin
dashboard.

## Build and share

Run `bash native/scripts/build-app.sh` from the repository root. The default
bundle is universal Apple Silicon/Intel, minimum macOS 13, production mode,
outbound WebSocket, and `https://ocr.dungxbuif.com`. It is ad-hoc signed for manual
sharing; there is no App Store submission or Developer ID requirement. macOS can
still require explicit approval to open downloaded, unnotarized apps.

The finalized private release is version `2.0.0` build `7`. The build directory
contains these operator-facing artifacts:

- `native/dist/Mac OCR Native.app`
- `native/dist/Mac-OCR-Native-2.0.0-universal.dmg` for normal manual installation
- `native/dist/Mac-OCR-Native-2.0.0-universal.zip` for direct sharing

Open the DMG and drag the app to Applications. On first launch, use Finder's
**Open** action if Gatekeeper asks for approval. The same DMG supports Apple
Silicon and Intel Macs running macOS 13 or newer. The Intel slice is build and
signature verified; physical release acceptance was performed on Apple Silicon.

The private release build supplies `MACOCR_DEFAULT_AUTH_SECRET` from production.
This embeds the shared server credential in the app, as requested. Keep the
release within the intended distribution group. Rotate the key by updating
production and distributing a newly built app. Never commit the key or print the
built plist. Shared production reads endpoint, transport, auth mode, concurrency,
and key from the bundle, ignoring saved preferences, cached Keychain values,
and ambient operational environment overrides. Capacity policy also uses bundle
settings. This prevents an older local setup from redirecting a released app.

Supported build variables include `MACOCR_DEFAULT_PROXY_URL`,
`MACOCR_DEFAULT_AUTH_SECRET`, `MACOCR_DEFAULT_LIMIT`, `MACOCR_DEFAULT_MODE`,
`MACOCR_DEFAULT_TRANSPORT`, `MACOCR_DEFAULT_AUTH_MODE`, `MACOCR_ARCHS`,
`MACOCR_VERSION`, and `MACOCR_BUILD_NUMBER`. `MACOCR_ARCHS` defaults to
`arm64 x86_64`; either architecture can be selected individually.

Adaptive capacity defaults are embedded using `MACOCR_ADAPTIVE_CONCURRENCY`,
`MACOCR_RESERVE_CORES`, `MACOCR_RESERVE_MEMORY_GB`, `MACOCR_MEMORY_PER_UNIT_GB`,
`MACOCR_IMAGE_JOB_UNITS`, `MACOCR_PDF_JOB_UNITS`, and
`MACOCR_CAPACITY_RECOVERY_SAMPLES`. There are no capacity settings in the app.
The release default for `MACOCR_ADAPTIVE_CONCURRENCY` is `false`: the worker uses
the operator limit and weighted job units without blocking on transient macOS
free-memory readings. Set it to `true` only when a host needs adaptive CPU,
memory, and thermal-pressure protection.

## Connection and identity

The Mac needs outbound HTTPS/WSS to the proxy and HTTPS access to input/output
URLs. There is no local listener or tunnel sidecar in outbound mode. Vision OCR
and scan run in-process. The app sends hello and capacity every five seconds,
reserves capacity before ACK, and starts work after sending ACK. Reconnect uses
bounded exponential backoff/jitter; wake triggers reconnection. Pause/drain and
resume are managed through the admin dashboard. Authorization errors stop retry
until the app is restarted with valid production configuration.

Shared-mode node identity is a SHA-256 hash of the machine platform UUID,
formatted as `mac_` followed by 32 hex characters. The raw hardware UUID is never
transmitted. A persisted random UUID is the fallback. Display names are generated
from the identity suffix; the app offers no name or identity editor.

Hello includes admin-only metadata: model, macOS login user, hostname, macOS
version, architecture (including Rosetta), active CPU cores, and physical memory
bytes. Text values are limited to 256 UTF-8 bytes. Serial numbers, raw hardware
UUIDs, and secrets are absent from metadata. Shared callbacks/uploads use HTTPS
bearer authentication plus node/event identity headers; destinations and
credential-bearing redirects must stay on the proxy origin.

Diagnostics persist to `~/Library/Logs/MacOCR/native.log` and are available from
the card's log viewer. Secrets, file contents, signed URLs, endpoints and worker
identity are redacted. Capacity remains an operational build setting; the card
shows its current value without allowing edits.

## Legacy rollback and development

The HTTP transport and per-node enrollment protocol remain for compatibility;
they do not add configuration controls to the distributed app. Explicitly build
a separate rollback/development bundle using `MACOCR_DEFAULT_TRANSPORT=http`
and appropriate legacy defaults. Legacy HTTP uses a local listener, shared key,
and HMAC callbacks. Development binaries may use existing runtime variables;
shared production bundles ignore runtime operational overrides.

Run `swift test --package-path native` to verify the worker, including its local
RFC6455 fixture. Tests cover connection headers, hello metadata, rejected
requests, drain/resume, resource reservations, build authority, and OFF during
connection. The fixture uses system Python without third-party dependencies.

Agent-readable protocol reference:
[OUTBOUND_NODES.md](../docs/architecture/OUTBOUND_NODES.md).
