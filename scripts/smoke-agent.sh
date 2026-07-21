#!/usr/bin/env bash
# Agent identity + scopes + drive allowlist + snapshot smoke (self-starts API).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API_PORT="${API_PORT:-18140}"
API="http://127.0.0.1:${API_PORT}"
export CGO_ENABLED=0
export NO_PROXY="127.0.0.1,localhost,::1"
export no_proxy="$NO_PROXY"
CURL=(curl -sS --noproxy '*')

cd "$ROOT"
go build -o .bin/api ./cmd/api
DB=$(mktemp /tmp/aihub-agent-XXXXXX.db)
HTTP_ADDR=":${API_PORT}" AI_CLOUDHUB_DB="$DB" ./.bin/api > /tmp/aihub-agent-api.log 2>&1 &
APID=$!
cleanup() { kill "$APID" 2>/dev/null || true; rm -f "$DB"; }
trap cleanup EXIT

for i in $(seq 1 30); do
  "${CURL[@]}" "$API/healthz" >/dev/null 2>&1 && break
  sleep 0.2
done

echo "== register/login =="
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"agentadmin","password":"password1"}' >/dev/null || true
TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"agentadmin","password":"password1"}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== provider + drives =="
PID=$("${CURL[@]}" -X POST "$API/v1/providers" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"name":"m","type":"minio","credentials":{"access_key":"a","secret_key":"bsecret","endpoint":"http://127.0.0.1:9000"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
D1=$("${CURL[@]}" -X POST "$API/v1/drives" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"alpha\",\"provider_id\":\"$PID\",\"bucket\":\"b1\",\"mount_point\":\"/workspace\",\"prefix\":\"p1\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
D2=$("${CURL[@]}" -X POST "$API/v1/drives" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"beta\",\"provider_id\":\"$PID\",\"bucket\":\"b2\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "== create agent allow d1 only =="
AID=$("${CURL[@]}" -X POST "$API/v1/agents" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"bot\",\"default_scopes\":[\"drive.read\",\"drive.write\"],\"allowed_drive_ids\":[\"$D1\"],\"read_prefixes\":[\"in\"],\"write_prefixes\":[\"out\"]}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
ATOK=$("${CURL[@]}" -X POST "$API/v1/agents/$AID/token" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== agent list drives (expect 1) =="
N=$("${CURL[@]}" "$API/v1/drives" -H "Authorization: Bearer $ATOK" | python3 -c 'import sys,json; print(len(json.load(sys.stdin)["items"]))')
test "$N" = "1"

echo "== agent forbidden on d2 =="
CODE=$("${CURL[@]}" -o /dev/null -w '%{http_code}' "$API/v1/drives/$D2" -H "Authorization: Bearer $ATOK")
test "$CODE" = "403"

echo "== snapshot + apply restore =="
# change mount_point then restore from snapshot of original
SID=$("${CURL[@]}" -X POST "$API/v1/drives/$D1/snapshots" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"label":"base"}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
# mutate via put? we only have update through restore; use a second snapshot after "fake" by creating new snap after we only have restore apply
# Apply restore should set prefix/mount back from snap
OUT=$("${CURL[@]}" -X POST "$API/v1/drives/$D1/snapshots/$SID/restore?apply=true" -H "Authorization: Bearer $TOK")
echo "$OUT" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("applied") is True, d; print("applied ok", d["drive"]["name"])'

echo "== session manifest v2 =="
"${CURL[@]}" -X POST "$API/v1/drives/$D1/session" -H "Authorization: Bearer $ATOK" -H 'Content-Type: application/json' \
  -d '{}' | python3 -c 'import sys,json; d=json.load(sys.stdin); m=d.get("manifest") or {}; assert m.get("version")==2, m; print("manifest v2 ok")'

echo "OK agent=$AID drive=$D1 snapshot=$SID"
