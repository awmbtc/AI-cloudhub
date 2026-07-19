#!/usr/bin/env bash
# Durable BYOC job queue smoke (no rclone required)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API_PORT="${API_PORT:-18082}"
API="http://127.0.0.1:${API_PORT}"
export CGO_ENABLED=0
export PATH="${PATH}:/tmp/go1.22.10/go/bin"

cd "$ROOT"
go build -o .bin/api ./cmd/api

DB=$(mktemp /tmp/aihub-job-XXXXXX.db)
HTTP_ADDR=":${API_PORT}" AI_CLOUDHUB_DB="$DB" ./.bin/api > /tmp/aihub-job-api.log 2>&1 &
APID=$!
cleanup() { kill "$APID" 2>/dev/null || true; rm -f "$DB"; }
trap cleanup EXIT
sleep 0.6

curl -sS -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"jobuser","password":"jobpassx"}' >/dev/null || true
TOKEN=$(curl -sS -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"jobuser","password":"jobpassx"}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

PID=$(curl -sS -X POST "$API/v1/providers" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"m","type":"minio","credentials":{"access_key":"a","secret_key":"b","endpoint":"http://127.0.0.1:9000"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
DID=$(curl -sS -X POST "$API/v1/drives" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"d\",\"provider_id\":\"$PID\",\"bucket\":\"b\",\"region\":\"ap-east\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

JID=$(curl -sS -X POST "$API/v1/jobs" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"command\":[\"echo\",\"hello-byoc\"],\"mode\":\"sync_workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
echo "created job $JID"

# restart api — job must survive
kill "$APID"; sleep 0.3
HTTP_ADDR=":${API_PORT}" AI_CLOUDHUB_DB="$DB" ./.bin/api > /tmp/aihub-job-api.log 2>&1 &
APID=$!
sleep 0.5
TOKEN=$(curl -sS -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"jobuser","password":"jobpassx"}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

PEND=$(curl -sS "$API/v1/jobs?status=pending" -H "Authorization: Bearer $TOKEN" \
  | python3 -c 'import sys,json; print(len(json.load(sys.stdin)["items"]))')
echo "pending_after_restart=$PEND"
test "$PEND" = "1"

CLAIM=$(curl -sS -X POST "$API/v1/jobs/next/claim" -H "Authorization: Bearer $TOKEN")
echo "$CLAIM" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="running"; print("claimed", d["id"], d["command"])'

curl -sS -X POST "$API/v1/jobs/$JID/complete" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"ok":true,"note":"smoke"}' | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="succeeded"; print("completed", d["status"])'

echo "OK smoke-job durable BYOC queue"
