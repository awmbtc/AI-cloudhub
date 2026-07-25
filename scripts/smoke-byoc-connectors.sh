#!/usr/bin/env bash
# BYOC connector 联调 smoke (user machine only — D-001).
# Covers: local git clone + postgres/mysql env inject via runner MATERIALIZE_ONLY
# (no rclone/FUSE required).
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
go build -o .bin/runner ./cmd/runner

if ! command -v git >/dev/null 2>&1; then
  echo "SKIP smoke-byoc-connectors: git not in PATH" >&2
  exit 0
fi

DB=$(mktemp /tmp/aihub-byoc-XXXXXX.db)
WORK=$(mktemp -d /tmp/aihub-byoc-ws-XXXXXX)
REPO=$(mktemp -d /tmp/aihub-byoc-repo-XXXXXX)
REPORT_DIR=$(mktemp -d /tmp/aihub-byoc-rep-XXXXXX)
cleanup() {
  kill "$APID" 2>/dev/null || true
  rm -f "$DB" "${DB}-wal" "${DB}-shm"
  rm -rf "$WORK" "$REPO" "$REPORT_DIR"
}
trap cleanup EXIT

HTTP_ADDR=":${API_PORT}" \
  AI_CLOUDHUB_DB="$DB" \
  JWT_SECRET="${JWT_SECRET:-byoc-smoke-jwt-secretxx}" \
  ./.bin/api >/tmp/aihub-byoc-api.log 2>&1 &
APID=$!

for _ in $(seq 1 50); do
  "${CURL[@]}" "$API/healthz" >/dev/null 2>&1 && break
  sleep 0.1
done

echo "== register + connectors =="
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"byocusr","password":"byocpassxx"}' >/dev/null || true
TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"byocusr","password":"byocpassxx"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
AUTH=(-H "Authorization: Bearer $TOK")

# Local bare repo for git clone (file:// URL — pure local BYOC)
git -C "$REPO" init -q --bare
# seed via temp clone
SEED=$(mktemp -d /tmp/aihub-byoc-seed-XXXXXX)
git -C "$SEED" init -q
git -C "$SEED" config user.email smoke@local
git -C "$SEED" config user.name smoke
echo "byoc-ok" >"$SEED/README.md"
git -C "$SEED" add README.md
git -C "$SEED" commit -q -m "seed"
git -C "$SEED" remote add origin "$REPO"
git -C "$SEED" push -q origin HEAD:main 2>/dev/null || git -C "$SEED" push -q origin HEAD:master
rm -rf "$SEED"
# detect default branch
BR=main
git -C "$REPO" rev-parse --verify main >/dev/null 2>&1 || BR=master

GIT_URL="file://$REPO"
GID=$("${CURL[@]}" -X POST "$API/v1/connectors" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"type\":\"git\",\"name\":\"local\",\"config\":{\"remote_url\":\"$GIT_URL\",\"branch\":\"$BR\",\"path_prefix\":\"repo\"}}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
PID=$("${CURL[@]}" -X POST "$API/v1/connectors" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"type":"postgres","name":"pg","config":{"host":"127.0.0.1","database":"app","user":"ro","port":"5432","password":"NOPE"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
MID=$("${CURL[@]}" -X POST "$API/v1/connectors" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"type":"mysql","name":"my","config":{"host":"127.0.0.1","database":"app","user":"ro","port":"3306","password":"NOPE"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
echo "connectors git=$GID pg=$PID mysql=$MID"

export AI_CLOUDHUB_API="$API"
export AI_CLOUDHUB_TOKEN="$TOK"
export AI_CLOUDHUB_MATERIALIZE_ONLY=1

echo "== materialize git (local clone) =="
GIT_WS="$WORK/git"
mkdir -p "$GIT_WS"
AI_CLOUDHUB_CONNECTOR_ID="$GID" \
AI_CLOUDHUB_MOUNT="$GIT_WS" \
AI_CLOUDHUB_MATERIALIZE_REPORT="$REPORT_DIR/git.json" \
  ./.bin/runner >/tmp/aihub-byoc-git.out 2>/tmp/aihub-byoc-git.err
python3 - <<PY
import json
r=json.load(open("$REPORT_DIR/git.json"))
assert r.get("ok"), r
assert "cloned to" in (r.get("note") or ""), r
assert r.get("clone_path")
import os
assert os.path.isdir(os.path.join(r["clone_path"], ".git")) or os.path.isfile(os.path.join("$GIT_WS","repo",".git"))
readme=open(os.path.join("$GIT_WS","repo","README.md")).read()
assert "byoc-ok" in readme, readme
print("git materialize ok", r["clone_path"])
PY

echo "== materialize postgres (env only) =="
mkdir -p "$WORK/pg"
AI_CLOUDHUB_CONNECTOR_ID="$PID" \
AI_CLOUDHUB_MOUNT="$WORK/pg" \
AI_CLOUDHUB_MATERIALIZE_REPORT="$REPORT_DIR/pg.json" \
  ./.bin/runner >/tmp/aihub-byoc-pg.out 2>/tmp/aihub-byoc-pg.err
python3 -c '
import json
r=json.load(open("'"$REPORT_DIR/pg.json"'"))
assert r.get("ok"), r
assert "pg ready" in (r.get("note") or ""), r
e=r.get("extra_env") or {}
assert e.get("AI_CLOUDHUB_PG_HOST")=="127.0.0.1"
assert e.get("AI_CLOUDHUB_PG_DATABASE")=="app"
assert e.get("AI_CLOUDHUB_PG_DSN_TEMPLATE")
assert r.get("pass_libpq") is True
print("postgres materialize ok", e["AI_CLOUDHUB_PG_DSN_TEMPLATE"][:48])
'

echo "== materialize mysql (env only) =="
mkdir -p "$WORK/my"
AI_CLOUDHUB_CONNECTOR_ID="$MID" \
AI_CLOUDHUB_MOUNT="$WORK/my" \
AI_CLOUDHUB_MATERIALIZE_REPORT="$REPORT_DIR/my.json" \
  ./.bin/runner >/tmp/aihub-byoc-my.out 2>/tmp/aihub-byoc-my.err
python3 -c '
import json
r=json.load(open("'"$REPORT_DIR/my.json"'"))
assert r.get("ok"), r
assert "mysql ready" in (r.get("note") or ""), r
e=r.get("extra_env") or {}
assert e.get("AI_CLOUDHUB_MYSQL_HOST")=="127.0.0.1"
assert e.get("AI_CLOUDHUB_MYSQL_DATABASE")=="app"
assert "tcp(" in (e.get("AI_CLOUDHUB_MYSQL_DSN_TEMPLATE") or "")
assert r.get("pass_mysql") is True
print("mysql materialize ok", e["AI_CLOUDHUB_MYSQL_DSN_TEMPLATE"])
'

echo "== job connector_id enqueue (control plane) =="
# minimal drive for job field only (no claim/run)
PR=$("${CURL[@]}" -X POST "$API/v1/providers" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"name":"m","type":"minio","credentials":{"access_key":"a","secret_key":"bsecretxx","endpoint":"http://127.0.0.1:9000"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
DID=$("${CURL[@]}" -X POST "$API/v1/drives" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"name\":\"d\",\"provider_id\":\"$PR\",\"bucket\":\"b\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
"${CURL[@]}" -X POST "$API/v1/jobs" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"command\":[\"true\"],\"connector_id\":\"$GID\"}" \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("connector_id")=="'"$GID"'"; print("job connector_id ok", d["id"])'

echo "OK smoke-byoc-connectors"
