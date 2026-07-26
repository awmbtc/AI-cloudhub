#!/usr/bin/env bash
# Golden path regression (D-003 / docs/GOLDEN-PATH.md).
# One narrative: auth → provider → drive → binding/session → agent → BYOC job → health.
# No live MinIO required. Does not deepen Job ops — exercises the product spine only.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
if [[ -z "${API_PORT:-}" ]]; then
  API_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
fi
API="http://127.0.0.1:${API_PORT}"
USER="${USER_NAME:-golden}"
PASS="${PASS:-goldenpassxx}"
export CGO_ENABLED=0
export NO_PROXY="127.0.0.1,localhost,::1"
export no_proxy="$NO_PROXY"
CURL=(curl -sS --noproxy '*')

cd "$ROOT"
mkdir -p .bin
go build -o .bin/api ./cmd/api

DB=$(mktemp /tmp/aihub-golden-XXXXXX.db)
HTTP_ADDR=":${API_PORT}" AI_CLOUDHUB_DB="$DB" JWT_SECRET="${JWT_SECRET:-golden-smoke-jwt-secretxx}" \
  ./.bin/api >/tmp/aihub-golden-api.log 2>&1 &
APID=$!
cleanup() { kill "$APID" 2>/dev/null || true; rm -f "$DB" "${DB}-wal" "${DB}-shm"; }
trap cleanup EXIT

for _ in $(seq 1 50); do
  if "${CURL[@]}" "$API/healthz" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$APID" 2>/dev/null; then
    echo "API exited early:" >&2
    cat /tmp/aihub-golden-api.log >&2 || true
    exit 1
  fi
  sleep 0.1
done

echo "== ① healthz / readyz =="
VER=$("${CURL[@]}" "$API/healthz" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("status")=="ok", d; print(d.get("version",""))')
"${CURL[@]}" "$API/readyz" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("status")=="ready", d; print("readyz ok")'

echo "== ② register / login =="
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" >/dev/null || true
TOKEN=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== ③ provider + drive =="
PID=$("${CURL[@]}" -X POST "$API/v1/providers" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"golden-minio","type":"minio","credentials":{"access_key":"minioadmin","secret_key":"minioadmin","endpoint":"http://127.0.0.1:9000"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
DID=$("${CURL[@]}" -X POST "$API/v1/drives" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"ws\",\"provider_id\":\"$PID\",\"bucket\":\"golden-bucket\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "== ④ binding + session (STS contract) =="
BID=$("${CURL[@]}" -X POST "$API/v1/bindings" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"device_id\":\"golden-device\",\"mount_point\":\"/workspace\",\"desired\":\"mounted\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
"${CURL[@]}" -X POST "$API/v1/bindings/$BID/session" \
  -H "Authorization: Bearer $TOKEN" | python3 -c '
import sys,json
d=json.load(sys.stdin)
sess=d.get("session") or d
man=d.get("manifest") or sess.get("manifest") or {}
spec=d.get("spec") or sess.get("spec") or {}
env=(man.get("env") or {})
ws=env.get("AI_CLOUDHUB_WORKSPACE") or man.get("workspace") or ""
assert ws, ("missing workspace", d)
print("session workspace", ws)
print("remote", (spec.get("remote_path") or "")[:80])
'
"${CURL[@]}" -X POST "$API/v1/bindings/$BID/report" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"actual":"mounted"}' >/dev/null

echo "== ⑤ agent token (scopes + drive allowlist) =="
AID=$("${CURL[@]}" -X POST "$API/v1/agents" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"golden-agent\",\"default_scopes\":[\"drive.read\",\"drive.write\",\"job.run\",\"provider.read\"],\"allowed_drive_ids\":[\"$DID\"]}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
ATOK=$("${CURL[@]}" -X POST "$API/v1/agents/$AID/token" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== ⑥ BYOC job create → claim → complete =="
JID=$("${CURL[@]}" -X POST "$API/v1/jobs" \
  -H "Authorization: Bearer $ATOK" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"command\":[\"echo\",\"golden-path\"],\"mode\":\"sync_workspace\"}" \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("id"), d; print(d["id"])')
CLAIMED=$("${CURL[@]}" -X POST "$API/v1/jobs/next/claim" \
  -H "Authorization: Bearer $ATOK" \
  -H 'X-AI-Cloudhub-Runner-Id: golden-runner' \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("status")=="running", d; print(d["id"])')
test "$CLAIMED" = "$JID"
"${CURL[@]}" -X POST "$API/v1/jobs/$JID/complete" \
  -H "Authorization: Bearer $ATOK" -H 'Content-Type: application/json' \
  -d '{"ok":true,"exit_code":0,"stdout":"golden-path\n"}' \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("status")=="succeeded", d; print("job", d["id"], "status", d["status"])'

echo "== ⑦ me (human) =="
"${CURL[@]}" "$API/v1/me" -H "Authorization: Bearer $TOKEN" \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("username") or d.get("id"), d; print("user", d.get("username") or d.get("id"))'

echo "OK golden-path version=$VER drive=$DID binding=$BID agent=$AID job=$JID"
echo "See docs/GOLDEN-PATH.md · D-003 job ops freeze"
