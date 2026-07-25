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
need={"list_jobs","get_job","create_job","claim_next_job","complete_job","heartbeat_job","cancel_job","list_providers",
      "list_marketplace","install_marketplace","marketplace_checkout","list_memory","put_memory","search_memory",
      "list_graph","link_graph","list_connectors","connectors_catalog","create_connector","get_connector","delete_connector",
      "list_lineage","record_lineage"}
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
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"heartbeat_job\",\"arguments\":{\"job_id\":\"$CLAIMED\"}}}" \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"): raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert body.get("status")=="running" and body.get("heartbeat_at"), body
  print("heartbeat ok")
'

printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"complete_job\",\"arguments\":{\"job_id\":\"$CLAIMED\",\"ok\":true,\"note\":\"mcp-smoke\",\"exit_code\":0,\"duration_ms\":55}}}" \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  body=json.loads(((d.get("result") or {}).get("content") or [{}])[0].get("text") or "")
  assert body.get("status")=="succeeded", body
  assert body.get("exit_code")==0 and body.get("duration_ms")==55, body
  print("completed")
'
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"get_job\",\"arguments\":{\"job_id\":\"$CLAIMED\"}}}" \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"): raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert body.get("id")=="'"$CLAIMED"'" and body.get("status")=="succeeded", body
  assert body.get("exit_code")==0, body
  print("get_job ok", body["id"])
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

echo "== MCP Stage C (memory / graph / marketplace skill / connectors / lineage) =="
export AI_CLOUDHUB_TOKEN="$ATOK"
# put_memory + list_memory
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"put_memory","arguments":{"layer":"semantic","key":"mcp.pref","content":"likes r2","embedding":[0.1,0.2,0.3]}}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"): raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert body.get("id") and body.get("content")=="likes r2", body
  open("/tmp/aihub-mcp-mem","w").write(body["id"])
  print("put_memory ok", body["id"])
'
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_memory","arguments":{"query":[0.1,0.2,0.3],"k":3}}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"): raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  hits=body.get("hits") or body.get("items") or []
  assert len(hits)>=1, body
  print("search_memory ok", len(hits))
'
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"link_graph","arguments":{"subject":"agent:'"$AID"'","relation":"prefers","object":"provider:r2"}}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"): raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert body.get("relation")=="prefers", body
  print("link_graph ok")
'
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_graph","arguments":{"subject":"agent:'"$AID"'"}}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"): raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert len(body.get("items") or [])>=1, body
  print("list_graph ok")
'
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_marketplace","arguments":{}}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"): raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert any(i.get("id")=="sys.skill.qiniu_presign" for i in (body.get("items") or [])), body
  print("list_marketplace ok")
'
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"install_marketplace","arguments":{"item_id":"sys.skill.qiniu_presign"}}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"): raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert body.get("kind")=="skill" and body.get("memory_id"), body
  assert not body.get("agent_id"), body
  print("install_marketplace skill ok", body["memory_id"])
'
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"connectors_catalog","arguments":{}}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"): raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert any(i.get("type")=="git" for i in (body.get("items") or [])), body
  print("connectors_catalog ok")
'
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"record_lineage","arguments":{"action":"mcp.smoke","entity":"agent:'"$AID"'","detail":"stage-c"}}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"): raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert body.get("action")=="mcp.smoke", body
  print("record_lineage ok")
'
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_lineage","arguments":{"entity":"agent:'"$AID"'"}}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"): raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert len(body.get("items") or [])>=1, body
  print("list_lineage ok")
'

echo "== MCP create_connector / get / delete (human) + checkout =="
export AI_CLOUDHUB_TOKEN="$TOK"
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create_connector","arguments":{"type":"postgres","name":"mcp-pg","config":{"host":"db.example.com","database":"app","user":"ro","password":"STRIPME"}}}}' \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"): raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert body.get("type")=="postgres" and body.get("id"), body
  cfg=body.get("config") or {}
  assert "password" not in cfg and "STRIPME" not in str(cfg), body
  open("/tmp/aihub-mcp-cid","w").write(body["id"])
  print("create_connector ok", body["id"])
'
CIDM=$(cat /tmp/aihub-mcp-cid)
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"get_connector\",\"arguments\":{\"connector_id\":\"$CIDM\"}}}" \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"): raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert body.get("id") and body.get("type")=="postgres", body
  print("get_connector ok")
'
# paid checkout mock url
PAID=$("${CURL[@]}" -X POST "$API/v1/marketplace" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"name":"mcp-paid","kind":"skill","price_cents":99,"currency":"usd","public":true,"payload":{"x":1}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"marketplace_checkout\",\"arguments\":{\"item_id\":\"$PAID\"}}}" \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"): raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert body.get("status")=="pending" and body.get("checkout_url"), body
  print("marketplace_checkout ok", body["session_id"])
'
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"delete_connector\",\"arguments\":{\"connector_id\":\"$CIDM\"}}}" \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"): raise SystemExit(str(r))
  print("delete_connector ok")
'

echo "== MCP object_presign_get (Qiniu native) =="
# Human token can create qiniu provider; agent has drive.read for the minio drive only —
# create qiniu drive with human, then call MCP with human token for this check.
export AI_CLOUDHUB_TOKEN="$TOK"
QPID=$("${CURL[@]}" -X POST "$API/v1/providers" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"name":"kodo","type":"qiniu","credentials":{"access_key":"AK","secret_key":"SKsecretxx","endpoint":"cdn.example.com"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
QDID=$("${CURL[@]}" -X POST "$API/v1/drives" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"qd\",\"provider_id\":\"$QPID\",\"bucket\":\"b\",\"prefix\":\"p\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"object_presign_get\",\"arguments\":{\"drive_id\":\"$QDID\",\"key\":\"x.bin\",\"ttl_min\":5}}}" \
  | ./.bin/mcp 2>/dev/null | python3 -c '
import sys,json
for line in sys.stdin:
  d=json.loads(line.strip() or "{}")
  if d.get("id")!=2: continue
  r=d.get("result") or {}
  if r.get("isError"):
    raise SystemExit(str(r))
  body=json.loads((r.get("content") or [{}])[0].get("text") or "")
  assert body.get("method")=="qiniu_download", body
  assert "token=" in (body.get("url") or ""), body
  print("mcp qiniu_download ok")
'

echo "OK smoke-mcp-jobs"
