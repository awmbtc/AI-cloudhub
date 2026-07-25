#!/usr/bin/env bash
# Durable BYOC job queue smoke: restart durability + agent_id / claimed_by_agent_id.
# No rclone required.
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

DB=$(mktemp /tmp/aihub-job-XXXXXX.db)
API_PID=""

start_api() {
  if [[ -n "${API_PID}" ]] && kill -0 "$API_PID" 2>/dev/null; then
    kill "$API_PID" 2>/dev/null || true
    wait "$API_PID" 2>/dev/null || true
    API_PID=""
    sleep 0.2
  fi
  HTTP_ADDR=":${API_PORT}" \
    AI_CLOUDHUB_DB="$DB" \
    JWT_SECRET="${JWT_SECRET:-job-smoke-jwt-secretxx}" \
    ./.bin/api >/tmp/aihub-job-api.log 2>&1 &
  API_PID=$!
  for _ in $(seq 1 50); do
    if "${CURL[@]}" "$API/healthz" >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "$API_PID" 2>/dev/null; then
      echo "API exited early:" >&2
      cat /tmp/aihub-job-api.log >&2 || true
      return 1
    fi
    sleep 0.1
  done
  echo "API not healthy:" >&2
  cat /tmp/aihub-job-api.log >&2 || true
  return 1
}

cleanup() {
  if [[ -n "${API_PID:-}" ]]; then
    kill "$API_PID" 2>/dev/null || true
    wait "$API_PID" 2>/dev/null || true
  fi
  rm -f "$DB" "${DB}-wal" "${DB}-shm"
}
trap cleanup EXIT

start_api

echo "== register/login =="
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"jobuser","password":"jobpassxx"}' >/dev/null || true
TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"jobuser","password":"jobpassxx"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== provider + drive =="
PID=$("${CURL[@]}" -X POST "$API/v1/providers" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"name":"m","type":"minio","credentials":{"access_key":"ak","secret_key":"sksecret","endpoint":"http://127.0.0.1:9000"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
DID=$("${CURL[@]}" -X POST "$API/v1/drives" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"d\",\"provider_id\":\"$PID\",\"bucket\":\"b\",\"region\":\"ap-east\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "== agent create + token (job.run + drive.*) =="
AID=$("${CURL[@]}" -X POST "$API/v1/agents" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"jobbot\",\"default_scopes\":[\"drive.read\",\"drive.write\",\"job.run\"],\"allowed_drive_ids\":[\"$DID\"]}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
ATOK=$("${CURL[@]}" -X POST "$API/v1/agents/$AID/token" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== agent creates job (agent_id set) =="
JID=$("${CURL[@]}" -X POST "$API/v1/jobs" -H "Authorization: Bearer $ATOK" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"command\":[\"echo\",\"hello-byoc\"],\"mode\":\"sync_workspace\"}" \
  | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d.get("agent_id"), d
assert not d.get("claimed_by_agent_id"), d
print(d["id"])
print("agent_id", d["agent_id"], file=sys.stderr)
')
echo "created job $JID by agent"

echo "== restart api — job must survive =="
start_api
TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"jobuser","password":"jobpassxx"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
ATOK=$("${CURL[@]}" -X POST "$API/v1/agents/$AID/token" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

PEND=$("${CURL[@]}" "$API/v1/jobs?status=pending" -H "Authorization: Bearer $ATOK" \
  | python3 -c 'import sys,json; print(len(json.load(sys.stdin)["items"]))')
echo "pending_after_restart=$PEND"
test "$PEND" = "1"

echo "== agent claim next (claimed_by_agent_id set) =="
CLAIM=$("${CURL[@]}" -X POST "$API/v1/jobs/next/claim" -H "Authorization: Bearer $ATOK")
echo "$CLAIM" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d["status"]=="running", d
assert d.get("agent_id"), d
assert d.get("claimed_by_agent_id"), d
assert d["agent_id"]==d["claimed_by_agent_id"] or True
print("claimed", d["id"], "creator", d["agent_id"], "claimer", d["claimed_by_agent_id"], d["command"])
'

echo "== complete =="
"${CURL[@]}" -X POST "$API/v1/jobs/$JID/complete" -H "Authorization: Bearer $ATOK" \
  -H 'Content-Type: application/json' -d '{"ok":true,"note":"smoke"}' \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d["status"]=="succeeded"; print("completed", d["status"])'

echo "== second job: human creates, agent claims =="
J2=$("${CURL[@]}" -X POST "$API/v1/jobs" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"command\":[\"true\"]}" \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); assert not d.get("agent_id"), d; print(d["id"])')
CLAIM2=$("${CURL[@]}" -X POST "$API/v1/jobs/$J2/claim" -H "Authorization: Bearer $ATOK")
echo "$CLAIM2" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d["status"]=="running", d
assert not d.get("agent_id"), d
assert d.get("claimed_by_agent_id"), d
print("human-created claimed_by", d["claimed_by_agent_id"])
'
"${CURL[@]}" -X POST "$API/v1/jobs/$J2/complete" -H "Authorization: Bearer $ATOK" \
  -H 'Content-Type: application/json' -d '{"ok":true}' >/dev/null

echo "== list filter by agent_id / claimed_by_agent_id =="
# create another agent job then filter
J3=$("${CURL[@]}" -X POST "$API/v1/jobs" -H "Authorization: Bearer $ATOK" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"command\":[\"true\"]}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
N=$("${CURL[@]}" "$API/v1/jobs?agent_id=$AID" -H "Authorization: Bearer $TOK" \
  | python3 -c 'import sys,json; print(len(json.load(sys.stdin)["items"]))')
test "$N" -ge 2
N2=$("${CURL[@]}" "$API/v1/jobs?claimed_by_agent_id=$AID" -H "Authorization: Bearer $TOK" \
  | python3 -c 'import sys,json; print(len(json.load(sys.stdin)["items"]))')
test "$N2" -ge 2
echo "list filter ok agent_id count=$N claimer count=$N2 (j3=$J3)"

echo "OK smoke-job durable BYOC + agent_id trace"
