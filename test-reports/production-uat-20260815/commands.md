# Reproduction procedure (redacted)

1. Start PostgreSQL, Redis, proxy, and the native menu-bar worker.
2. Put the real S3 values and native shared secret in ignored `proxy/.env`.
3. Create a disposable user/API key with rate >= 600 RPM and quota >= 500.
4. Export `OCR_API_KEY`, `NATIVE_AUTH_SECRET`, `OCR_API_URL`, and `NATIVE_BASE_URL`.
5. Run `scripts/prod_readiness_e2e.sh`.
6. Run `python3 scripts/final_ocr_benchmark.py --report-dir test-reports/<run>`.

No secret values or presigned URLs are stored in this folder.
