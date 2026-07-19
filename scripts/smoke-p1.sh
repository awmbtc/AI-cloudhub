#!/usr/bin/env bash
# P1 smoke: SQLite persist + secretbox + restart + barrier + devices
# Manages its own .bin/api process (temp DB + master key).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API_BIN="${API_BIN:-$ROOT/.bin/api}"
HTTP_ADDR="${HTTP_ADDR:-:18081}"
API="${API:-http://127.0.0.1:18081}"
USER="${USER_NAME:-p1user}"
PASS="${PASS:-p1passxx}"
DEVICE_ID="${DEVICE_ID:-p1-device}"

if [[ ! -x "$API_BIN" ]]; then
  echo "== build .bin/api (CGO_ENABLED=0) =="
  mkdir -p "$ROOT/.bin"
  (cd "$ROOT" && CGO_ENABLED=0 go build -o "$API_BIN" ./cmd/api)
fi

# BSD mktemp requires trailing X's; append .db after.
DB_FILE="$(mktemp "${TMPDIR:-/tmp}/ai-cloudhub-p1.XXXXXX").db"
# shellcheck disable=SC2064
trap '[[ -n "${API_PID:-}" ]] && kill "$API_PID" 2>/dev/null || true; wait "$API_PID" 2>/dev/null || true; rm -f "$DB_FILE" "${DB_FILE}-wal" "${DB_FILE}-shm"' EXIT

export AI_CLOUDHUB_DB="$DB_FILE"
export AI_CLOUDHUB_MASTER_KEY
AI_CLOUDHUB_MASTER_KEY="$(openssl rand -base64 32)"
export HTTP_ADDR
export JWT_SECRET="${JWT_SECRET:-p1-smoke-jwt}"

API_PID=""

start_api() {
  if [[ -n "${API_PID}" ]] && kill -0 "$API_PID" 2>/dev/null; then
    kill "$API_PID" 2>/dev/null || true
    wait "$API_PID" 2>/dev/null || true
    API_PID=""
  fi
  # brief pause so port is released
  sleep 0.2
  AI_CLOUDHUB_DB="$DB_FILE" \
  AI_CLOUDHUB_MASTER_KEY="$AI_CLOUDHUB_MASTER_KEY" \
  HTTP_ADDR="$HTTP_ADDR" \
  JWT_SECRET="$JWT_SECRET" \
  "$API_BIN" >/tmp/ai-cloudhub-p1-api.log 2>&1 &
  API_PID=$!
  for _ in $(seq 1 50); do
    if curl -sf "$API/healthz" >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "$API_PID" 2>/dev/null; then
      echo "API exited early; log:" >&2
      cat /tmp/ai-cloudhub-p1-api.log >&2 || true
      return 1
    fi
    sleep 0.1
  done
  echo "API failed to become healthy; log:" >&2
  cat /tmp/ai-cloudhub-p1-api.log >&2 || true
  return 1
}

echo "== start api (db=$DB_FILE master_key=set) =="
start_api

echo "== health =="
curl -sS "$API/healthz" | python3 -m json.tool

echo "== register/login =="
curl -sS -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" | python3 -m json.tool
TOKEN=$(curl -sS -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== create minio provider (encrypted at rest) =="
PID=$(curl -sS -X POST "$API/v1/providers" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"m","type":"minio","credentials":{"access_key":"minioadmin","secret_key":"minioadmin","endpoint":"http://127.0.0.1:9000","region":"us-east-1"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
echo "provider_id=$PID"

echo "== restart api with same db+key =="
start_api

echo "== re-login after restart =="
TOKEN=$(curl -sS -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== list providers (expect count=1) =="
COUNT=$(curl -sS "$API/v1/providers" -H "Authorization: Bearer $TOKEN" \
  | python3 -c 'import sys,json; print(len(json.load(sys.stdin)["items"]))')
echo "providers_count=$COUNT"
if [[ "$COUNT" != "1" ]]; then
  echo "FAIL: expected 1 provider after restart, got $COUNT" >&2
  exit 1
fi

echo "== create drive + binding + session =="
DID=$(curl -sS -X POST "$API/v1/drives" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"ws\",\"provider_id\":\"$PID\",\"bucket\":\"test\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
echo "drive_id=$DID"

BID=$(curl -sS -X POST "$API/v1/bindings" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"device_id\":\"$DEVICE_ID\",\"mount_point\":\"/workspace\",\"desired\":\"mounted\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
echo "binding_id=$BID"

curl -sS -X POST "$API/v1/bindings/$BID/session" \
  -H "Authorization: Bearer $TOKEN" | python3 -c '
import sys,json
d=json.load(sys.stdin)
print("session expires", d["session"]["expires_at"])
spec=d.get("spec") or d["session"]["spec"]
print("remote", spec.get("remote_path"))
print("conf_bytes", len(spec.get("rclone_conf") or ""))
'

echo "== post barrier =="
curl -sS -X POST "$API/v1/drives/$DID/barrier" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"device_id\":\"$DEVICE_ID\",\"status\":\"ok\",\"note\":\"smoke-p1\"}" | python3 -m json.tool

echo "== register device POST /v1/devices =="
curl -sS -X POST "$API/v1/devices" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"id\":\"$DEVICE_ID\",\"name\":\"smoke-laptop\"}" | python3 -m json.tool

echo "OK p1 smoke: provider=$PID drive=$DID binding=$BID providers_count=1"
