#!/usr/bin/env bash
# hubd check + dry-run regression (FUSE not required; rclone optional for dry-run).
# See docs/HUBD.md
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
DEVICE="smoke-hubd-device"

cd "$ROOT"
mkdir -p .bin
go build -o .bin/hubd ./cmd/hubd
go build -o .bin/api ./cmd/api

echo "== hubd check =="
set +e
./.bin/hubd check > /tmp/aihub-hubd-check.json
CHECK_RC=$?
set -e
python3 -c '
import json,sys
d=json.load(open("/tmp/aihub-hubd-check.json"))
assert "rclone_ok" in d and "os" in d, d
print("check json ok rclone_ok=", d.get("rclone_ok"), "exit", '"$CHECK_RC"')
'
if [[ "$CHECK_RC" -ne 0 && "${AI_CLOUDHUB_SMOKE_HUBD_REQUIRE:-0}" == "1" ]]; then
  echo "FAIL: hubd check required rclone" >&2
  exit 1
fi

DB=$(mktemp /tmp/aihub-hubd.XXXXXX)
STATE=$(mktemp -d /tmp/aihub-hubd-state.XXXXXX)
HTTP_ADDR=":${API_PORT}" AI_CLOUDHUB_DB="$DB" JWT_SECRET="${JWT_SECRET:-hubd-smoke-jwt-secretxx}" \
  ./.bin/api >/tmp/aihub-hubd-api.log 2>&1 &
APID=$!
cleanup() { kill "$APID" 2>/dev/null || true; rm -f "$DB" "${DB}-wal" "${DB}-shm"; rm -rf "$STATE"; }
trap cleanup EXIT

for _ in $(seq 1 50); do
  "${CURL[@]}" "$API/healthz" >/dev/null 2>&1 && break
  sleep 0.1
done

echo "== register / provider / drive / binding =="
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"hubduser","password":"hubdpassxx"}' >/dev/null || true
TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"hubduser","password":"hubdpassxx"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
AUTH=(-H "Authorization: Bearer $TOK")
PID=$("${CURL[@]}" -X POST "$API/v1/providers" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"name":"m","type":"minio","credentials":{"access_key":"minioadmin","secret_key":"minioadmin","endpoint":"http://127.0.0.1:9000"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
DID=$("${CURL[@]}" -X POST "$API/v1/drives" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"name\":\"ws\",\"provider_id\":\"$PID\",\"bucket\":\"hubd-bucket\",\"mount_point\":\"/tmp/aihub-hubd-ws-smoke\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
BID=$("${CURL[@]}" -X POST "$API/v1/bindings" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"device_id\":\"$DEVICE\",\"mount_point\":\"/tmp/aihub-hubd-ws-smoke\",\"desired\":\"mounted\",\"mode\":\"sync_workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "== hubd dry-run (no rclone mount) =="
export AI_CLOUDHUB_API="$API"
export AI_CLOUDHUB_TOKEN="$TOK"
export AI_CLOUDHUB_DEVICE_ID="$DEVICE"
export AI_CLOUDHUB_STATE="$STATE"
./.bin/hubd dry-run > /tmp/aihub-hubd-dry.json
python3 -c '
import json,os
d=json.load(open("/tmp/aihub-hubd-dry.json"))
assert d.get("mode")=="dry-run", d
assert d.get("ok_count",0)>=1, d
b=d["bindings"][0]
assert b.get("conf_path") and os.path.isfile(b["conf_path"]), b
assert os.path.getsize(b["conf_path"])>0
assert b.get("workspace") or b.get("mount_point"), b
print("dry-run ok binding", b.get("binding_id"), "source", b.get("session_source"), "conf", b["conf_path"])
'

echo "OK smoke-hubd binding=$BID drive=$DID"
