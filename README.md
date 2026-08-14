# OCR Platform

OCR Platform is an API-first service for extracting text from images and documents. It offers durable asynchronous processing, stable resource URLs, batch submission, and operational controls suitable for backend integrations and automation workflows.

## Highlights

- Asynchronous single-document and batch OCR APIs
- JSON-only public HTTPS URL, Base64, and presigned large-file upload input methods
- Language priorities and configurable recognition behavior
- Durable document resources containing status and completed results
- API-key authentication, rate limits, and account quotas
- Signed webhooks, resumable SSE, and MCP task/resource notifications
- TTL-bound OCR results served from Redis
- RFC 9457 Problem Details and discoverable resource links
- Self-contained local development dependencies

## API overview

Submit a document:

```bash
curl -X POST "$OCR_API_URL/v1/documents" \
  -H "Authorization: Bearer $OCR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"input":{"url":"https://files.example.com/invoice.png"}}'
```

The API responds immediately with `202 Accepted` and a durable identifier:

```json
{
  "documentId": "doc_18f673199c0",
  "status": "queued",
  "_links": {
    "self": {
      "href": "https://ocr.example.com/v1/documents/doc_18f673199c0"
    }
  }
}
```

Poll the document URL until it reaches a terminal state, choose webhook/SSE notification during submission, or use the MCP endpoint. The same document response includes `result` until its configured TTL expires.

## Core endpoints

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/healthz` | Liveness check |
| `GET` | `/readyz` | Dependency readiness check |
| `GET` | `/v1/ocr/capabilities` | Recognition options and public limits |
| `POST` | `/v1/uploads/presign` | Create a size-bound upload URL |
| `POST` | `/v1/documents` | Submit one document |
| `GET` | `/v1/documents/{id}` | Read document status and completed result |
| `POST` | `/v1/batches` | Submit a batch |
| `GET` | `/v1/events` | Receive configured SSE notifications |
| `POST`, `GET` | `/mcp` | MCP Streamable HTTP and task/resource events for agents |

## Local development

Local object storage and the development OCR worker start together with one command:

```bash
cd local-dev
npm install --prefix s3
npm start
```

This starts:

- S3-compatible object storage at `http://localhost:9000`
- The local OCR worker at `http://localhost:8787`

In another terminal, configure PostgreSQL and Redis, copy `proxy/.env.example` to `proxy/.env`, then run the API:

```bash
cd proxy
go run ./cmd/proxy
```

The local worker downloads every submitted object through its presigned URL before producing a deterministic development result. This exercises the complete storage, dispatch, and callback path without pretending to provide production recognition accuracy.

## Documentation

Docusaurus guides are compiled from `docs/` and served at `/`. Interactive Swagger documentation is available at `/api/v1/docs`, and the runtime-generated OpenAPI 3.1 contract is available at `/api/v1/openapi.json`.

## Security

Do not expose local development services to untrusted networks. Never include API keys, customer documents, or OCR output in bug reports and fixtures. Report security issues privately to the project maintainers.

## Contributing

Focused issues and pull requests are welcome. Changes should include proportionate tests and preserve the versioned API contract. Run the following checks before submitting a change:

```bash
cd proxy
go test ./...
go vet ./...

cd ../local-dev/native
go test ./...
go vet ./...
```
