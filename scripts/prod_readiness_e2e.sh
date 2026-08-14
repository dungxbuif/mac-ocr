#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROXY_DIR="$ROOT/proxy"
API_URL="${OCR_API_URL:-http://localhost:8080}"
RUN_ID="prod-$(date -u +%Y%m%dT%H%M%SZ)-$$"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/macocr-prod-e2e.XXXXXX")"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

need() {
  command -v "$1" >/dev/null || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

need curl
need jq
need base64
need go
need python3

request() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  local auth="${4:-}"
  local content_type="${5:-application/json}"
  local out="$TMP_DIR/response.json"
  local status
  if [[ -n "$body" ]]; then
    status="$(curl -sS -o "$out" -w "%{http_code}" -X "$method" "$API_URL$path" \
      ${auth:+-H "Authorization: Bearer $auth"} \
      -H "Content-Type: $content_type" \
      --data-binary "$body")"
  else
    status="$(curl -sS -o "$out" -w "%{http_code}" -X "$method" "$API_URL$path" \
      ${auth:+-H "Authorization: Bearer $auth"})"
  fi
  printf '%s\n' "$status"
}

expect_status() {
  local label="$1"
  local got="$2"
  local want="$3"
  if [[ "$got" != "$want" ]]; then
    echo "FAIL $label: status $got, want $want" >&2
    sed -n '1,200p' "$TMP_DIR/response.json" >&2
    exit 1
  fi
  echo "PASS $label status=$got"
}

expect_code() {
  local label="$1"
  local want="$2"
  local got
  got="$(jq -r '.code // empty' "$TMP_DIR/response.json")"
  if [[ "$got" != "$want" ]]; then
    echo "FAIL $label: code $got, want $want" >&2
    sed -n '1,200p' "$TMP_DIR/response.json" >&2
    exit 1
  fi
  echo "PASS $label code=$got"
}

poll_completed() {
  local doc_id="$1"
  local auth="$2"
  local require_text="${3:-0}"
  local deadline=$((SECONDS + 30))
  while (( SECONDS < deadline )); do
    local status
    status="$(request GET "/v1/documents/$doc_id" "" "$auth")"
    expect_status "poll $doc_id" "$status" "200" >/dev/null
    local state
    state="$(jq -r '.status' "$TMP_DIR/response.json")"
    if [[ "$state" == "completed" ]]; then
      jq -e '
        .result
        and .resultExpiresAt
        and (.result.text | type == "string")
        and (.result.pageCount >= 1)
        and ((.result.pages // []) | type == "array")
        and all(.result.pages[]?.blocks[]?;
          (.text | type == "string")
          and (.confidence >= 0 and .confidence <= 1)
          and ((.bbox // []) | length == 4)
        )
      ' "$TMP_DIR/response.json" >/dev/null
      if [[ "$require_text" == "1" ]]; then
        jq -e '
          ((.result.text // "") | length > 0)
          and (((.result.pages // []) | length) == .result.pageCount)
          and ([.result.pages[]?.blocks[]?] | length > 0)
        ' "$TMP_DIR/response.json" >/dev/null
      fi
      echo "PASS document completed doc=$doc_id"
      return
    fi
    if [[ "$state" == "failed" || "$state" == "cancelled" ]]; then
      echo "FAIL document terminal state=$state doc=$doc_id" >&2
      sed -n '1,200p' "$TMP_DIR/response.json" >&2
      exit 1
    fi
    sleep 1
  done
  echo "FAIL document did not complete before timeout doc=$doc_id" >&2
  exit 1
}

cat >"$TMP_DIR/sample.pdf" <<'PDF'
%PDF-1.4
1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj
2 0 obj
<< /Type /Pages /Kids [3 0 R] /Count 1 >>
endobj
3 0 obj
<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 144] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>
endobj
4 0 obj
<< /Length 62 >>
stream
BT /F1 18 Tf 36 90 Td (Mac OCR production readiness PDF) Tj ET
endstream
endobj
5 0 obj
<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>
endobj
xref
0 6
0000000000 65535 f 
0000000009 00000 n 
0000000058 00000 n 
0000000115 00000 n 
0000000234 00000 n 
0000000346 00000 n 
trailer
<< /Root 1 0 R /Size 6 >>
startxref
416
%%EOF
PDF

python3 - "$TMP_DIR/sample.png" <<'PY'
import struct
import sys
import zlib

path = sys.argv[1]
width, height = 32, 32
rows = []
for y in range(height):
    row = bytearray([0])
    for x in range(width):
        row.extend((30 + x * 4, 80 + y * 3, 180))
    rows.append(bytes(row))

def chunk(kind, data):
    return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)

png = (
    b"\x89PNG\r\n\x1a\n"
    + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
    + chunk(b"IDAT", zlib.compress(b"".join(rows)))
    + chunk(b"IEND", b"")
)
with open(path, "wb") as f:
    f.write(png)
PY
PNG_B64="$(base64 <"$TMP_DIR/sample.png" | tr -d '\n')"
PDF_B64="$(base64 <"$TMP_DIR/sample.pdf" | tr -d '\n')"

echo "RUN $RUN_ID"

curl -fsS "$API_URL/healthz" | jq -e '.status == "ok"' >/dev/null
echo "PASS healthz"
curl -fsS "$API_URL/readyz" | jq -e '.status == "ready"' >/dev/null
echo "PASS readyz"
curl -fsS "$API_URL/api/v1/openapi.json" | jq -e '
  .openapi == "3.1.0"
  and has("x-mcp-tools")
  and .components.securitySchemes.apiKeyAuth.type == "apiKey"
  and (.components.securitySchemes.bearerAuth? == null)
  and (.paths["/v1/documents"].get? == null)
  and (.paths["/v1/documents/{documentId}"].delete? == null)
  and (."x-mcp-tools" | length == 3)
' >/dev/null
echo "PASS openapi"
curl -fsS "$API_URL/api/v1/docs" >/dev/null
echo "PASS swagger-docs"
curl -fsS -o "$TMP_DIR/ocr-response-docs.html" "$API_URL/api/OCR_RESPONSE"
grep -q "OCR response model" "$TMP_DIR/ocr-response-docs.html"
echo "PASS docusaurus OCR response docs"
curl -fsS -o "$TMP_DIR/mcp-docs.html" "$API_URL/api/MCP_INTEGRATION"
grep -q "MCP integration" "$TMP_DIR/mcp-docs.html"
echo "PASS docusaurus MCP docs"
curl -fsS -o "$TMP_DIR/admin.html" "$API_URL/admin/"
grep -q 'id="root"' "$TMP_DIR/admin.html"
echo "PASS admin static app"

ADMIN_EMAIL="admin-$RUN_ID@example.test"
USER_EMAIL="user-$RUN_ID@example.test"
INDEPENDENT_USER_EMAIL="independent-$RUN_ID@example.test"
ADMIN_PASSWORD="Admin-${RUN_ID}-password-12345"

(cd "$PROXY_DIR" && go run ./cmd/admin seed --email "$ADMIN_EMAIL" --password "$ADMIN_PASSWORD") >/dev/null
ADMIN_LOGIN_BODY="$(jq -n --arg email "$ADMIN_EMAIL" --arg password "$ADMIN_PASSWORD" '{email:$email,password:$password}')"
curl -fsS -c "$TMP_DIR/admin.cookies" -H "Content-Type: application/json" --data-binary "$ADMIN_LOGIN_BODY" "$API_URL/v1/auth/login" >"$TMP_DIR/admin-login.json"
ADMIN_CSRF="$(jq -r '.csrfToken' "$TMP_DIR/admin-login.json")"
jq -e '.user.role == "admin" and (.csrfToken | length > 0)' "$TMP_DIR/admin-login.json" >/dev/null
curl -fsS -b "$TMP_DIR/admin.cookies" "$API_URL/v1/admin/dashboard" >/dev/null
curl -fsS -b "$TMP_DIR/admin.cookies" "$API_URL/v1/users" >/dev/null
curl -fsS -b "$TMP_DIR/admin.cookies" "$API_URL/v1/admin/documents" >/dev/null
curl -fsS -X POST -b "$TMP_DIR/admin.cookies" -H "X-CSRF-Token: $ADMIN_CSRF" "$API_URL/v1/auth/logout" >/dev/null
echo "PASS admin login/dashboard/users/documents/logout"
CREATE_USER_OUTPUT="$(cd "$PROXY_DIR" && go run ./cmd/admin create-user --email "$USER_EMAIL" --rate 120 --quota 20)"
USER_ID="$(printf '%s\n' "$CREATE_USER_OUTPUT" | awk -F: '/ID:/ {gsub(/^[ \t]+/, "", $2); print $2; exit}')"
KEY_OUTPUT="$(cd "$PROXY_DIR" && go run ./cmd/admin create-key --user-id "$USER_ID" --name "$RUN_ID" --rate 120)"
API_KEY="$(printf '%s\n' "$KEY_OUTPUT" | awk -F'API Key:[[:space:]]*' '/API Key:/ {print $2; exit}')"
KEY_ID="$(printf '%s\n' "$KEY_OUTPUT" | awk -F: '/Key ID:/ {gsub(/^[ \t]+/, "", $2); print $2; exit}')"
if [[ -z "$USER_ID" || -z "$API_KEY" || -z "$KEY_ID" ]]; then
  echo "FAIL could not parse admin CLI output" >&2
  echo "$CREATE_USER_OUTPUT" >&2
  echo "$KEY_OUTPUT" >&2
  exit 1
fi
echo "PASS admin create user/key user=$USER_ID key=$KEY_ID"

INDEPENDENT_OUTPUT="$(cd "$PROXY_DIR" && go run ./cmd/admin create-user --email "$INDEPENDENT_USER_EMAIL" --rate 120 --quota 20)"
INDEPENDENT_USER_ID="$(printf '%s\n' "$INDEPENDENT_OUTPUT" | awk -F: '/ID:/ {gsub(/^[ \t]+/, "", $2); print $2; exit}')"
INDEPENDENT_KEY_OUTPUT="$(cd "$PROXY_DIR" && go run ./cmd/admin create-key --user-id "$INDEPENDENT_USER_ID" --name independent --rate 120)"
INDEPENDENT_KEY="$(printf '%s\n' "$INDEPENDENT_KEY_OUTPUT" | awk -F'API Key:[[:space:]]*' '/API Key:/ {print $2; exit}')"
if [[ -z "$INDEPENDENT_USER_ID" || -z "$INDEPENDENT_KEY" ]]; then
  echo "FAIL could not create independent user/key" >&2
  echo "$INDEPENDENT_OUTPUT" >&2
  echo "$INDEPENDENT_KEY_OUTPUT" >&2
  exit 1
fi
echo "PASS independent user/key user=$INDEPENDENT_USER_ID"

status="$(request GET "/v1/documents/doc_missing")"
expect_status "missing auth" "$status" "401"
expect_code "missing auth" "UNAUTHORIZED"

status="$(request GET "/v1/documents" "" "$API_KEY")"
expect_status "public document list removed" "$status" "404"
status="$(request DELETE "/v1/documents/doc_missing" "" "$API_KEY")"
expect_status "public document delete removed" "$status" "404"

status="$(curl -sS -o "$TMP_DIR/response.json" -w "%{http_code}" -X POST "$API_URL/v1/documents" \
  -H "Authorization: Bearer $API_KEY" -F "file=@$TMP_DIR/sample.png")"
expect_status "multipart rejected" "$status" "415"
expect_code "multipart rejected" "UNSUPPORTED_CONTENT_TYPE"

status="$(request POST "/v1/documents" '{"input":{"url":"https://example.com/a.png","base64":"AAAA"}}' "$API_KEY")"
expect_status "both sources rejected" "$status" "400"
expect_code "both sources rejected" "INVALID_SOURCE"

status="$(request POST "/v1/documents" '{"input":{"base64":"not-valid***"}}' "$API_KEY")"
expect_status "invalid base64 rejected" "$status" "400"
expect_code "invalid base64 rejected" "INVALID_BASE64"

status="$(request POST "/v1/documents" '{"input":{"url":"https://127.0.0.1/secret"}}' "$API_KEY")"
expect_status "private url rejected" "$status" "400"
expect_code "private url rejected" "SSRF_BLOCKED"

status="$(request POST "/v1/documents" '{"input":{"base64":"AAAA","unexpected":true}}' "$API_KEY")"
expect_status "unknown field rejected" "$status" "400"
expect_code "unknown field rejected" "INVALID_INPUT"

status="$(request POST "/v1/documents" '{"input":{"base64":"AAAA"}} {}' "$API_KEY")"
expect_status "trailing json rejected" "$status" "400"
expect_code "trailing json rejected" "INVALID_INPUT"

status="$(request POST "/v1/documents" "{\"input\":{\"base64\":\"$PNG_B64\"},\"options\":{\"recognitionLevel\":\"accurate\",\"languages\":[\"en-US\"]}}" "$API_KEY")"
expect_status "submit png" "$status" "202"
PNG_DOC="$(jq -r '.documentId' "$TMP_DIR/response.json")"
poll_completed "$PNG_DOC" "$API_KEY"

status="$(request POST "/v1/documents" "{\"input\":{\"base64\":\"$PDF_B64\"},\"notification\":{\"type\":\"sse\"}}" "$API_KEY")"
expect_status "submit pdf sse" "$status" "202"
PDF_DOC="$(jq -r '.documentId' "$TMP_DIR/response.json")"
poll_completed "$PDF_DOC" "$API_KEY" 1

curl -sS -N --max-time 5 -H "Authorization: Bearer $API_KEY" "$API_URL/v1/events" >"$TMP_DIR/sse.txt" || true
if ! grep -q "$PDF_DOC" "$TMP_DIR/sse.txt"; then
  echo "FAIL sse stream did not contain pdf document event $PDF_DOC" >&2
  sed -n '1,120p' "$TMP_DIR/sse.txt" >&2
  exit 1
fi
echo "PASS sse event contains document=$PDF_DOC"

status="$(request POST "/v1/uploads/presign" '{"filename":"too-large.pdf","sizeBytes":104857601,"contentType":"application/pdf"}' "$API_KEY")"
expect_status "oversized presign rejected" "$status" "413"
expect_code "oversized presign rejected" "URL_CONTENT_TOO_LARGE"
status="$(request POST "/v1/uploads/presign" '{"filename":"one-gib.bin","sizeBytes":1073741824}' "$API_KEY")"
expect_status "one GiB presign rejected" "$status" "413"
status="$(request POST "/v1/uploads/presign" '{"filename":"boundary.bin","sizeBytes":104857600}' "$API_KEY")"
expect_status "exact upload boundary accepted" "$status" "201"

PDF_SIZE="$(wc -c <"$TMP_DIR/sample.pdf" | tr -d ' ')"
PRESIGN_BODY="$(jq -n --argjson size "$PDF_SIZE" '{filename:"presigned.pdf",sizeBytes:$size,contentType:"application/pdf"}')"
status="$(request POST "/v1/uploads/presign" "$PRESIGN_BODY" "$API_KEY")"
expect_status "presign upload" "$status" "201"
UPLOAD_URL="$(jq -r '.uploadUrl' "$TMP_DIR/response.json")"
SOURCE_URL="$(jq -r '.sourceUrl' "$TMP_DIR/response.json")"
if [[ -z "$UPLOAD_URL" || "$UPLOAD_URL" == "null" || -z "$SOURCE_URL" || "$SOURCE_URL" == "null" ]]; then
  echo "FAIL presign response missing uploadUrl/sourceUrl" >&2
  sed -n '1,200p' "$TMP_DIR/response.json" >&2
  exit 1
fi
jq -e --arg size "$PDF_SIZE" '.headers["Content-Length"] == $size' "$TMP_DIR/response.json" >/dev/null
echo "PASS presign binds exact Content-Length"
curl -fsS -X PUT "$UPLOAD_URL" -H "Content-Type: application/pdf" --data-binary "@$TMP_DIR/sample.pdf" >/dev/null
echo "PASS presigned PUT upload"
status="$(request POST "/v1/documents" "$(jq -n --arg url "$SOURCE_URL" '{input:{url:$url}}')" "$INDEPENDENT_KEY")"
expect_status "independent user cannot use sourceUrl" "$status" "404"

MISSING_SOURCE_URL="s3://macocr-inputs/uploads/$USER_ID/missing.pdf"
status="$(request POST "/v1/documents" "$(jq -n --arg url "$MISSING_SOURCE_URL" '{input:{url:$url}}')" "$API_KEY")"
expect_status "missing uploaded sourceUrl rejected" "$status" "400"
expect_code "missing uploaded sourceUrl rejected" "INVALID_URL"

status="$(request POST "/v1/documents" "$(jq -n --arg url "$SOURCE_URL" '{input:{url:$url},options:{recognitionLevel:"accurate",languages:["en-US"]}}')" "$API_KEY")"
expect_status "submit presigned sourceUrl" "$status" "202"
PRESIGNED_DOC="$(jq -r '.documentId' "$TMP_DIR/response.json")"
poll_completed "$PRESIGNED_DOC" "$API_KEY" 1

status="$(request POST "/v1/batches" "[{\"input\":{\"base64\":\"$PNG_B64\"}},{\"input\":{\"base64\":\"$PDF_B64\"},\"options\":{\"languages\":[\"en-US\"]}}]" "$API_KEY")"
expect_status "submit batch" "$status" "202"
jq -e '.summary.total == 2 and .summary.accepted == 2 and (.items | length) == 2 and (.batchId? == null)' "$TMP_DIR/response.json" >/dev/null
BATCH_DOC_0="$(jq -r '.items[0].documentId' "$TMP_DIR/response.json")"
BATCH_DOC_1="$(jq -r '.items[1].documentId' "$TMP_DIR/response.json")"
poll_completed "$BATCH_DOC_0" "$API_KEY"
poll_completed "$BATCH_DOC_1" "$API_KEY" 1

status="$(request POST "/v1/batches" "[{\"input\":{\"base64\":\"$PNG_B64\"}},{\"input\":{\"base64\":\"bad***\"}}]" "$API_KEY")"
expect_status "invalid batch rejected" "$status" "400"
expect_code "invalid batch rejected" "INVALID_BASE64"
jq -e '.detail | contains("batch item 1")' "$TMP_DIR/response.json" >/dev/null
echo "PASS invalid batch identifies index"

(cd "$PROXY_DIR" && go run ./cmd/admin disable-user --user-id "$USER_ID") >/dev/null
status="$(request GET "/v1/documents/$PNG_DOC" "" "$API_KEY")"
expect_status "disabled user blocked" "$status" "401"
(cd "$PROXY_DIR" && go run ./cmd/admin enable-user --user-id "$USER_ID") >/dev/null
status="$(request GET "/v1/documents/$PNG_DOC" "" "$API_KEY")"
expect_status "enabled user allowed exact document read" "$status" "200"

MCP_INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"prod-e2e","version":"1"}}}'
status="$(request POST "/mcp" "$MCP_INIT" "$API_KEY")"
expect_status "mcp initialize" "$status" "200"
jq -e '.result.protocolVersion == "2025-11-25"' "$TMP_DIR/response.json" >/dev/null
status="$(request POST "/mcp" '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"prod-e2e","version":"1"}}}' "$API_KEY")"
expect_status "mcp tools/list" "$status" "200"
jq -e '.result.tools | map(.name) | index("submit_ocr_document")' "$TMP_DIR/response.json" >/dev/null
jq -e '(.result.tools | map(.name) | index("cancel_ocr_document")) == null' "$TMP_DIR/response.json" >/dev/null
echo "PASS mcp tools/list contains submit_ocr_document"

MCP_SUBMIT="$(jq -n --arg b64 "$PDF_B64" '{jsonrpc:"2.0",id:3,method:"tools/call",params:{name:"submit_ocr_document",arguments:{input:{base64:$b64},options:{recognitionLevel:"accurate",languages:["en-US"]}},task:{ttl:600}}}')"
status="$(request POST "/mcp" "$MCP_SUBMIT" "$API_KEY")"
expect_status "mcp submit document" "$status" "200"
MCP_DOC="$(jq -r '.result.task.id // .result.task.taskId // .result._meta["io.modelcontextprotocol/related-task"].taskId' "$TMP_DIR/response.json")"
if [[ -z "$MCP_DOC" || "$MCP_DOC" == "null" ]]; then
  echo "FAIL mcp submit did not return task/document id" >&2
  sed -n '1,200p' "$TMP_DIR/response.json" >&2
  exit 1
fi
poll_completed "$MCP_DOC" "$API_KEY" 1

MCP_TASK_RESULT="$(jq -n --arg id "$MCP_DOC" '{jsonrpc:"2.0",id:4,method:"tasks/result",params:{taskId:$id}}')"
status="$(request POST "/mcp" "$MCP_TASK_RESULT" "$API_KEY")"
expect_status "mcp tasks/result" "$status" "200"
jq -e '.result.content[0].text | fromjson | .status == "completed"' "$TMP_DIR/response.json" >/dev/null
echo "PASS mcp tasks/result completed"

MCP_RESOURCE_READ="$(jq -n --arg uri "ocr://documents/$MCP_DOC" '{jsonrpc:"2.0",id:5,method:"resources/read",params:{uri:$uri}}')"
status="$(request POST "/mcp" "$MCP_RESOURCE_READ" "$API_KEY")"
expect_status "mcp resources/read" "$status" "200"
jq -e '.result.contents[0].text | fromjson | .documentId == "'"$MCP_DOC"'"' "$TMP_DIR/response.json" >/dev/null
echo "PASS mcp resources/read document=$MCP_DOC"

curl -sS -N --max-time 5 -H "Authorization: Bearer $API_KEY" "$API_URL/mcp" >"$TMP_DIR/mcp-sse.txt" || true
if ! grep -q "$MCP_DOC" "$TMP_DIR/mcp-sse.txt"; then
  echo "FAIL MCP SSE stream did not contain document event $MCP_DOC" >&2
  sed -n '1,160p' "$TMP_DIR/mcp-sse.txt" >&2
  exit 1
fi
echo "PASS mcp GET stream contains document=$MCP_DOC"

RATE_USER_EMAIL="rate-$RUN_ID@example.test"
RATE_USER_OUTPUT="$(cd "$PROXY_DIR" && go run ./cmd/admin create-user --email "$RATE_USER_EMAIL" --rate 2 --quota 5)"
RATE_USER_ID="$(printf '%s\n' "$RATE_USER_OUTPUT" | awk -F: '/ID:/ {gsub(/^[ \t]+/, "", $2); print $2; exit}')"
RATE_KEY_OUTPUT="$(cd "$PROXY_DIR" && go run ./cmd/admin create-key --user-id "$RATE_USER_ID" --name rate --rate 2)"
RATE_KEY="$(printf '%s\n' "$RATE_KEY_OUTPUT" | awk -F'API Key:[[:space:]]*' '/API Key:/ {print $2; exit}')"
request GET "/v1/documents/doc_missing" "" "$RATE_KEY" >/dev/null
request GET "/v1/documents/doc_missing" "" "$RATE_KEY" >/dev/null
status="$(request GET "/v1/documents/doc_missing" "" "$RATE_KEY")"
expect_status "rate limit enforced" "$status" "429"
expect_code "rate limit enforced" "RATE_LIMITED"

QUOTA_USER_EMAIL="quota-$RUN_ID@example.test"
QUOTA_USER_OUTPUT="$(cd "$PROXY_DIR" && go run ./cmd/admin create-user --email "$QUOTA_USER_EMAIL" --rate 20 --quota 1)"
QUOTA_USER_ID="$(printf '%s\n' "$QUOTA_USER_OUTPUT" | awk -F: '/ID:/ {gsub(/^[ \t]+/, "", $2); print $2; exit}')"
QUOTA_KEY_OUTPUT="$(cd "$PROXY_DIR" && go run ./cmd/admin create-key --user-id "$QUOTA_USER_ID" --name quota --rate 20)"
QUOTA_KEY="$(printf '%s\n' "$QUOTA_KEY_OUTPUT" | awk -F'API Key:[[:space:]]*' '/API Key:/ {print $2; exit}')"
status="$(request POST "/v1/uploads/presign" '{"filename":"quota-check.png","sizeBytes":1024,"contentType":"image/png"}' "$QUOTA_KEY")"
expect_status "presign does not consume document quota" "$status" "201"
status="$(request POST "/v1/documents" "{\"input\":{\"base64\":\"$PNG_B64\"}}" "$QUOTA_KEY")"
expect_status "first document consumes quota" "$status" "202"
status="$(request POST "/v1/documents" "{\"input\":{\"base64\":\"$PNG_B64\"}}" "$QUOTA_KEY")"
expect_status "document quota enforced" "$status" "429"
expect_code "document quota enforced" "QUOTA_EXCEEDED"

STORAGE_USER_EMAIL="storage-$RUN_ID@example.test"
STORAGE_USER_OUTPUT="$(cd "$PROXY_DIR" && go run ./cmd/admin create-user --email "$STORAGE_USER_EMAIL" --rate 30 --quota 5 --storage-bytes "$PDF_SIZE")"
STORAGE_USER_ID="$(printf '%s\n' "$STORAGE_USER_OUTPUT" | awk -F: '/ID:/ {gsub(/^[ \t]+/, "", $2); print $2; exit}')"
STORAGE_KEY_OUTPUT="$(cd "$PROXY_DIR" && go run ./cmd/admin create-key --user-id "$STORAGE_USER_ID" --name storage --rate 30)"
STORAGE_KEY="$(printf '%s\n' "$STORAGE_KEY_OUTPUT" | awk -F'API Key:[[:space:]]*' '/API Key:/ {print $2; exit}')"
status="$(request POST "/v1/uploads/presign" "$PRESIGN_BODY" "$STORAGE_KEY")"
expect_status "storage quota presign reservation" "$status" "201"
STORAGE_UPLOAD_URL="$(jq -r '.uploadUrl' "$TMP_DIR/response.json")"
STORAGE_SOURCE_URL="$(jq -r '.sourceUrl' "$TMP_DIR/response.json")"
curl -fsS -X PUT "$STORAGE_UPLOAD_URL" -H "Content-Type: application/pdf" --data-binary "@$TMP_DIR/sample.pdf" >/dev/null
status="$(request POST "/v1/documents" "$(jq -n --arg url "$STORAGE_SOURCE_URL" '{input:{url:$url}}')" "$STORAGE_KEY")"
expect_status "storage reservation converted to usage" "$status" "202"
STORAGE_DOC="$(jq -r '.documentId' "$TMP_DIR/response.json")"
poll_completed "$STORAGE_DOC" "$STORAGE_KEY" 1
status="$(request POST "/v1/uploads/presign" '{"filename":"over-storage-quota.bin","sizeBytes":1}' "$STORAGE_KEY")"
expect_status "aggregate storage quota enforced" "$status" "429"
expect_code "aggregate storage quota enforced" "STORAGE_QUOTA_EXCEEDED"

(cd "$PROXY_DIR" && go run ./cmd/admin revoke-key --user-id "$USER_ID" --key-id "$KEY_ID") >/dev/null
status="$(request GET "/v1/documents/$PNG_DOC" "" "$API_KEY")"
expect_status "revoked key blocked" "$status" "401"

echo "BENCH start healthz-20"
BENCH_OUT="$TMP_DIR/bench.txt"
/usr/bin/time -l bash -c "for i in {1..20}; do curl -fsS -H 'Authorization: Bearer $RATE_KEY' '$API_URL/healthz' >/dev/null; done" >"$BENCH_OUT" 2>&1 || true
sed -n '1,80p' "$BENCH_OUT"

echo "SUMMARY user=$USER_ID docs=$PNG_DOC,$PDF_DOC,$BATCH_DOC_0,$BATCH_DOC_1"
echo "PASS prod_readiness_e2e"
