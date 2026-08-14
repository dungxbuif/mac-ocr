---
title: Environment configuration
sidebar_position: 1
---

# Environment configuration

`proxy/.env.example` is the canonical inventory. Production should inject secrets through the deployment platform instead of shipping a `.env` file.

## Core service

| Variable | Default/example | Purpose |
|---|---|---|
| `APP_ENV` | `development` | Enables environment-specific validation; use `production` in production |
| `HTTP_ADDR` | `:8080` | Proxy listen address |
| `PUBLIC_API_BASE_URL` | `http://localhost:8080` | Absolute API links and callback URL base |
| `PUBLIC_DOCS_BASE_URL` | `http://localhost:3000` | Public documentation links |
| `SHUTDOWN_TIMEOUT` | `10s` | Graceful HTTP shutdown window |

## PostgreSQL and Redis

| Variable | Requirement |
|---|---|
| `DATABASE_URL` | Required PostgreSQL URI; TLS must be enabled according to the production provider |
| `REDIS_URL` | Required Redis URI; use authentication and transport encryption in production |

## Object storage and uploads

| Variable | Purpose |
|---|---|
| `S3_ENDPOINT` | S3-compatible endpoint |
| `S3_REGION` | Signing region |
| `S3_BUCKET` | Private input/result bucket |
| `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | Storage credentials |
| `S3_FORCE_PATH_STYLE` | Required by many local-compatible providers; AWS commonly uses virtual-host style |
| `MAX_UPLOAD_BYTES` | Maximum bytes per public URL or app-owned presigned upload; default `104857600` (100 MiB) |

The presigned endpoint currently issues single-object `PUT` URLs and binds the declared exact content length. Production storage must enforce SigV4 signed headers. OCR submission always performs a second object-size check. Multipart uploads are not part of the public contract. Each account also has `storage_quota_bytes`, `storage_used_bytes`, and `storage_reserved_bytes`; configure the quota through Admin or `macocr-admin set-limits --user-id <id> --storage-gb <GiB>`. Zero means unlimited. Production accounts should use a nonzero quota so abandoned presigned objects cannot grow without an aggregate bound.

## Native worker and security

| Variable | Purpose |
|---|---|
| `NATIVE_BASE_URL` | Native worker origin; required in production |
| `NATIVE_AUTH_SECRET` | Shared HMAC authentication secret; required in production |
| `NOTIFICATION_ENCRYPTION_KEY` | Encrypts webhook secrets at rest; production requires a non-default value of at least 32 characters |
| `PROCESSING_LEASE` | Scheduler ownership lease, default `15m` |
| `PROCESSING_MAX_ATTEMPTS` | Maximum dispatch attempts, default `3` |

## Retention

| Variable | Default | Effect |
|---|---:|---|
| `RESULT_TTL` | `168h` | Redis OCR result lifetime and result object cleanup eligibility |
| `INPUT_TTL` | `168h` | Terminal input object retention |
| `UPLOAD_TTL` | `24h` | Unsubmitted presigned upload reservation/object retention before automatic byte refund |
| `DOCUMENT_TTL` | `2160h` | Terminal PostgreSQL document metadata retention before deletion (90 days) |
| `NOTIFICATION_TTL` | `2160h` | Delivered/expired notification audit retention (90 days) |

Choose `DOCUMENT_TTL` longer than `RESULT_TTL` when clients should receive a temporary `410 RESULT_EXPIRED` period before the document metadata itself becomes `404 NOT_FOUND`. Keep metadata/audit retention separate from file retention: increasing trace history must not implicitly preserve OCR input or result payloads. The orphan-upload janitor checks PostgreSQL references before deleting an object, so an old upload still used by a queued, processing, or retained document is not removed as an orphan.

`UPLOAD_TTL` is also the maximum reservation lifetime. Cleanup atomically wins or loses against document submission: if submission has consumed the reservation, cleanup preserves the document-owned object; otherwise it refunds reserved bytes and removes the abandoned object. Keep this value long enough for the largest supported client upload, but much shorter than document metadata retention.

## Production validation

When `APP_ENV=production`, startup rejects missing native settings and weak/default notification encryption keys. Readiness fails when PostgreSQL, Redis, or S3 is unavailable.
