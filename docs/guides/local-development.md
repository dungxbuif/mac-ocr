---
title: Local development
sidebar_position: 2
---

# Local development

## Prerequisites

- macOS when running the production native OCR worker
- Go matching `proxy/go.mod`
- Swift toolchain and Xcode command-line tools for `native/`
- Node.js 20 or newer and npm for the local S3 service and web builds
- PostgreSQL and Redis reachable from the proxy
- `curl` and `jq` for the end-to-end script

## 1. Start PostgreSQL and Redis

Use an existing local installation or containers. Create the `macocr` database, then make `DATABASE_URL` and `REDIS_URL` in `proxy/.env` match those services.

The checked-in example expects:

```text
PostgreSQL: localhost:5432, database macocr, user dev
Redis:      localhost:6379, database 0
```

## 2. Configure the proxy

```bash
cd proxy
cp .env.example .env
```

Review every value. Local defaults are not production secrets. The proxy runs database migrations during startup.

## 3. Start object storage

```bash
cd local-dev/s3
npm install
npm start
```

The default endpoint is `http://localhost:9000` with bucket `macocr-inputs`.

## 4. Build and run the native worker

For real native OCR behavior:

```bash
cd native
swift build -c release
.build/release/mac-ocr-native
```

For a deterministic simulator instead:

```bash
cd local-dev
npm start
```

The worker must listen at the configured `NATIVE_BASE_URL` and share `NATIVE_AUTH_SECRET` with the proxy.

## 5. Build embedded web assets

```bash
cd proxy/admin-ui
npm install
npm run build

cd ../../docs-site
npm install
npm run build
```

The admin build writes to `proxy/admin/static`; the docs build writes to `proxy/docs/static`. Both directories are embedded into the Go process.

## 6. Run the API

```bash
cd proxy
go run ./cmd/proxy
```

Verify:

```bash
curl -fsS http://localhost:8080/healthz
curl -fsS http://localhost:8080/readyz
```

- Documentation: `http://localhost:8080/`
- Swagger: `http://localhost:8080/api/v1/docs`
- Admin: `http://localhost:8080/admin/`

## 7. Create local credentials

```bash
cd proxy
go run ./cmd/admin seed --email admin@example.com --password 'replace-this-password'
go run ./cmd/admin create-user --email user@example.com --rate 120 --quota 100
go run ./cmd/admin create-key --user-id 2 --name local --rate 120
```

The full API key is displayed once. Use it as `Authorization: Bearer sk_ocr_...`.

## 8. Test the stack

```bash
cd proxy
go test ./...

cd ..
scripts/prod_readiness_e2e.sh
```

The E2E flow expects the proxy, storage, PostgreSQL, Redis, and worker to already be running.
