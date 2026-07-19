#!/usr/bin/env bash
# Batch A smoke: catalog → provider (minio) → drive → mount config
set -euo pipefail
API="${API:-http://127.0.0.1:8080}"
USER="${USER_NAME:-driveuser}"
PASS="${PASS:-drivepass}"

echo "== health =="
curl -sS "$API/healthz"
echo

echo "== catalog =="
curl -sS "$API/v1/providers/catalog" | python3 -m json.tool | head -40
echo

echo "== register/login =="
curl -sS -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" >/dev/null || true
TOKEN=$(curl -sS -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== create minio provider =="
PROV=$(curl -sS -X POST "$API/v1/providers" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "local-minio",
    "type": "minio",
    "credentials": {
      "access_key": "minioadmin",
      "secret_key": "minioadmin",
      "endpoint": "http://127.0.0.1:9000",
      "region": "us-east-1"
    }
  }')
echo "$PROV" | python3 -m json.tool
PID=$(echo "$PROV" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "== create drive map G: or /workspace =="
DRV=$(curl -sS -X POST "$API/v1/drives" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"agent-disk\",\"provider_id\":\"$PID\",\"bucket\":\"testbucket\",\"mount_point\":\"G:\"}")
echo "$DRV" | python3 -m json.tool
DID=$(echo "$DRV" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "== mount bundle (rclone) =="
curl -sS "$API/v1/drives/$DID/mount" \
  -H "Authorization: Bearer $TOKEN" | python3 -m json.tool

echo "OK drive_id=$DID"
