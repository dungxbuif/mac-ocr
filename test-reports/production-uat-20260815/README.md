# Final local production UAT

Generated: `2026-08-14T20:12:18Z`

## Verdict

- Baseline production E2E: **PASS**
- Accuracy/page-count cases: **PASS**
- Stepped load rounds: **PASS**
- Recommended concurrency for receipt images: **4**
- Recommended concurrency for multi-page or mixed traffic: **1**
- Maximum concurrency validated without request failure: **6**

This concurrency is a local capacity result, not a universal production SLA. Re-run after changing the model, macOS, Vision revision, document mix, or memory pressure.

## Resource envelope

- Minimum system free memory: **36%**
- Maximum native RSS: **7626 MiB**
- Maximum proxy RSS: **117 MiB**
- LM Studio worker effective footprint observed by `top`: **24576 MiB**
- `pmset -g therm`: no thermal or performance warning recorded after the load run.

## Accuracy and document cases

| Case | Type | Pages | Token recall | Phrase coverage | Processing | Result |
|---|---:|---:|---:|---:|---:|---:|
| eurotext-checksum-fixture | simple | 1/1 | 100.0% | 100.0% | 3.02s | PASS |
| cord-validation-000 | receipt | 1/1 | 100.0% | 75.0% | 3.02s | PASS |
| cord-validation-001 | receipt | 1/1 | 100.0% | 50.0% | 3.02s | PASS |
| cord-validation-002 | receipt | 1/1 | 92.3% | 40.0% | 3.02s | PASS |
| cord-validation-007 | receipt | 1/1 | 93.1% | 30.0% | 3.02s | PASS |
| cord-validation-015 | receipt | 1/1 | 100.0% | 61.5% | 3.01s | PASS |
| cord-10-page | multipage-pdf | 10/10 | 97.7% | 54.9% | 15.05s | PASS |
| cord-30-page | multipage-pdf | 30/30 | 97.7% | 54.9% | 36.11s | PASS |

## Load rounds

| Workload | Worker limit | Jobs | Success | Throughput | p50 | p95 | Min free memory |
|---|---:|---:|---:|---:|---:|---:|---:|
| receipt-image | 1 | 6 | 100% | 1.967 doc/s | 3.04s | 3.04s | 41% |
| receipt-image | 2 | 8 | 100% | 2.626 doc/s | 3.04s | 3.05s | 41% |
| receipt-image | 3 | 12 | 100% | 3.923 doc/s | 3.05s | 3.06s | 41% |
| receipt-image | 4 | 16 | 100% | 5.223 doc/s | 3.05s | 3.06s | 42% |
| receipt-image | 6 | 18 | 100% | 5.860 doc/s | 3.06s | 3.07s | 42% |
| 10-page-pdf | 1 | 2 | 100% | 0.083 doc/s | 12.08s | 24.10s | 42% |
| 10-page-pdf | 2 | 4 | 100% | 0.088 doc/s | 24.12s | 45.17s | 42% |
| 10-page-pdf | 3 | 6 | 100% | 0.087 doc/s | 36.16s | 69.25s | 42% |
| 10-page-pdf | 4 | 8 | 100% | 0.086 doc/s | 48.23s | 93.36s | 43% |
| 10-page-pdf | 6 | 12 | 100% | 0.087 doc/s | 69.30s | 138.50s | 43% |

## Evidence

- `dataset-manifest.json`: primary sources, licenses, sample hashes, and ground-truth hashes.
- `raw-results/`: exact document responses returned by the API (no credentials or signed upload URLs).
- `resource-samples.csv`: CPU/RSS, memory pressure, swap, native capacity, and LLM worker footprint over time.
- `summary.json`: machine-readable complete results and per-request load records.
- `commands.md`: redacted reproduction procedure.
- `e2e-checklist.md`: final production E2E coverage and run identifier.
- `findings-and-fixes.md`: defect investigation, remediation, and post-fix evidence.

## Security handling

S3 credentials, API keys, native shared secrets, Authorization headers, and presigned URLs are intentionally excluded. Rotate the supplied S3 key before production because it was shared in chat.
