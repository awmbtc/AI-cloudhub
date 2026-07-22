#!/usr/bin/env bash
# Objects API smoke: version-hint / restore-plan / presign-get (+ optional soft live MinIO).
# Self-starts API. Does not require a live object store for the core path.
# For hard-assert inventory + snapshot include_objects with auto MinIO: make smoke-minio
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API_PORT="${API_PORT:-18150}"
API="http://127.0.0.1:${API_PORT}"
export CGO_ENABLED=0
export NO_PROXY="127.0.0.1,localhost,::1"
export no_proxy="$NO_PROXY"
CURL=(curl -sS --noproxy '*')

USER="${USER_NAME:-objuser}"
PASS="${PASS:-objpassxx}"
# When set (1), expect list + restore-version against a real MinIO at MINIO_ENDPOINT.
LIVE="${AI_CLOUDHUB_SMOKE_MINIO:-0}"
MINIO_EP="${MINIO_ENDPOINT:-http://127.0.0.1:9000}"
MINIO_AK="${MINIO_ACCESS_KEY:-minioadmin}"
MINIO_SK="${MINIO_SECRET_KEY:-minioadmin}"
MINIO_BUCKET="${MINIO_BUCKET:-testbucket}"

cd "$ROOT"
mkdir -p .bin
go build -o .bin/api ./cmd/api

DB=$(mktemp /tmp/aihub-objects-XXXXXX.db)
HTTP_ADDR=":${API_PORT}" AI_CLOUDHUB_DB="$DB" JWT_SECRET="${JWT_SECRET:-objects-smoke-jwt}" \
  ./.bin/api >/tmp/aihub-objects-api.log 2>&1 &
APID=$!
cleanup() { kill "$APID" 2>/dev/null || true; rm -f "$DB" "${DB}-wal" "${DB}-shm"; }
trap cleanup EXIT

for _ in $(seq 1 40); do
  "${CURL[@]}" "$API/healthz" >/dev/null 2>&1 && break
  sleep 0.1
done
"${CURL[@]}" "$API/healthz" >/dev/null

echo "== register/login =="
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" >/dev/null || true
TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== provider + drive (prefix=ws) =="
PID=$("${CURL[@]}" -X POST "$API/v1/providers" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"m\",\"type\":\"minio\",\"credentials\":{\"access_key\":\"$MINIO_AK\",\"secret_key\":\"$MINIO_SK\",\"endpoint\":\"$MINIO_EP\",\"region\":\"us-east-1\"}}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
DID=$("${CURL[@]}" -X POST "$API/v1/drives" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"obj-drive\",\"provider_id\":\"$PID\",\"bucket\":\"$MINIO_BUCKET\",\"prefix\":\"ws\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
echo "drive_id=$DID"

echo "== version-hint (no S3 body) =="
HINT=$("${CURL[@]}" -X POST "$API/v1/drives/$DID/objects/version-hint" \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"key":"file.bin","version_id":"v-smoke-1"}')
echo "$HINT" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d.get("key")=="ws/file.bin", d
assert "v-smoke-1" in (d.get("hint") or ""), d
print("hint ok", d["key"])
'

echo "== restore-plan =="
PLAN=$("${CURL[@]}" -X POST "$API/v1/drives/$DID/objects/restore-plan" \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"key":"file.bin","version_id":"v-smoke-1","ttl_min":10}')
echo "$PLAN" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert "api_restore" in d and "cli_hint" in d, d
assert d.get("key")=="ws/file.bin", d
# presign is local signing — should succeed without live MinIO
if d.get("presign"):
    assert "url" in d["presign"], d
    print("plan ok (presign present)")
else:
    print("plan ok (presign_error=%s)" % d.get("presign_error"))
'

echo "== presign-get =="
PS=$("${CURL[@]}" -X POST "$API/v1/drives/$DID/objects/presign-get" \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"key":"nested/x","version_id":"vid99","ttl_min":5}')
echo "$PS" | python3 -c '
import sys,json
d=json.load(sys.stdin)
assert d.get("key")=="ws/nested/x", d
assert "url" in d and d["url"], d
assert "vid99" in d["url"] or d.get("version_id")=="vid99", d
print("presign ok expires_in=", d.get("expires_in"))
'

echo "== restore-version missing fields =="
CODE=$("${CURL[@]}" -o /dev/null -w '%{http_code}' -X POST \
  "$API/v1/drives/$DID/objects/restore-version" \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"key":"only"}')
test "$CODE" = "400"

echo "== agent read scope: restore-version forbidden =="
AID=$("${CURL[@]}" -X POST "$API/v1/agents" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"name":"objbot","default_scopes":["drive.read"]}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
ATOK=$("${CURL[@]}" -X POST "$API/v1/agents/$AID/token" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
CODE=$("${CURL[@]}" -o /dev/null -w '%{http_code}' -X POST \
  "$API/v1/drives/$DID/objects/restore-version" \
  -H "Authorization: Bearer $ATOK" -H 'Content-Type: application/json' \
  -d '{"key":"file.bin","version_id":"v1"}')
test "$CODE" = "403"
CODE=$("${CURL[@]}" -o /dev/null -w '%{http_code}' -X POST \
  "$API/v1/drives/$DID/objects/presign-get" \
  -H "Authorization: Bearer $ATOK" -H 'Content-Type: application/json' \
  -d '{"key":"file.bin"}')
test "$CODE" = "200"

if [[ "$LIVE" == "1" ]]; then
  # Soft live path (expects pre-existing MinIO at MINIO_ENDPOINT; does not seed).
  # Hard-assert inventory + include_objects + auto-start: scripts/smoke-minio-inventory.sh
  echo "== LIVE MinIO list objects (soft; use make smoke-minio for hard-assert) =="
  INV=$("${CURL[@]}" "$API/v1/drives/$DID/objects?max=50" -H "Authorization: Bearer $TOK")
  echo "$INV" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert "entries" in d, d; print("count", d.get("count"))'
  echo "== LIVE restore-version (expect 502 if no such version; must not 403) =="
  CODE=$("${CURL[@]}" -o /tmp/aihub-restore.json -w '%{http_code}' -X POST \
    "$API/v1/drives/$DID/objects/restore-version" \
    -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
    -d '{"key":"file.bin","version_id":"no-such-version-smoke"}')
  # 200 only if version exists; otherwise 502 from store
  test "$CODE" = "200" -o "$CODE" = "502"
  echo "restore-version HTTP $CODE"
else
  echo "== skip LIVE MinIO (soft: AI_CLOUDHUB_SMOKE_MINIO=1; hard-assert: make smoke-minio) =="
fi

echo "OK objects smoke drive=$DID"
