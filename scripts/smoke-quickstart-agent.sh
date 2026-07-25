#!/usr/bin/env bash
# End-to-end regression for docs/QUICKSTART-AGENT.md (no live MinIO required).
# Covers: API + register + agent token + MCP whoami/list_drives + jobs +
# ensure_mounted_hint + offline Qiniu object_presign_get.
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

DB=$(mktemp /tmp/aihub-qs-XXXXXX.db)
HTTP_ADDR=":${API_PORT}" AI_CLOUDHUB_DB="$DB" JWT_SECRET="${JWT_SECRET:-local-dev-jwt-secretxx}" \
  ./.bin/api >/tmp/aihub-qs-api.log 2>&1 &
APID=$!
cleanup() { kill "$APID" 2>/dev/null || true; rm -f "$DB" "${DB}-wal" "${DB}-shm"; }
trap cleanup EXIT

for _ in $(seq 1 50); do
  "${CURL[@]}" "$API/healthz" >/dev/null 2>&1 && break
  sleep 0.1
done
"${CURL[@]}" "$API/healthz" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("status")=="ok", d'

echo "== register/login =="
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"dev","password":"devpassxx"}' >/dev/null || true
TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"dev","password":"devpassxx"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== provider + drive =="
PID=$("${CURL[@]}" -X POST "$API/v1/providers" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"name":"local-minio","type":"minio","credentials":{"access_key":"minioadmin","secret_key":"minioadmin","endpoint":"http://127.0.0.1:9000"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
DID=$("${CURL[@]}" -X POST "$API/v1/drives" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"workspace\",\"provider_id\":\"$PID\",\"bucket\":\"mybucket\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "== agent + token =="
AID=$("${CURL[@]}" -X POST "$API/v1/agents" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"mcpbot\",\"default_scopes\":[\"drive.read\",\"drive.write\",\"job.run\",\"provider.read\"],\"allowed_drive_ids\":[\"$DID\"]}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
ATOK=$("${CURL[@]}" -X POST "$API/v1/agents/$AID/token" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

export AI_CLOUDHUB_API="$API"
export AI_CLOUDHUB_TOKEN="$ATOK"
export AI_CLOUDHUB_WORKSPACE=/workspace

echo "== MCP whoami + list_drives =="
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami","arguments":{}}}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_drives","arguments":{}}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id") in (2,3):
    r=d.get("result") or {}
    assert not r.get("isError"), r
print("mcp whoami/list_drives ok")
'

echo "== MCP job create/claim/complete =="
JID=$(printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"create_job\",\"arguments\":{\"drive_id\":\"$DID\",\"command\":[\"echo\",\"hello-byoc\"],\"mode\":\"sync_workspace\"}}}" \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  body=json.loads(((d.get("result") or {}).get("content") or [{}])[0].get("text") or "")
  assert body.get("status")=="pending", body
  print(body["id"])
')
CLAIM=$(printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"claim_next_job","arguments":{}}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  body=json.loads(((d.get("result") or {}).get("content") or [{}])[0].get("text") or "")
  assert body.get("status")=="running", body
  print(body["id"])
')
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"complete_job\",\"arguments\":{\"job_id\":\"$CLAIM\",\"ok\":true,\"note\":\"qs\"}}}" \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  body=json.loads(((d.get("result") or {}).get("content") or [{}])[0].get("text") or "")
  assert body.get("status")=="succeeded", body
print("job flow ok", "'"$JID"'")
'

echo "== binding + ensure_mounted_hint =="
BID=$("${CURL[@]}" -X POST "$API/v1/bindings" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"device_id\":\"laptop-1\",\"mount_point\":\"/workspace\",\"desired\":\"mounted\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
export AI_CLOUDHUB_DEVICE_ID=laptop-1
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"ensure_mounted_hint\",\"arguments\":{\"drive_id\":\"$DID\",\"mount_point\":\"/workspace\"}}}" \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  assert not r.get("isError"), r
  text=((r.get("content") or [{}])[0].get("text") or "")
  assert len(text) > 20, text
print("ensure_mounted_hint ok")
'

echo "== offline Qiniu object_presign_get =="
QPID=$("${CURL[@]}" -X POST "$API/v1/providers" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"name":"kodo","type":"qiniu","credentials":{"access_key":"AK","secret_key":"SKsecretxx","endpoint":"cdn.example.com"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
QDID=$("${CURL[@]}" -X POST "$API/v1/drives" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"qd\",\"provider_id\":\"$QPID\",\"bucket\":\"b\",\"prefix\":\"ws\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
export AI_CLOUDHUB_TOKEN="$TOK"
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"object_presign_get\",\"arguments\":{\"drive_id\":\"$QDID\",\"key\":\"hello.txt\",\"ttl_min\":15}}}" \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  body=json.loads(((d.get("result") or {}).get("content") or [{}])[0].get("text") or "")
  assert body.get("method")=="qiniu_download", body
print("qiniu_download ok")
'

echo "OK smoke-quickstart-agent agent=$AID drive=$DID binding=$BID"
