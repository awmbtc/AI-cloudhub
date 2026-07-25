#!/usr/bin/env bash
# Stage C smoke: memory/vector, marketplace checkout+stripe webhook, lineage, graph, connectors.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
export CGO_ENABLED=0
export NO_PROXY="127.0.0.1,localhost,::1"
export no_proxy="$NO_PROXY"
CURL=(curl -sS --noproxy '*')

if [[ -z "${API_PORT:-}" ]]; then
  API_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
fi
API="http://127.0.0.1:${API_PORT}"
mkdir -p .bin
go build -o .bin/api ./cmd/api

DB=$(mktemp /tmp/aihub-sc.db)
export AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET=whsec_smoke
HTTP_ADDR=":${API_PORT}" AI_CLOUDHUB_DB="$DB" JWT_SECRET=stagec-smoke-jwt-xx \
  AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET=whsec_smoke \
  ./.bin/api >/tmp/aihub-stage-c-api.log 2>&1 &
APID=$!
cleanup() { kill "$APID" 2>/dev/null || true; rm -f "$DB" "${DB}-wal" "${DB}-shm"; }
trap cleanup EXIT

for _ in $(seq 1 50); do "${CURL[@]}" "$API/healthz" >/dev/null 2>&1 && break; sleep 0.1; done

"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"stagec","password":"stagecpass"}' >/dev/null || true
TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"stagec","password":"stagecpass"}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
AUTH=( -H "Authorization: Bearer $TOK" )

echo "== modules =="
"${CURL[@]}" "$API/v1/modules" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["deployment"]=="monolith"'

echo "== memory + vector search =="
"${CURL[@]}" -X POST "$API/v1/memory" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"layer":"semantic","content":"likes r2","embedding":[1,0,0]}' >/dev/null
"${CURL[@]}" -X POST "$API/v1/memory" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"layer":"semantic","content":"likes cos","embedding":[0,1,0]}' >/dev/null
"${CURL[@]}" -X POST "$API/v1/memory/search" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"query":[0.9,0.1,0],"k":2}' | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert len(d["hits"])>=1
assert d["hits"][0]["score"] > 0.5
print("vector search ok score", d["hits"][0]["score"])
'

echo "== lineage + graph =="
"${CURL[@]}" -X POST "$API/v1/lineage" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"action":"test.event","entity":"drive:demo","detail":"smoke"}' >/dev/null
"${CURL[@]}" "$API/v1/lineage?entity=drive:demo" "${AUTH[@]}" | python3 -c '
import sys,json; d=json.load(sys.stdin); assert len(d["items"])>=1; print("lineage ok")
'
"${CURL[@]}" -X POST "$API/v1/graph" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"subject":"agent:a1","relation":"can_access","object":"drive:demo"}' >/dev/null
"${CURL[@]}" "$API/v1/graph?subject=agent:a1" "${AUTH[@]}" | python3 -c '
import sys,json; d=json.load(sys.stdin); assert len(d["items"])>=1; print("graph ok")
'

echo "== connectors =="
"${CURL[@]}" "$API/v1/connectors/catalog" | python3 -c '
import sys,json; d=json.load(sys.stdin); assert any(i["type"]=="git" for i in d["items"]); print("catalog ok")
'
CID=$("${CURL[@]}" -X POST "$API/v1/connectors" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"type":"git","name":"app","config":{"remote_url":"https://github.com/example/app.git","branch":"main","password":"STRIPME"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
"${CURL[@]}" "$API/v1/connectors/$CID" "${AUTH[@]}" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d["type"]=="git"
cfg=d.get("config") or {}
# password must be stripped
assert "password" not in cfg and "STRIPME" not in str(cfg)
print("connector ok", d["id"])
'

echo "== marketplace checkout + stripe webhook =="
ITEM=$("${CURL[@]}" -X POST "$API/v1/marketplace" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"name":"paid-skill","kind":"skill","price_cents":500,"currency":"usd","public":true,"payload":{"x":1}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
CHK=$("${CURL[@]}" -X POST "$API/v1/marketplace/$ITEM/checkout" "${AUTH[@]}" -H 'Content-Type: application/json' -d '{}')
echo "$CHK" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d["status"]=="pending"
assert d["stripe_metadata"]["purchase_id"]
print("checkout", d["id"])
open("/tmp/aihub-purchase-id","w").write(d["id"])
open("/tmp/aihub-buyer-id","w").write(d["user_id"])
'
PURCHASE_ID=$(cat /tmp/aihub-purchase-id)
BUYER_ID=$(cat /tmp/aihub-buyer-id)
PAYLOAD=$(python3 - <<PY
import json
print(json.dumps({
  "type":"checkout.session.completed",
  "data":{"object":{"id":"cs_smoke","metadata":{"purchase_id":"$PURCHASE_ID","user_id":"$BUYER_ID","item_id":"$ITEM"}}}
}))
PY
)
export PAYLOAD
SIG=$(PAYLOAD="$PAYLOAD" python3 - <<'PY'
import hmac, hashlib, time, os
secret=b"whsec_smoke"
payload=os.environ["PAYLOAD"].encode()
ts=int(time.time())
mac=hmac.new(secret, f"{ts}.".encode()+payload, hashlib.sha256).hexdigest()
print(f"t={ts},v1={mac}")
PY
)
"${CURL[@]}" -X POST "$API/v1/webhooks/stripe" \
  -H "Content-Type: application/json" \
  -H "Stripe-Signature: $SIG" \
  -d "$PAYLOAD" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d.get("status")=="paid", d
print("stripe webhook ok")
'

echo "== paid install gate =="
PAID=$("${CURL[@]}" -X POST "$API/v1/marketplace" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"name":"paid-agent","kind":"agent_template","price_cents":100,"currency":"usd","public":true,"payload":{"name":"paidbot","default_scopes":["drive.read"]}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
CODE=$("${CURL[@]}" -o /tmp/aihub-inst.json -w '%{http_code}' -X POST "$API/v1/marketplace/$PAID/install" "${AUTH[@]}" -d '{}')
test "$CODE" = "400"
python3 -c 'import json; d=json.load(open("/tmp/aihub-inst.json")); assert "purchase" in d.get("error","").lower(), d; print("install blocked ok")'
CHK=$("${CURL[@]}" -X POST "$API/v1/marketplace/$PAID/checkout" "${AUTH[@]}" -d '{}')
PID=$(echo "$CHK" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
BUY=$(echo "$CHK" | python3 -c 'import sys,json; print(json.load(sys.stdin)["user_id"])')
PAYLOAD=$(python3 - <<PY
import json
print(json.dumps({"type":"checkout.session.completed","data":{"object":{"id":"cs2","metadata":{"purchase_id":"$PID","user_id":"$BUY","item_id":"$PAID"}}}}))
PY
)
export PAYLOAD
SIG=$(PAYLOAD="$PAYLOAD" python3 - <<'PY'
import hmac,hashlib,time,os
ts=int(time.time()); p=os.environ["PAYLOAD"].encode()
print("t=%d,v1=%s"%(ts,hmac.new(b"whsec_smoke",f"{ts}.".encode()+p,hashlib.sha256).hexdigest()))
PY
)
"${CURL[@]}" -X POST "$API/v1/webhooks/stripe" -H "Stripe-Signature: $SIG" -H "Content-Type: application/json" -d "$PAYLOAD" >/dev/null
INST=$("${CURL[@]}" -X POST "$API/v1/marketplace/$PAID/install" "${AUTH[@]}" -d '{}')
echo "$INST" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d.get("agent_id"), d
assert d.get("memory_id"), d
print("paid install ok", d["agent_id"], "memory", d["memory_id"])
'
AGENT_ID=$(echo "$INST" | python3 -c 'import sys,json; print(json.load(sys.stdin)["agent_id"])')
MEM_ID=$(echo "$INST" | python3 -c 'import sys,json; print(json.load(sys.stdin)["memory_id"])')

echo "== install auto memory + graph =="
"${CURL[@]}" "$API/v1/memory?key=marketplace.install.$PAID&limit=5" "${AUTH[@]}" | python3 -c '
import sys,json
d=json.load(sys.stdin)
items=d.get("items") or []
assert any(i.get("id")=="'"$MEM_ID"'" for i in items), d
assert any("Installed marketplace" in (i.get("content") or "") for i in items), d
print("install memory ok")
'
"${CURL[@]}" "$API/v1/graph?object=item:$PAID&limit=20" "${AUTH[@]}" | python3 -c '
import sys,json
d=json.load(sys.stdin)
items=d.get("items") or []
rels={ (e.get("subject"), e.get("relation"), e.get("object")) for e in items }
assert any(r[1]=="installed" and r[2]=="item:'"$PAID"'" for r in rels), (rels, d)
assert any(r[0]=="agent:'"$AGENT_ID"'" and r[1]=="from_item" for r in rels), (rels, d)
print("install graph ok")
'
"${CURL[@]}" "$API/v1/lineage?entity=item:$PAID&limit=20" "${AUTH[@]}" | python3 -c '
import sys,json
d=json.load(sys.stdin)
items=d.get("items") or []
assert any(e.get("action")=="marketplace.install" for e in items), d
print("install lineage ok")
'

echo "== job connector_id field =="
# need a drive for job
PIDR=$("${CURL[@]}" -X POST "$API/v1/providers" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"name":"m","type":"minio","credentials":{"access_key":"a","secret_key":"bsecretxx","endpoint":"http://127.0.0.1:9000"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
DID=$("${CURL[@]}" -X POST "$API/v1/drives" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"name\":\"d\",\"provider_id\":\"$PIDR\",\"bucket\":\"b\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
JRESP=$("${CURL[@]}" -X POST "$API/v1/jobs" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"command\":[\"echo\",\"hi\"],\"connector_id\":\"$CID\",\"note\":\"seed\"}")
J=$(echo "$JRESP" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("connector_id")=="'"$CID"'"; print(d["id"])')
echo "$JRESP" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert "BYOC only" in (d.get("note") or ""); print("job create note ok")'
# complete with clone path note → must append, not replace
"${CURL[@]}" -X POST "$API/v1/jobs/$J/claim" "${AUTH[@]}" >/dev/null
"${CURL[@]}" -X POST "$API/v1/jobs/$J/complete" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"ok":true,"note":"cloned to /workspace/repo"}' | python3 -c '
import sys,json
d=json.load(sys.stdin)
n=d.get("note") or ""
assert "BYOC only" in n, d
assert "cloned to /workspace/repo" in n, d
assert "seed" in n, d
print("job complete append clone path ok")
'
echo "job connector ok $J"

echo "OK smoke-stage-c"
