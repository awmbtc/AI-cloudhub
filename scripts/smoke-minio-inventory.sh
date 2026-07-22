#!/usr/bin/env bash
# Live MinIO hard-assert smoke: object inventory + snapshot include_objects.
#
# Flow:
#   1. Prefer existing MINIO_ENDPOINT (health + ListBuckets via seed probe)
#   2. Else auto-download official MinIO server binary and start on a free port
#   3. Ensure bucket, put 1–2 objects under drive prefix
#   4. Start API → provider/drive → GET objects (count>=1, keys present)
#   5. POST snapshot include_objects=true → payload.objects.ok + non-empty entries
#   6. Mutate object → second snapshot → snapshots/diff (added/changed)
#
# Usage:
#   make smoke-minio
#   ./scripts/smoke-minio-inventory.sh
#   MINIO_ENDPOINT=http://127.0.0.1:9000 ./scripts/smoke-minio-inventory.sh
#
# Env:
#   AI_CLOUDHUB_SMOKE_MINIO_REQUIRE=1  fail (exit 1) if MinIO cannot start
#   MINIO_ENDPOINT / MINIO_ACCESS_KEY / MINIO_SECRET_KEY / MINIO_BUCKET
#   API_PORT (default 18155)
#
# Offline path (no MinIO): make smoke-objects / scripts/smoke-objects.sh
#
# Skip policy: if MinIO cannot be reached or auto-started (e.g. network blocked),
# prints "SKIP: ..." and exits 0 unless AI_CLOUDHUB_SMOKE_MINIO_REQUIRE=1.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API_PORT="${API_PORT:-18155}"
API="http://127.0.0.1:${API_PORT}"
export CGO_ENABLED=0
export NO_PROXY="127.0.0.1,localhost,::1"
export no_proxy="$NO_PROXY"
CURL=(curl -sS --noproxy '*' --connect-timeout 3 --max-time 30)

USER="${USER_NAME:-miniouser}"
PASS="${PASS:-miniopass}" # >=8 chars (auth min)
REQUIRE="${AI_CLOUDHUB_SMOKE_MINIO_REQUIRE:-0}"

MINIO_AK="${MINIO_ACCESS_KEY:-minioadmin}"
MINIO_SK="${MINIO_SECRET_KEY:-minioadmin}"
MINIO_BUCKET="${MINIO_BUCKET:-testbucket}"
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
  # Keep downloaded binary under .bin for reuse; only remove if we used /tmp one-shot path.
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
  # $1 = base URL e.g. http://127.0.0.1:9000
  local base="$1"
  "${CURL[@]}" "${base%/}/minio/health/live" >/dev/null 2>&1
}

# Strip scheme for s3store / minio-go (host:port).
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
  data="$(mktemp -d /tmp/aihub-minio-data-XXXXXX)"
  MINIO_DATA="$data"
  log="/tmp/aihub-minio-server.log"
  echo "== start MinIO on 127.0.0.1:${port} (bin=$bin) =="
  # Root password must be long enough; minioadmin is fine.
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
    # process died?
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
    # Common default
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
  # args: optional extra name:body pairs
  cd "$ROOT"
  local extra=("$@")
  local default=(smoke-a.txt:smoke-body-a nested/smoke-b.txt:smoke-body-b)
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
  skip_or_fail "MinIO unavailable (cannot reach endpoint or download/start server binary). Live inventory smoke skipped."
fi

echo "== seed bucket/objects =="
if ! seed_objects; then
  skip_or_fail "MinIO reachable but seed (EnsureBucket/Put) failed — check credentials."
fi

echo "== build + start API =="
go build -o .bin/api ./cmd/api
DB=$(mktemp /tmp/aihub-minio-inv-XXXXXX.db)
HTTP_ADDR=":${API_PORT}" AI_CLOUDHUB_DB="$DB" JWT_SECRET="${JWT_SECRET:-minio-inventory-smoke-jwt}" \
  ./.bin/api >/tmp/aihub-minio-inv-api.log 2>&1 &
APID=$!

for _ in $(seq 1 50); do
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

echo "== provider + drive (prefix=${MINIO_PREFIX}) =="
PID=$("${CURL[@]}" -X POST "$API/v1/providers" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"live-minio\",\"type\":\"minio\",\"credentials\":{\"access_key\":\"$MINIO_AK\",\"secret_key\":\"$MINIO_SK\",\"endpoint\":\"$MINIO_EP\",\"region\":\"us-east-1\"}}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
DID=$("${CURL[@]}" -X POST "$API/v1/drives" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"inv-drive\",\"provider_id\":\"$PID\",\"bucket\":\"$MINIO_BUCKET\",\"prefix\":\"$MINIO_PREFIX\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
echo "drive_id=$DID provider_id=$PID endpoint=$MINIO_EP"

echo "== GET objects hard-assert inventory =="
INV=$("${CURL[@]}" "$API/v1/drives/$DID/objects?max=50" -H "Authorization: Bearer $TOK")
echo "$INV" | python3 -c '
import sys, json
d = json.load(sys.stdin)
assert "entries" in d, d
assert int(d.get("count") or 0) >= 1, ("expected count>=1", d)
keys = [e.get("key","") for e in d.get("entries") or []]
assert any("smoke-a.txt" in k for k in keys), ("missing smoke-a.txt", keys)
assert any("smoke-b.txt" in k for k in keys), ("missing smoke-b.txt", keys)
print("inventory ok count=%s keys=%s" % (d["count"], keys))
'

echo "== POST snapshot include_objects=true hard-assert =="
SNAP=$("${CURL[@]}" -X POST "$API/v1/drives/$DID/snapshots" \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"label":"with-inv","note":"live minio","include_objects":true,"max_objects":100}')
SID1=$(echo "$SNAP" | python3 -c '
import sys, json
d = json.load(sys.stdin)
assert d.get("id"), d
payload = d.get("payload")
if isinstance(payload, str):
    payload = json.loads(payload)
objs = (payload or {}).get("objects") or {}
assert objs.get("ok") is True, ("objects.ok != true", objs)
entries = objs.get("entries") or []
assert len(entries) >= 1, ("empty inventory entries", objs)
keys = [e.get("key","") for e in entries]
assert any("smoke-a.txt" in k for k in keys), keys
print(d["id"])
print("snapshot inventory ok count=%s kind_hint=%s" % (objs.get("count"), (payload or {}).get("kind")), file=sys.stderr)
')

echo "== mutate object + second snapshot =="
seed_objects "smoke-c.txt:smoke-body-c" >/dev/null
# also overwrite a to force size/etag change if possible
seed_objects "smoke-a.txt:smoke-body-a-v2" >/dev/null

SNAP2=$("${CURL[@]}" -X POST "$API/v1/drives/$DID/snapshots" \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"label":"with-inv-2","include_objects":true,"max_objects":100}')
SID2=$(echo "$SNAP2" | python3 -c '
import sys, json
d = json.load(sys.stdin)
assert d.get("id"), d
payload = d.get("payload")
if isinstance(payload, str):
    payload = json.loads(payload)
objs = (payload or {}).get("objects") or {}
assert objs.get("ok") is True, objs
assert int(objs.get("count") or 0) >= 2, objs
print(d["id"])
')

echo "== snapshots/diff hard-assert =="
DIFF=$("${CURL[@]}" "$API/v1/drives/$DID/snapshots/diff?a=$SID1&b=$SID2" \
  -H "Authorization: Bearer $TOK")
echo "$DIFF" | python3 -c '
import sys, json
d = json.load(sys.stdin)
assert d.get("a_has_objects") is True, d
assert d.get("b_has_objects") is True, d
assert "error" not in d or not d.get("error"), d
summary = d.get("summary") or {}
# smoke-c added; smoke-a may show as changed
assert int(summary.get("added") or 0) >= 1 or int(summary.get("changed") or 0) >= 1, d
print("diff ok summary=%s" % summary)
'

if [[ "$STARTED_MINIO" == "1" ]]; then
  echo "MinIO auto-start: yes ($MINIO_EP)"
else
  echo "MinIO auto-start: no (used existing $MINIO_EP)"
fi
echo "OK minio inventory smoke drive=$DID snap1=$SID1 snap2=$SID2"
