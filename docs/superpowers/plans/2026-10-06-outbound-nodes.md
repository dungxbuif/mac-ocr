# Outbound Nodes Implementation Plan

Goal: a manually shared Mac app connects outbound to production and participates in multi-node OCR routing.
Architecture: persistent node credentials and WSS sessions in Go; direct Swift engine dispatch; React worker management. Existing HTTP mode is rollback.
Spec: ../../architecture/OUTBOUND_NODES.md

Global constraints: macOS 13 minimum; no App Store submission; no shared secret in bundles; production HTTPS/WSS; single proxy replica for v1.

Review focus: revoked/wrong-node callbacks, stale reconnect sessions, capacity reservations racing heartbeats, sleep/network loss, scan upload authorization.

- [ ] Server: write failing tests for enrollment and multi-node dispatch; implement registry/storage/socket routes and scheduler adapter; run Go race suite.
- [ ] Native: write failing protocol/URL tests; implement outbound connector/enrollment and controller UI without HTTP listener; run Swift suite.
- [ ] Admin: implement worker enrollment, health/capacity and routing table, controls using existing session/CSRF wrapper; build React.
- [ ] Integration: review contracts, add end-to-end synthetic transport checks, build manually shared app ZIP/DMG and proxy; reconcile human/agent docs.
- [ ] Release: inspect production access, stage candidate and synthetic smoke; deploy only verified compatible release, retain rollback and state exact live verification limitations.

Execution: independent backend/native/admin file ownership in parallel, followed by integrated verification. User explicitly requested immediate implementation; no additional planning approval needed.
