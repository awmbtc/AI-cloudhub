#!/usr/bin/env bash
# Policy file smoke: external JSON deny + admin policy + agent job gate.
# Self-starts API with AI_CLOUDHUB_POLICY_FILE.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# Prefer free high port to avoid colliding with leftover .bin/api processes.
if [[ -z "${API_PORT:-}" ]]; then
  API_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
fi
API="http://127.0.0.1:${API_PORT}"
export CGO_ENABLED=0
export NO_PROXY="127.0.0.1,localhost,::1"
export no_proxy="$NO_PROXY"
CURL=(curl -sS --noproxy '*')

POLICY_FILE="$(mktemp /tmp/aihub-policy-XXXXXX.json)"
# Placeholder D2 filled after drives are created; restart API once with final policy.
cat >"$POLICY_FILE" <<'EOF'
{"version":1,"mode":"enforce","rules":[{"id":"deny-jobs-for-agents","effect":"deny","principals":["agent"],"actions":["job.run"],"reason":"jobs blocked by policy smoke"}]}
EOF

cd "$ROOT"
mkdir -p .bin
go build -o .bin/api ./cmd/api

DB=$(mktemp /tmp/aihub-policy-XXXXXX.db)
API_PID=""

start_api() {
  if [[ -n "${API_PID}" ]] && kill -0 "$API_PID" 2>/dev/null; then
    kill "$API_PID" 2>/dev/null || true
    wait "$API_PID" 2>/dev/null || true
    API_PID=""
    sleep 0.3
  fi
  HTTP_ADDR=":${API_PORT}" \
    AI_CLOUDHUB_DB="$DB" \
    AI_CLOUDHUB_POLICY_FILE="$POLICY_FILE" \
    JWT_SECRET="${JWT_SECRET:-policy-smoke-jwt-secretxx}" \
    ./.bin/api >/tmp/aihub-policy-api.log 2>&1 &
  API_PID=$!
  for _ in $(seq 1 50); do
    if "${CURL[@]}" "$API/healthz" >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "$API_PID" 2>/dev/null; then
      echo "API exited early:" >&2
      cat /tmp/aihub-policy-api.log >&2 || true
      return 1
    fi
    sleep 0.1
  done
  echo "API not healthy:" >&2
  cat /tmp/aihub-policy-api.log >&2 || true
  return 1
}

cleanup() {
  if [[ -n "${API_PID:-}" ]]; then
    kill "$API_PID" 2>/dev/null || true
    wait "$API_PID" 2>/dev/null || true
  fi
  rm -f "$DB" "${DB}-wal" "${DB}-shm" "$POLICY_FILE"
}
trap cleanup EXIT

start_api

echo "== register/login (admin) =="
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"poladmin","password":"password1"}' >/dev/null || true
LOGIN=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"poladmin","password":"password1"}')
TOK=$(echo "$LOGIN" | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== admin policy status =="
POL=$("${CURL[@]}" "$API/v1/admin/policy" -H "Authorization: Bearer $TOK")
echo "$POL" | python3 -c '
import sys,json
raw=sys.stdin.read()
try:
  d=json.loads(raw)
except Exception as e:
  print("raw=", repr(raw[:500]), file=sys.stderr)
  raise
st=d.get("status") or {}
assert st.get("loaded") is True, d
assert st.get("rule_count",0) >= 1, d
assert st.get("mode")=="enforce", d
print("admin policy ok rules=", st.get("rule_count"), "mode=", st.get("mode"))
'

echo "== provider + drives =="
PID=$("${CURL[@]}" -X POST "$API/v1/providers" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"name":"m","type":"minio","credentials":{"access_key":"a","secret_key":"bsecret","endpoint":"http://127.0.0.1:9000"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
D1=$("${CURL[@]}" -X POST "$API/v1/drives" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"alpha\",\"provider_id\":\"$PID\",\"bucket\":\"b1\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
D2=$("${CURL[@]}" -X POST "$API/v1/drives" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"beta\",\"provider_id\":\"$PID\",\"bucket\":\"b2\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "== rewrite policy with D2 deny + restart API =="
python3 - <<PY
import json
doc={
  "version": 1,
  "mode": "enforce",
  "rules": [
    {
      "id": "deny-jobs-for-agents",
      "effect": "deny",
      "principals": ["agent"],
      "actions": ["job.run"],
      "reason": "jobs blocked by policy smoke"
    },
    {
      "id": "deny-drive-d2",
      "effect": "deny",
      "principals": ["agent"],
      "actions": ["drive.read", "drive.write", "drive.session"],
      "drive_ids": ["$D2"],
      "reason": "drive blocked by policy smoke"
    }
  ]
}
open("$POLICY_FILE","w").write(json.dumps(doc))
print("policy rewritten for d2=$D2")
PY
start_api

TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"poladmin","password":"password1"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== agent with drive.read+write+job.run, both drives on allowlist =="
AID=$("${CURL[@]}" -X POST "$API/v1/agents" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"polbot\",\"default_scopes\":[\"drive.read\",\"drive.write\",\"job.run\"],\"allowed_drive_ids\":[\"$D1\",\"$D2\"]}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
ATOK=$("${CURL[@]}" -X POST "$API/v1/agents/$AID/token" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== agent can GET d1 =="
CODE=$("${CURL[@]}" -o /dev/null -w '%{http_code}' "$API/v1/drives/$D1" -H "Authorization: Bearer $ATOK")
test "$CODE" = "200"

echo "== agent forbidden on d2 by policy file =="
BODY=$("${CURL[@]}" "$API/v1/drives/$D2" -H "Authorization: Bearer $ATOK" || true)
CODE=$("${CURL[@]}" -o /dev/null -w '%{http_code}' "$API/v1/drives/$D2" -H "Authorization: Bearer $ATOK")
test "$CODE" = "403"
echo "$BODY" | python3 -c 'import sys,json; d=json.load(sys.stdin); err=d.get("error",""); assert "blocked by policy" in err or "policy" in err.lower(), d; print("d2 deny ok:", err)'

echo "== agent job create forbidden by policy =="
CODE=$("${CURL[@]}" -o /tmp/aihub-job.json -w '%{http_code}' -X POST "$API/v1/jobs" \
  -H "Authorization: Bearer $ATOK" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$D1\",\"command\":[\"echo\",\"hi\"]}")
test "$CODE" = "403"
python3 -c 'import json; d=json.load(open("/tmp/aihub-job.json")); assert "jobs blocked" in d.get("error",""), d; print("job deny ok:", d.get("error"))'

echo "== human can still create job =="
CODE=$("${CURL[@]}" -o /tmp/aihub-job2.json -w '%{http_code}' -X POST "$API/v1/jobs" \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$D1\",\"command\":[\"echo\",\"hi\"]}")
test "$CODE" = "201"
echo "human job ok"

echo "OK policy smoke agent=$AID d1=$D1 d2=$D2"
