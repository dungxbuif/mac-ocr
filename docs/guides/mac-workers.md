---
title: Connect Mac workers
sidebar_position: 3
---

# Connect Mac workers

Install Mac OCR Native on each Mac you want to contribute OCR capacity. Each app connects outbound to your production server over secure WebSocket. Macs may be on different networks; no incoming Mac port, VPN, or separate OCR tunnel service is required in outbound mode.

## Add a Mac

1. Share `Mac-OCR-Native-2.0.0-universal.dmg` or `Mac-OCR-Native-2.0.0-universal.zip` manually. For the DMG, open it and drag **Mac OCR Native.app** to Applications. For the ZIP, extract it and move the app to Applications.
2. Open the app. The shared-key build already contains the production URL and connection key, so it connects automatically. Its status card shows worker state and capacity, with actions for logs, Launch at Login, quit, and an on/off switch. Connection details and settings are hidden and fixed by the release build.
3. Sign in to the administration dashboard and open **Workers** at `/admin/workers` to confirm the Mac's connection and capacity. User Management, Workers, and Routing are available only to administrator accounts.

The app derives a stable node identity from a hash of the Mac's hardware identity, with a saved random fallback if the hardware identity is unavailable. The raw hardware identifier is not sent to the server. All shared-key builds use the same production credential over HTTPS/WSS. Treat the distributed app as a private artifact; rotating this key requires updating the server and all copies of the app. If an ad-hoc build is blocked on first opening, use Finder's Open action and the macOS Privacy & Security approval for that app; do not disable Gatekeeper globally. App Store submission is not part of this distribution flow.

Optional enrollment mode is available for administrators who want separate node credentials. In that mode, create a single-use 15-minute code from **Add Mac**, paste it into the app, and enroll each installation separately. The node's credential is stored in Keychain.

## Understand status and routing

A connected node may still have zero capacity because it is paused, draining, or its CPU/memory reserve leaves no space for another job. The scheduler considers node capabilities and the weighted cost of images and PDFs before assigning work.

The administrator's Workers page always shows the automatic node identity, machine model, local OS username, hostname, macOS version, CPU architecture/core count, RAM, one effective worker status, last heartbeat, app version, resource capacity, and recent routing assignments. These details and controls require an administrator account; ordinary accounts cannot view the roster or routing. Machine metadata remains available when a node goes offline. The app generates its label from its fingerprint and has no custom node-name field or identity panel. Use **Pause** to stop new work, **Resume** to permit new work again, **Drain** before stopping a Mac, and **Revoke** to block a registered node identity. In shared-key mode, revoking one identity does not rotate the common key.

Pause, Drain, and Resume are durable server settings. They survive proxy restarts and are reapplied when a Mac reconnects; they may also be set while a registered Mac is offline.

On a network interruption or sleep, a node becomes unavailable for new work. Active attempts follow the server's lease and retry policy. The app reconnects after the network returns. A late result cannot overwrite a different active attempt.

## Network requirements

- Outbound HTTPS/WSS to the production domain, normally port 443.
- HTTPS access to the job's input and result storage endpoints.
- A supported macOS version and enough resources for the installed OCR engine.

The existing Swift package requires macOS 13 or newer. Release `2.0.0` build `7` is Universal and contains both Apple Silicon (`arm64`) and Intel (`x86_64`) binaries. Apple Silicon has physical production acceptance; the Intel slice has build and signature verification but no physical Intel acceptance run. A Mac must remain awake to contribute capacity.

## Upgrade from the old OCR tunnel

Switch the server to WebSocket worker transport only after the upgraded app and server have passed a synthetic job check. In the new mode the Mac app calls the OCR engine directly and opens no local OCR HTTP listener. Existing client REST/MCP submission behavior remains the same.

Remove the OCR mapping from the old tunnel after cutover. If `mac-service-tunnel` also serves LM Studio, keep the LLM mapping; stopping the entire container would interrupt that service.

For machine-readable connection and administration details, see [worker protocol Markdown](pathname:///worker-protocol.md) and [the agent entry point](/llms.txt).
