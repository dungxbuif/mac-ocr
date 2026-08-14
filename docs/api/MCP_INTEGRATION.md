---
title: MCP integration
sidebar_position: 3
---

# MCP integration

The authenticated `/mcp` endpoint exposes the same validation, quota, native OCR worker, response model, and result TTL as REST. MCP adds JSON-RPC envelopes, durable task IDs, exact document resources, and task/resource notifications; it does not create a second OCR result format.

## Connection contract

- URL: `POST /mcp` for JSON-RPC and `GET /mcp` for the SSE event stream.
- Authentication: `Authorization: Bearer sk_ocr_...`.
- Content type: `application/json` for POST.
- Protocol header: `MCP-Protocol-Version: 2025-11-25` when supplied.
- Maximum POST envelope: 128 MiB.
- Origin checks apply when the client sends an `Origin` header.

Initialize before discovery or tool calls:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-11-25",
    "capabilities": {},
    "clientInfo": {"name": "my-agent", "version": "1.0.0"}
  }
}
```

The server advertises tools, subscribable resources, and task support for tool calls.

## Available tools

| Tool | Result |
|---|---|
| `submit_ocr_document` | Submits one URL/Base64/app-owned S3 input. With MCP task metadata, returns a durable task; otherwise returns a normal tool result containing the queued document. |
| `submit_ocr_batch` | Submits 1–100 independent documents and returns their indexes, document IDs, and task metadata. Task wrapping for the batch tool itself is forbidden. |
| `get_ocr_document` | Reads one known document by `documentId`, including the completed OCR result while retained. |

Task listing and cancellation are intentionally not exposed. The client must retain every returned `documentId`/`taskId`.

## Submit and poll as a task

Tool call request:

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "submit_ocr_document",
    "arguments": {
      "input": {"url": "s3://macocr-inputs/uploads/123/example.pdf"},
      "options": {"recognitionLevel": "accurate", "languages": ["vi-VN", "en-US"]}
    },
    "task": {"ttl": 60000}
  }
}
```

Queued task response:

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "task": {
      "taskId": "doc_18f673199c0",
      "status": "working",
      "statusMessage": "",
      "createdAt": "2026-08-15T08:30:00Z",
      "lastUpdatedAt": "2026-08-15T08:30:00Z",
      "ttl": null,
      "pollInterval": 3000
    },
    "_meta": {
      "io.modelcontextprotocol/related-task": {"taskId": "doc_18f673199c0"}
    }
  }
}
```

The requested client TTL is not the server's OCR retention TTL; `ttl` is returned as `null`. Use `pollInterval` as milliseconds and rely on `resultExpiresAt` for result retention.

Poll metadata with `tasks/get`:

```json
{"jsonrpc":"2.0","id":3,"method":"tasks/get","params":{"taskId":"doc_18f673199c0"}}
```

MCP task states map from document states as follows:

| Document state | MCP task state |
|---|---|
| `queued`, `processing` | `working` |
| `completed` | `completed` |
| `failed` | `failed` |
| internal legacy `cancelled` | `cancelled` |

After the task is `completed`, call `tasks/result`:

```json
{"jsonrpc":"2.0","id":4,"method":"tasks/result","params":{"taskId":"doc_18f673199c0"}}
```

Calling `tasks/result` before completion returns JSON-RPC error `-32602`.

## Tool-result envelope

`get_ocr_document`, non-task submission responses, batch submission, and `tasks/result` use the MCP tool-result shape:

```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\"documentId\":\"doc_18f673199c0\",\"status\":\"completed\",\"result\":{...}}"
      }
    ],
    "structuredContent": {
      "documentId": "doc_18f673199c0",
      "status": "completed",
      "createdAt": "2026-08-15T08:30:00Z",
      "updatedAt": "2026-08-15T08:30:04Z",
      "result": {
        "text": "Invoice 1042",
        "pageCount": 1,
        "pages": [
          {
            "pageNumber": 1,
            "text": "Invoice 1042",
            "blocks": [
              {"text": "Invoice 1042", "confidence": 0.9874, "bbox": [0.091, 0.862, 0.311, 0.048]}
            ]
          }
        ]
      },
      "resultExpiresAt": "2026-08-22T08:30:04Z"
    },
    "isError": false,
    "_meta": {
      "io.modelcontextprotocol/related-task": {"taskId": "doc_18f673199c0"}
    }
  }
}
```

`content[0].text` is the JSON-string serialization of `structuredContent`. Prefer `structuredContent` when the MCP client exposes it; parse the text item only as a compatibility fallback. The nested `result` follows the complete [OCR response model](OCR_RESPONSE.md), including Apple Vision confidence and normalized lower-left coordinates.

The MCP document view intentionally contains only `documentId`, `status`, timestamps, optional `result`/`resultExpiresAt`, and optional `errorDetail`. REST-only input metadata and HATEOAS `links` are not included.

## Exact document resource

Discover the template through `resources/templates/list`. Read a known resource with:

```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "method": "resources/read",
  "params": {"uri": "ocr://documents/doc_18f673199c0"}
}
```

Response:

```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "result": {
    "contents": [
      {
        "uri": "ocr://documents/doc_18f673199c0",
        "mimeType": "application/json",
        "text": "{\"documentId\":\"doc_18f673199c0\",\"status\":\"completed\",\"result\":{...}}"
      }
    ]
  }
}
```

Resource content is JSON encoded inside a text field. Parse it once to obtain the same MCP document view used by `get_ocr_document`.

## Event stream

Keep `GET /mcp` open with the same API key. The server sends:

- `notifications/tasks/status` with `taskId` and MCP task `status`.
- `notifications/resources/updated` with `uri: ocr://documents/{documentId}`.
- SSE `id` on the task-status event and `retry: 3000` connection guidance.
- Comment heartbeats every 15 seconds while idle.

Reconnect with `Last-Event-ID` to resume durable terminal events. Delivery is at least once, so deduplicate by SSE event ID and always re-read the exact task/resource before acting.

## JSON-RPC errors and retention

| Code | Meaning |
|---|---|
| `-32600` | Invalid JSON-RPC request or malformed envelope. |
| `-32601` | Method does not exist, including task list/cancel methods. |
| `-32602` | Invalid parameters, unknown tool, invalid ID/URI format, or task result not yet available. |
| `-32001` | Authentication required. |
| `-32004` | Exact document was not found or is not visible to this account. |
| `-32010` | Result expired. |
| `-32029` | Rate or quota limit reached. |
| `-32009` | Document state conflicts with the requested operation. |
| `-32003` | Storage/service is temporarily unavailable. |
| `-32000` | MCP request envelope is too large. |
| `-32603` | Internal operation failed without exposing implementation details. |

When Redis result retention expires, result-bearing tool/resource calls fail even while task metadata may still be available. After the terminal PostgreSQL row reaches `DOCUMENT_TTL`, the same task or resource becomes not found. Consume and persist required output before `resultExpiresAt`.
