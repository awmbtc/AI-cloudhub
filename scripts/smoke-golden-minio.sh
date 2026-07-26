#!/usr/bin/env bash
# Live MinIO golden path: offline golden contract + real bucket inventory + BYOC job.
#
# Flow:
#   1. Prefer existing MINIO_ENDPOINT, else auto-start MinIO (same helpers as smoke-minio-inventory)
#   2. Seed ≥1 object under drive prefix
#   3. Start API → register → provider(minio live) → drive → binding → session
#   4. GET /v1/drives/{id}/objects hard-assert (live inventory sees seeded object)
#   5. Agent token → job create/claim/complete (BYOC; command=echo)
#
# Does NOT start hubd / FUSE mount — live contract is session + objects inventory.
# Real mount needs local rclone + FUSE/WinFsp; see docs/GOLDEN-PATH.md § Live MinIO path.
#
# Usage:
#   make smoke-golden-minio
#   ./scripts/smoke-golden-minio.sh
#   MINIO_ENDPOINT=http://127.0.0.1:9000 ./scripts/smoke-golden-minio.sh
#
# Env:
#   AI_CLOUDHUB_SMOKE_MINIO_REQUIRE=1  fail (exit 1) if MinIO cannot start
#   MINIO_ENDPOINT / MINIO_ACCESS_KEY / MINIO_SECRET_KEY / MINIO_BUCKET
#   API_PORT (default: free port)
#
# Skip policy: if MinIO cannot be reached or auto-started, prints "SKIP: ..." and
# exits 0 unless AI_CLOUDHUB_SMOKE_MINIO_REQUIRE=1.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
if [[ -z "${API_PORT:-}" ]]; then
  API_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
fi
API="http://127.0.0.1:${API_PORT}"
export CGO_ENABLED=0
export NO_PROXY="127.0.0.1,localhost,::1"
export no_proxy="$NO_PROXY"
CURL=(curl -sS --noproxy '*' --connect-timeout 3 --max-time 30)

USER="${USER_NAME:-goldenminio}"
PASS="${PASS:-goldenminiopass}" # >=8 chars (auth min)
REQUIRE="${AI_CLOUDHUB_SMOKE_MINIO_REQUIRE:-0}"

MINIO_AK="${MINIO_ACCESS_KEY:-minioadmin}"
MINIO_SK="${MINIO_SECRET_KEY:-minioadmin}"
MINIO_BUCKET="${MINIO_BUCKET:-golden-live-bucket}"
MINIO_PREFIX="ws"
MINIO_EP_IN="${MINIO_ENDPOINT:-}"

MINIO_PID=""
MINIO_DATA=""
MINIO_BIN_DOWNLOADED=""
APID=""
DB=""
STARTED_MINIO=0

skip_or_fail() {
  local msg="$1"
  if [[ "$REQUIRE" == "1" ]]; then
    echo "FAIL (require): $msg" >&2
    exit 1
  fi
  echo "SKIP: $msg"
  exit 0
}

cleanup() {
  if [[ -n "${APID:-}" ]]; then kill "$APID" 2>/dev/null || true; fi
  if [[ -n "${MINIO_PID:-}" ]]; then kill "$MINIO_PID" 2>/dev/null || true; wait "$MINIO_PID" 2>/dev/null || true; fi
  if [[ -n "${DB:-}" ]]; then rm -f "$DB" "${DB}-wal" "${DB}-shm"; fi
  if [[ -n "${MINIO_DATA:-}" ]]; then rm -rf "$MINIO_DATA"; fi
  if [[ -n "${MINIO_BIN_DOWNLOADED:-}" && "${MINIO_BIN_DOWNLOADED}" == /tmp/* ]]; then
    rm -f "$MINIO_BIN_DOWNLOADED"
  fi
}
trap cleanup EXIT

free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'
}

minio_os_arch() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) echo "unsupported arch: $arch" >&2; return 1 ;;
  esac
  case "$os" in
    darwin|linux) ;;
    *) echo "unsupported os: $os" >&2; return 1 ;;
  esac
  echo "${os}-${arch}"
}

minio_health() {
  local base="$1"
  "${CURL[@]}" "${base%/}/minio/health/live" >/dev/null 2>&1
}

hostport_from_url() {
  local u="$1"
  u="${u#http://}"
  u="${u#https://}"
  u="${u%%/*}"
  echo "$u"
}

download_minio() {
  local oa url dest
  oa="$(minio_os_arch)" || return 1
  dest="${ROOT}/.bin/minio-server"
  mkdir -p "${ROOT}/.bin"
  if [[ -x "$dest" ]]; then
    printf '%s' "$dest"
    return 0
  fi
  url="https://dl.min.io/server/minio/release/${oa}/minio"
  echo "== download MinIO server ${oa} ==" >&2
  if ! curl -fsSL --connect-timeout 10 --max-time 180 -o "${dest}.partial" "$url"; then
    echo "download failed: $url" >&2
    rm -f "${dest}.partial"
    return 1
  fi
  chmod +x "${dest}.partial"
  mv "${dest}.partial" "$dest"
  printf '%s' "$dest"
}

start_minio_local() {
  local bin port data log
  bin="$(download_minio)" || return 1
  if [[ ! -x "$bin" ]]; then
    echo "minio binary not executable: $bin" >&2
    return 1
  fi
  MINIO_BIN_DOWNLOADED="" # kept in .bin for reuse
  port="$(free_port)"
  data="$(mktemp -d /tmp/aihub-golden-minio-data-XXXXXX)"
  MINIO_DATA="$data"
  log="/tmp/aihub-golden-minio-server.log"
  echo "== start MinIO on 127.0.0.1:${port} (bin=$bin) =="
  MINIO_ROOT_USER="$MINIO_AK" MINIO_ROOT_PASSWORD="$MINIO_SK" \
    "$bin" server "$data" --address "127.0.0.1:${port}" >"$log" 2>&1 &
  MINIO_PID=$!
  STARTED_MINIO=1
  for _ in $(seq 1 50); do
    if minio_health "http://127.0.0.1:${port}"; then
      MINIO_EP="http://127.0.0.1:${port}"
      MINIO_HOSTPORT="127.0.0.1:${port}"
      echo "MinIO ready at $MINIO_EP (pid=$MINIO_PID)"
      return 0
    fi
    if ! kill -0 "$MINIO_PID" 2>/dev/null; then
      echo "MinIO process exited; log:" >&2
      tail -n 40 "$log" >&2 || true
      MINIO_PID=""
      return 1
    fi
    sleep 0.15
  done
  echo "MinIO health timeout; log:" >&2
  tail -n 40 "$log" >&2 || true
  return 1
}

ensure_minio() {
  if [[ -n "$MINIO_EP_IN" ]]; then
    echo "== try existing MINIO_ENDPOINT=$MINIO_EP_IN =="
    if minio_health "$MINIO_EP_IN"; then
      MINIO_EP="$MINIO_EP_IN"
      MINIO_HOSTPORT="$(hostport_from_url "$MINIO_EP")"
      echo "using existing MinIO at $MINIO_EP"
      return 0
    fi
    echo "existing endpoint not healthy; will try auto-start"
  else
    if minio_health "http://127.0.0.1:9000"; then
      MINIO_EP="http://127.0.0.1:9000"
      MINIO_HOSTPORT="127.0.0.1:9000"
      echo "using MinIO at $MINIO_EP"
      return 0
    fi
  fi
  start_minio_local || return 1
}

seed_objects() {
  cd "$ROOT"
  local extra=("$@")
  local default=(golden-live.txt:golden-minio-body nested/golden-b.txt:golden-nested)
  if [[ ${#extra[@]} -gt 0 ]]; then
    go run ./scripts/minio-seed \
      -endpoint "$MINIO_HOSTPORT" -ak "$MINIO_AK" -sk "$MINIO_SK" \
      -bucket "$MINIO_BUCKET" -prefix "${MINIO_PREFIX}/" \
      "${extra[@]}"
  else
    go run ./scripts/minio-seed \
      -endpoint "$MINIO_HOSTPORT" -ak "$MINIO_AK" -sk "$MINIO_SK" \
      -bucket "$MINIO_BUCKET" -prefix "${MINIO_PREFIX}/" \
      "${default[@]}"
  fi
}

# ---- main ----
cd "$ROOT"
mkdir -p .bin

echo "== ensure MinIO =="
if ! ensure_minio; then
  skip_or_fail "MinIO unavailable (cannot reach endpoint or download/start server binary). Live golden-minio smoke skipped."
fi

echo "== seed bucket/objects =="
if ! seed_objects; then
  skip_or_fail "MinIO reachable but seed (EnsureBucket/Put) failed — check credentials."
fi

echo "== build + start API =="
go build -o .bin/api ./cmd/api
DB=$(mktemp /tmp/aihub-golden-minio-XXXXXX.db)
HTTP_ADDR=":${API_PORT}" AI_CLOUDHUB_DB="$DB" JWT_SECRET="${JWT_SECRET:-golden-minio-smoke-jwt}" \
  ./.bin/api >/tmp/aihub-golden-minio-api.log 2>&1 &
APID=$!

for _ in $(seq 1 50); do
  if "${CURL[@]}" "$API/healthz" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$APID" 2>/dev/null; then
    echo "API exited early:" >&2
    cat /tmp/aihub-golden-minio-api.log >&2 || true
    exit 1
  fi
  sleep 0.1
done
"${CURL[@]}" "$API/healthz" >/dev/null

echo "== ① healthz / readyz =="
VER=$("${CURL[@]}" "$API/healthz" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("status")=="ok", d; print(d.get("version",""))')
"${CURL[@]}" "$API/readyz" | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("status")=="ready", d; print("readyz ok")'

echo "== ② register / login =="
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" >/dev/null || true
TOKEN=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== ③ provider + drive (live MinIO endpoint=$MINIO_EP prefix=$MINIO_PREFIX) =="
PID=$("${CURL[@]}" -X POST "$API/v1/providers" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"golden-live-minio\",\"type\":\"minio\",\"credentials\":{\"access_key\":\"$MINIO_AK\",\"secret_key\":\"$MINIO_SK\",\"endpoint\":\"$MINIO_EP\",\"region\":\"us-east-1\"}}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
DID=$("${CURL[@]}" -X POST "$API/v1/drives" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"golden-ws\",\"provider_id\":\"$PID\",\"bucket\":\"$MINIO_BUCKET\",\"prefix\":\"$MINIO_PREFIX\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
echo "drive_id=$DID provider_id=$PID"

echo "== ④ binding + session (STS contract) =="
BID=$("${CURL[@]}" -X POST "$API/v1/bindings" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"device_id\":\"golden-minio-device\",\"mount_point\":\"/workspace\",\"desired\":\"mounted\"}" \
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

echo "== ⑤ GET objects hard-assert (live inventory) =="
INV=$("${CURL[@]}" "$API/v1/drives/$DID/objects?max=50" -H "Authorization: Bearer $TOKEN")
echo "$INV" | python3 -c '
import sys, json
d = json.load(sys.stdin)
assert "entries" in d, d
assert int(d.get("count") or 0) >= 1, ("expected count>=1", d)
keys = [e.get("key","") for e in d.get("entries") or []]
assert any("golden-live.txt" in k for k in keys), ("missing golden-live.txt", keys)
print("inventory ok count=%s keys=%s" % (d["count"], keys))
'

echo "== ⑥ agent token (scopes + drive allowlist) =="
AID=$("${CURL[@]}" -X POST "$API/v1/agents" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"golden-minio-agent\",\"default_scopes\":[\"drive.read\",\"drive.write\",\"job.run\",\"provider.read\"],\"allowed_drive_ids\":[\"$DID\"]}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
ATOK=$("${CURL[@]}" -X POST "$API/v1/agents/$AID/token" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

echo "== ⑦ BYOC job create → claim → complete =="
JID=$("${CURL[@]}" -X POST "$API/v1/jobs" \
  -H "Authorization: Bearer $ATOK" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"command\":[\"echo\",\"golden-minio-path\"],\"mode\":\"sync_workspace\"}" \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("id"), d; print(d["id"])')
CLAIMED=$("${CURL[@]}" -X POST "$API/v1/jobs/next/claim" \
  -H "Authorization: Bearer $ATOK" \
  -H 'X-AI-Cloudhub-Runner-Id: golden-minio-runner' \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("status")=="running", d; print(d["id"])')
test "$CLAIMED" = "$JID"
"${CURL[@]}" -X POST "$API/v1/jobs/$JID/complete" \
  -H "Authorization: Bearer $ATOK" -H 'Content-Type: application/json' \
  -d '{"ok":true,"exit_code":0,"stdout":"golden-minio-path\n"}' \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("status")=="succeeded", d; print("job", d["id"], "status", d["status"])'

echo "== ⑧ me (human) =="
"${CURL[@]}" "$API/v1/me" -H "Authorization: Bearer $TOKEN" \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); assert d.get("username") or d.get("id"), d; print("user", d.get("username") or d.get("id"))'

if [[ "$STARTED_MINIO" == "1" ]]; then
  echo "MinIO auto-start: yes ($MINIO_EP)"
else
  echo "MinIO auto-start: no (used existing $MINIO_EP)"
fi
echo "OK golden-minio version=$VER drive=$DID binding=$BID agent=$AID job=$JID endpoint=$MINIO_EP"
echo "See docs/GOLDEN-PATH.md · Live MinIO path · D-003 job ops freeze"
