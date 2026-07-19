#!/usr/bin/env bash
# P0 smoke: provider → drive → binding → STS session (self-starts API)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API_PORT="${API_PORT:-18080}"
API="http://127.0.0.1:${API_PORT}"
USER="${USER_NAME:-p0user}"
PASS="${PASS:-p0passxx}"
export CGO_ENABLED=0
export PATH="${PATH}:/tmp/go1.22.10/go/bin"
# Avoid HTTP proxies hijacking localhost API calls.
export NO_PROXY="127.0.0.1,localhost,::1"
export no_proxy="$NO_PROXY"
CURL=(curl -sS --noproxy '*')

cd "$ROOT"
go build -o .bin/api ./cmd/api
DB=$(mktemp /tmp/aihub-p0-XXXXXX.db)
HTTP_ADDR=":${API_PORT}" AI_CLOUDHUB_DB="$DB" ./.bin/api > /tmp/aihub-p0-api.log 2>&1 &
APID=$!
cleanup() { kill "$APID" 2>/dev/null || true; rm -f "$DB"; }
trap cleanup EXIT
# Wait until API accepts connections (cold start / sqlite migrate).
for i in 1 2 3 4 5 6 7 8 9 10; do
  if "${CURL[@]}" "$API/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.3
done

echo "== health =="
"${CURL[@]}" "$API/healthz" | python3 -m json.tool | head -20

echo "== register/login =="
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" >/dev/null || true
TOKEN=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== provider minio =="
PID=$("${CURL[@]}" -X POST "$API/v1/providers" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"m","type":"minio","credentials":{"access_key":"minioadmin","secret_key":"minioadmin","endpoint":"http://127.0.0.1:9000"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "== drive =="
DID=$("${CURL[@]}" -X POST "$API/v1/drives" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"ws\",\"provider_id\":\"$PID\",\"bucket\":\"test\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "== binding desired=mounted =="
BID=$("${CURL[@]}" -X POST "$API/v1/bindings" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"device_id\":\"smoke-device\",\"mount_point\":\"/workspace\",\"desired\":\"mounted\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "== STS session for binding =="
"${CURL[@]}" -X POST "$API/v1/bindings/$BID/session" \
  -H "Authorization: Bearer $TOKEN" | python3 -c '
import sys,json
d=json.load(sys.stdin)
spec=d.get("spec") or d["session"]["spec"]
man=d.get("manifest") or d["session"]["manifest"]
print("expires", d["session"]["expires_at"])
print("workspace", man["env"]["AI_CLOUDHUB_WORKSPACE"])
print("remote", spec["remote_path"])
print("conf_bytes", len(spec.get("rclone_conf") or ""))
'

echo "== report actual =="
"${CURL[@]}" -X POST "$API/v1/bindings/$BID/report" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"actual":"mounted"}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["actual"])'

echo "OK drive=$DID binding=$BID"
