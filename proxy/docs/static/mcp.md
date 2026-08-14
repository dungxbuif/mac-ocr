# MCP for agents

OCR Platform exposes an authenticated MCP Streamable HTTP endpoint at `/mcp`, using protocol revision `2025-11-25`.

```text
POST /mcp
GET /mcp
Authorization: Bearer sk_ocr_...
MCP-Protocol-Version: 2025-11-25
```

Tools:

- `submit_ocr_document`
- `submit_ocr_batch`
- `get_ocr_document`
- `cancel_ocr_document`

Resources use `ocr://documents/{documentId}`. A single submitted document maps directly to an MCP task. Agents can call `tasks/get`, `tasks/list`, `tasks/result`, and `tasks/cancel` rather than polling REST-specific representations.

Keep `GET /mcp` open to receive `notifications/tasks/status` and `notifications/resources/updated`. These notifications are hints; task and resource reads remain authoritative, and reconnecting agents can resume from `Last-Event-ID`.

Batch submission returns multiple independent document task IDs and does not create a public batch task.

MCP uses the same input validation as REST: each tool item contains exactly one `input.url` or `input.base64`; Base64 is limited to 25 MiB decoded, URLs must be public HTTPS, and format detection uses the bytes rather than a caller-supplied type. MCP submissions automatically select the MCP event stream, so they do not accept a REST webhook/SSE notification object.

The MCP request envelope is capped at 128 MiB. Tool schemas and the `x-mcp-tools` OpenAPI extension are generated from the runtime Go definitions, so the agent contract and implementation share one source.
