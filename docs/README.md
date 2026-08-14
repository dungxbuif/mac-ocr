# OCR Platform Documentation

This directory documents the code currently implemented in this repository. It intentionally does not describe the underlying recognition engine or unimplemented roadmap features.

## Documentation map

- [API Reference](api/API_REFERENCE.md) — public OCR endpoints, payloads, responses, and errors.
- [Software Requirements](architecture/SRS.md) — current functional and security requirements.
- [Technical Design](architecture/TECH_DESIGN.md) — implemented components and processing flow.
- [Authentication, Rate Limits, and Quotas](architecture/API_LIMIT_DESIGN.md) — current account controls.
- [Performance Notes](architecture/BENCHMARK.md) — benchmarking policy without engine-specific claims.
- [Implementation Status](planning/IMPLEMENTATION_PLAN.md) — delivered functionality and known gaps.
- [Engineering Backlog](planning/TICKETS.md) — review findings and remaining work.
- [Manual Test Flow](testing/TEST_FLOW_GUIDE.md) — reproducible local API checks.

## Sources of truth

The public HTTP contract is defined by:

1. Route registration in `proxy/internal/rest/router.go`.
2. Request and response handlers in `proxy/internal/rest`.
3. Runtime OpenAPI generation in `proxy/internal/rest/openapi.go`, served at `/api/v1/openapi.json`.
4. Interactive Swagger UI at `/api/v1/docs` when the proxy is running.

MCP tool schemas live in `proxy/internal/rest/mcp.go` and are reused by the OpenAPI `x-mcp-tools` extension. If prose and runtime schemas disagree, update them in the same change.
