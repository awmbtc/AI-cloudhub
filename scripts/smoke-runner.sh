#!/usr/bin/env bash
# runner check + dry-run (no claim). See docs/RUNNER.md
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
go build -o .bin/runner ./cmd/runner
go build -o .bin/api ./cmd/api

echo "== runner check =="
set +e
./.bin/runner check > /tmp/aihub-runner-check.json
CHECK_RC=$?
set -e
python3 -c '
import json
d=json.load(open("/tmp/aihub-runner-check.json"))
assert d.get("component")=="runner", d
assert "rclone_ok" in d and "byoc_note" in d, d
assert "D-001" in (d.get("byoc_note") or "") or "pool" in (d.get("byoc_note") or "").lower(), d
print("check json ok rclone_ok=", d.get("rclone_ok"), "exit", '"$CHECK_RC"')
'

DB=$(mktemp /tmp/aihub-runner.XXXXXX)
STATE=$(mktemp -d /tmp/aihub-runner-state.XXXXXX)
HTTP_ADDR=":${API_PORT}" AI_CLOUDHUB_DB="$DB" JWT_SECRET="${JWT_SECRET:-runner-smoke-jwt-secretxx}" \
  ./.bin/api >/tmp/aihub-runner-api.log 2>&1 &
APID=$!
cleanup() { kill "$APID" 2>/dev/null || true; rm -f "$DB" "${DB}-wal" "${DB}-shm"; rm -rf "$STATE"; }
trap cleanup EXIT

for _ in $(seq 1 50); do
  "${CURL[@]}" "$API/healthz" >/dev/null 2>&1 && break
  sleep 0.1
done

echo "== register / provider / drive / agent / pending job =="
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"runneruser","password":"runnerpassxx"}' >/dev/null || true
TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"runneruser","password":"runnerpassxx"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
AUTH=(-H "Authorization: Bearer $TOK")
PID=$("${CURL[@]}" -X POST "$API/v1/providers" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"name":"m","type":"minio","credentials":{"access_key":"minioadmin","secret_key":"minioadmin","endpoint":"http://127.0.0.1:9000"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
DID=$("${CURL[@]}" -X POST "$API/v1/drives" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"name\":\"ws\",\"provider_id\":\"$PID\",\"bucket\":\"runner-bucket\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
AID=$("${CURL[@]}" -X POST "$API/v1/agents" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"name\":\"rbot\",\"default_scopes\":[\"drive.read\",\"drive.write\",\"job.run\",\"provider.read\"],\"allowed_drive_ids\":[\"$DID\"]}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
ATOK=$("${CURL[@]}" -X POST "$API/v1/agents/$AID/token" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
JID=$("${CURL[@]}" -X POST "$API/v1/jobs" -H "Authorization: Bearer $ATOK" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"command\":[\"echo\",\"runner-dry\"],\"mode\":\"sync_workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "== runner dry-run (must not claim) =="
export AI_CLOUDHUB_API="$API"
export AI_CLOUDHUB_TOKEN="$ATOK"
export AI_CLOUDHUB_DRIVE_ID="$DID"
export AI_CLOUDHUB_STATE="$STATE"
export AI_CLOUDHUB_MODE=sync_workspace
./.bin/runner dry-run > /tmp/aihub-runner-dry.json
python3 -c '
import json,os
d=json.load(open("/tmp/aihub-runner-dry.json"))
assert d.get("mode")=="dry-run", d
assert d.get("pending_count",0)>=1, d
ids=[j.get("id") for j in d.get("pending_jobs") or []]
assert "'"$JID"'" in ids, (ids, d)
assert d.get("conf_path") and os.path.isfile(d["conf_path"]), d
assert os.path.getsize(d["conf_path"])>0
print("dry-run ok pending", d["pending_count"], "job", "'"$JID"'", "conf", d["conf_path"])
'

# still pending (not claimed)
ST=$("${CURL[@]}" "$API/v1/jobs/$JID" -H "Authorization: Bearer $ATOK" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin).get("status",""))')
test "$ST" = "pending" -o "$ST" = "dispatched" || {
  echo "job should still be claimable, got status=$ST" >&2
  exit 1
}
echo "job still pending/dispatched ok status=$ST"

echo "OK smoke-runner job=$JID drive=$DID"
