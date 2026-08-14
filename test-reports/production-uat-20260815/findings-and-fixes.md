# Findings and fixes from final UAT

## Test process

1. Started PostgreSQL, Redis, the Go proxy, and the installed native macOS worker.
2. Used the native UI as an operator: verified the signed proxy connection, then enabled the worker with the toggle.
3. Ran the production-readiness E2E against the configured real S3-compatible endpoint.
4. Downloaded checksum/ground-truth fixtures over verified TLS and recorded attribution plus SHA-256 hashes in `dataset-manifest.json`.
5. Tested simple text, five CORD receipts/invoices, a generated 10-page PDF, and a generated 30-page PDF.
6. Ran stepped load for receipt images and 10-page PDFs while sampling proxy/native CPU and RSS, system memory pressure, swap, native capacity, and the loaded LM Studio worker.
7. Stopped increasing load on request failure or unsafe memory pressure. Restored the operator limit to 3 after the run.
8. Ran Go tests, race tests, vet, Swift tests/release build, npm production builds/audits, `govulncheck`, and the production Docker build.

## Defect found under heavy PDF concurrency

Before the final run, 10-page PDFs at concurrency 2 exposed duplicate native processing. The native worker returned HTTP 202 asynchronously and immediately began synchronous Vision work. A large PDF could occupy worker threads long enough that the proxy timed out before observing the acknowledgement, even though the worker had accepted the attempt. The scheduler then requeued the same document, producing duplicate attempts and conflicting callbacks.

## Fix

- The native server now waits for the HTTP 202 response bytes to be processed before starting Vision OCR. If the acknowledgement cannot be sent, it releases the reserved slot and does not start OCR.
- A non-busy dispatch error is treated as an ambiguous outcome. The scheduler keeps the signed attempt leased for a late callback or lease expiry instead of immediately requeueing it.
- HTTP 503 capacity responses remain definite rejections and release the attempt without consuming a retry.

## Verification after the fix

- Production E2E: PASS, including real presigned PUT, PNG/PDF OCR, SSE, native callbacks, MCP, tenant isolation, auth, rate limit, and quota paths.
- Final benchmark user: 100 documents, zero failed documents, maximum database `attempt_count` of 1.
- Heavy PDF rounds: 32 ten-page PDFs across concurrency 1, 2, 3, 4, and 6; all completed successfully.
- No callback-conflict or exhausted-callback message occurred in the final run window.
- Minimum free memory was 36%; maximum native RSS was about 7.6 GiB while LM Studio's loaded worker occupied about 24 GiB.
- macOS reported no thermal or performance warning after the run.

## Legacy-row compatibility defect found during final restart

The final clean restart exposed a scheduler log loop when an older document row had nullable input metadata. `GetByID` scanned `input_content_type = NULL` into a Go string, so exhausted-attempt cleanup retried and logged the same database error every second. The repository query now applies the same `COALESCE` defaults already used by admin listing for nullable key, checksum, content type, and size fields. After restart, readiness stayed healthy, the error loop disappeared, and the exhausted legacy row was finalized (`0` exhausted processing rows remained).

## Capacity conclusion

- Receipt images: concurrency 4 is the efficient setting; concurrency 6 passed but added comparatively little throughput.
- Multi-page or mixed traffic: concurrency 1 is the efficient production setting. Concurrency 2–6 remained stable, but throughput stayed near 0.087 PDF/s while p95 latency grew from 24 seconds to 139 seconds.
- Concurrency 6 is the validated failure-free ceiling for this exact machine and workload, not the recommended operating point or a production SLA.
