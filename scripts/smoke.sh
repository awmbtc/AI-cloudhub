#!/usr/bin/env bash
set -euo pipefail
API="${API:-http://127.0.0.1:8080}"
USER="${USER_NAME:-demo}"
PASS="${PASS:-demo1234}"

echo "== register =="
curl -sS -X POST "$API/v1/auth/register" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" || true
echo

echo "== login =="
TOKEN=$(curl -sS -X POST "$API/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
echo "token ok"

echo "== create workspace =="
WS=$(curl -sS -X POST "$API/v1/workspaces" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"agent-ws"}')
echo "$WS"
WS_ID=$(echo "$WS" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "== put file via API =="
echo "hello from agent $(date)" | curl -sS -X PUT "$API/v1/workspaces/$WS_ID/files/notes/hello.txt" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: text/plain' \
  --data-binary @-
echo

echo "== list files =="
curl -sS "$API/v1/workspaces/$WS_ID/files?prefix=notes/" \
  -H "Authorization: Bearer $TOKEN"
echo

echo "== agent mount hint =="
curl -sS "$API/v1/workspaces/$WS_ID/agent-mount" \
  -H "Authorization: Bearer $TOKEN"
echo

echo "OK workspace_id=$WS_ID"
