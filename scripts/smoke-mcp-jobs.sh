#!/usr/bin/env bash
# MCP job tools smoke: tools/list + create_job / claim_next_job / complete_job / cancel_job via stdio.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
if [[ -z "${API_PORT:-}" ]]; then
  API_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
fi
API="http://127.0.0.1:${API_PORT}"
export CGO_ENABLED=0
export NO_PROXY="127.0.0.1,localhost,::1"
export no_proxy="$NO_PROXY"
CURL=(curl -sS --noproxy '*')

cd "$ROOT"
mkdir -p .bin
go build -o .bin/api ./cmd/api
go build -o .bin/mcp ./cmd/mcp

DB=$(mktemp /tmp/aihub-mcp-XXXXXX.db)
HTTP_ADDR=":${API_PORT}" \
  AI_CLOUDHUB_DB="$DB" \
  JWT_SECRET="${JWT_SECRET:-mcp-smoke-jwt-secretxx}" \
  ./.bin/api >/tmp/aihub-mcp-api.log 2>&1 &
APID=$!
cleanup() { kill "$APID" 2>/dev/null || true; rm -f "$DB" "${DB}-wal" "${DB}-shm"; }
trap cleanup EXIT

for _ in $(seq 1 50); do
  "${CURL[@]}" "$API/healthz" >/dev/null 2>&1 && break
  sleep 0.1
done

echo "== register/login + fixture =="
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"mcpuser","password":"mcppassxx"}' >/dev/null || true
TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"mcpuser","password":"mcppassxx"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
PID=$("${CURL[@]}" -X POST "$API/v1/providers" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"name":"m","type":"minio","credentials":{"access_key":"ak","secret_key":"sksecret","endpoint":"http://127.0.0.1:9000"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
DID=$("${CURL[@]}" -X POST "$API/v1/drives" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"d\",\"provider_id\":\"$PID\",\"bucket\":\"b\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
AID=$("${CURL[@]}" -X POST "$API/v1/agents" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"mcpbot\",\"default_scopes\":[\"drive.read\",\"job.run\",\"provider.read\"],\"allowed_drive_ids\":[\"$DID\"]}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
ATOK=$("${CURL[@]}" -X POST "$API/v1/agents/$AID/token" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

export AI_CLOUDHUB_API="$API"
export AI_CLOUDHUB_TOKEN="$ATOK"
export AI_CLOUDHUB_WORKSPACE="/workspace"

echo "== MCP tools/list has job tools =="
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
names=set()
for line in sys.stdin:
  line=line.strip()
  if not line: continue
  d=json.loads(line)
  for t in (d.get("result") or {}).get("tools") or []:
    names.add(t["name"])
need={"list_jobs","create_job","claim_next_job","complete_job","cancel_job","list_providers"}
assert need <= names, names
print("tools ok", sorted(need))
'

echo "== MCP create_job + list_jobs + claim + complete =="
# tools/call returns content[0].text as JSON string of API body
CREATE=$(printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"create_job\",\"arguments\":{\"drive_id\":\"$DID\",\"command\":[\"echo\",\"mcp\"],\"mode\":\"sync_workspace\"}}}" \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"):
    raise SystemExit("create error: "+str(r))
  text=(r.get("content") or [{}])[0].get("text") or ""
  body=json.loads(text)
  assert body.get("id") and body.get("status")=="pending", body
  print(body["id"])
')
echo "created $CREATE"

printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_jobs","arguments":{"status":"pending"}}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  text=((d.get("result") or {}).get("content") or [{}])[0].get("text") or ""
  body=json.loads(text)
  assert len(body.get("items") or [])>=1, body
  print("pending", len(body["items"]))
'

CLAIMED=$(printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"claim_next_job","arguments":{}}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"):
    raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert body.get("status")=="running", body
  print(body["id"])
')
echo "claimed $CLAIMED"

printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"complete_job\",\"arguments\":{\"job_id\":\"$CLAIMED\",\"ok\":true,\"note\":\"mcp-smoke\"}}}" \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  body=json.loads(((d.get("result") or {}).get("content") or [{}])[0].get("text") or "")
  assert body.get("status")=="succeeded", body
  print("completed")
'

echo "== MCP cancel_job =="
J2=$(printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"create_job\",\"arguments\":{\"drive_id\":\"$DID\",\"command\":[\"sleep\",\"99\"]}}}" \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  print(json.loads(((d.get("result") or {}).get("content") or [{}])[0].get("text") or "")["id"])
')
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"cancel_job\",\"arguments\":{\"job_id\":\"$J2\"}}}" \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  body=json.loads(((d.get("result") or {}).get("content") or [{}])[0].get("text") or "")
  assert body.get("status")=="cancelled", body
  print("cancelled", body["id"])
'

echo "== MCP list_providers =="
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_providers","arguments":{}}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"):
    raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert len(body.get("items") or [])>=1, body
  print("providers", len(body["items"]))
'

echo "OK smoke-mcp-jobs"
