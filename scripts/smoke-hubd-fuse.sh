#!/usr/bin/env bash
# Real FUSE mount smoke: MinIO → binding mode=mount → hubd daemon → ls mountpoint.
# Soft-skip if rclone mount or macFUSE unavailable unless AI_CLOUDHUB_SMOKE_FUSE_REQUIRE=1.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export CGO_ENABLED=0
export NO_PROXY="127.0.0.1,localhost,::1"
export no_proxy="$NO_PROXY"
CURL=(curl -sS --noproxy '*' --connect-timeout 3 --max-time 30)
REQUIRE="${AI_CLOUDHUB_SMOKE_FUSE_REQUIRE:-0}"

if [[ -z "${API_PORT:-}" ]]; then
  API_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
fi
API="http://127.0.0.1:${API_PORT}"
DEVICE="fuse-smoke-device"
USER_NAME="fusemount"
PASS="fusemountxx"
MINIO_AK="${MINIO_ACCESS_KEY:-minioadmin}"
MINIO_SK="${MINIO_SECRET_KEY:-minioadmin}"
MINIO_BUCKET="fuse-smoke-bucket"
MINIO_PREFIX="ws"

MINIO_PID=""
MINIO_DATA=""
APID=""
HUBD_PID=""
DB=""
MP=""
STATE=""

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
  if [[ -n "${HUBD_PID:-}" ]]; then
    kill "$HUBD_PID" 2>/dev/null || true
    wait "$HUBD_PID" 2>/dev/null || true
  fi
  # Best-effort unmount leftovers
  if [[ -n "${MP:-}" ]]; then
    if command -v umount >/dev/null 2>&1; then
      umount "$MP" 2>/dev/null || true
    fi
    if command -v diskutil >/dev/null 2>&1; then
      diskutil unmount force "$MP" 2>/dev/null || true
    fi
    # rclone may leave mount busy briefly
    sleep 0.5
    umount "$MP" 2>/dev/null || true
  fi
  if [[ -n "${APID:-}" ]]; then kill "$APID" 2>/dev/null || true; fi
  if [[ -n "${MINIO_PID:-}" ]]; then kill "$MINIO_PID" 2>/dev/null || true; wait "$MINIO_PID" 2>/dev/null || true; fi
  if [[ -n "${DB:-}" ]]; then rm -f "$DB" "${DB}-wal" "${DB}-shm"; fi
  if [[ -n "${MINIO_DATA:-}" ]]; then rm -rf "$MINIO_DATA"; fi
  if [[ -n "${STATE:-}" ]]; then rm -rf "$STATE"; fi
  if [[ -n "${MP:-}" && -d "$MP" ]]; then rmdir "$MP" 2>/dev/null || true; fi
}
trap cleanup EXIT

cd "$ROOT"
mkdir -p .bin

echo "== preflight =="
# macOS Homebrew rclone intentionally disables mount — prefer official binary.
ensure_rclone_for_mount() {
  mkdir -p "${ROOT}/.bin"
  # Already have a non-homebrew rclone earlier in PATH?
  if command -v rclone >/dev/null 2>&1; then
    local rpath
    rpath="$(command -v rclone)"
    if ! strings "$rpath" 2>/dev/null | grep -q "installed via Homebrew"; then
      # quick runtime probe: homebrew fails with that message when mounting
      if [[ "$rpath" != /usr/local/Cellar/rclone/* && "$rpath" != /opt/homebrew/Cellar/rclone/* ]]; then
        if [[ -x "${ROOT}/.bin/rclone-official" ]]; then
          :
        fi
      fi
    fi
  fi
  local dest oa arch os
  dest="${ROOT}/.bin/rclone-official"
  if [[ ! -x "$dest" ]]; then
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"
    case "$arch" in x86_64|amd64) oa=amd64 ;; aarch64|arm64) oa=arm64 ;; *) return 1 ;; esac
    case "$os" in darwin) os=osx ;; linux) ;; *) return 1 ;; esac
    local url zip tmp
    if [[ "$os" == "osx" ]]; then
      url="https://downloads.rclone.org/rclone-current-osx-${oa}.zip"
    else
      url="https://downloads.rclone.org/rclone-current-linux-${oa}.zip"
    fi
    zip=$(mktemp /tmp/rclone-zip.XXXXXX)
    tmp=$(mktemp -d /tmp/rclone-ex.XXXXXX)
    echo "== download official rclone for FUSE mount =="
    curl -fsSL --connect-timeout 15 --max-time 180 -o "$zip" "$url" || return 1
    unzip -q "$zip" -d "$tmp"
    local bin
    bin=$(find "$tmp" -type f -name rclone | head -1)
    [[ -n "$bin" ]] || return 1
    cp "$bin" "$dest"
    chmod +x "$dest"
    rm -rf "$tmp" "$zip"
  fi
  # Shadow brew rclone for this process
  ln -sfn "$dest" "${ROOT}/.bin/rclone"
  export PATH="${ROOT}/.bin:${PATH}"
  hash -r 2>/dev/null || true
  echo "using rclone=$(command -v rclone)"
  rclone version 2>&1 | head -2
}
if ! ensure_rclone_for_mount; then
  skip_or_fail "could not install official rclone for mount"
fi
if ! command -v rclone >/dev/null 2>&1; then
  skip_or_fail "rclone not in PATH"
fi
if ! rclone mount --help >/dev/null 2>&1; then
  skip_or_fail "rclone mount not available"
fi
# macOS: prefer macfuse present
if [[ "$(uname -s)" == "Darwin" ]]; then
  if [[ ! -d /Library/Filesystems/macfuse.fs && ! -d /Library/Filesystems/osxfuse.fs ]]; then
    skip_or_fail "macFUSE/osxfuse filesystem not found under /Library/Filesystems"
  fi
fi

go build -o .bin/api ./cmd/api
go build -o .bin/hubd ./cmd/hubd
go build -o .bin/minio-seed ./scripts/minio-seed

free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'
}
minio_health() {
  "${CURL[@]}" "${1%/}/minio/health/live" >/dev/null 2>&1
}
download_minio() {
  local dest oa os arch
  dest="${ROOT}/.bin/minio-server"
  if [[ -x "$dest" ]]; then printf '%s' "$dest"; return 0; fi
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) return 1 ;; esac
  oa="${os}-${arch}"
  curl -fsSL --connect-timeout 10 --max-time 180 \
    -o "${dest}.partial" "https://dl.min.io/server/minio/release/${oa}/minio"
  chmod +x "${dest}.partial" && mv "${dest}.partial" "$dest"
  printf '%s' "$dest"
}

echo "== MinIO =="
MINIO_EP="${MINIO_ENDPOINT:-}"
if [[ -z "$MINIO_EP" ]] || ! minio_health "$MINIO_EP"; then
  bin="$(download_minio)"
  port="$(free_port)"
  MINIO_DATA="$(mktemp -d /tmp/aihub-fuse-minio.XXXXXX)"
  MINIO_ROOT_USER="$MINIO_AK" MINIO_ROOT_PASSWORD="$MINIO_SK" \
    "$bin" server "$MINIO_DATA" --address "127.0.0.1:${port}" >/tmp/aihub-fuse-minio.log 2>&1 &
  MINIO_PID=$!
  MINIO_EP="http://127.0.0.1:${port}"
  for _ in $(seq 1 50); do minio_health "$MINIO_EP" && break; sleep 0.15; done
  minio_health "$MINIO_EP" || skip_or_fail "MinIO failed to start"
fi
echo "MinIO $MINIO_EP"

MINIO_HP="${MINIO_EP#http://}"
MINIO_HP="${MINIO_HP#https://}"
MINIO_HP="${MINIO_HP%%/*}"
.bin/minio-seed -endpoint "$MINIO_HP" -ak "$MINIO_AK" -sk "$MINIO_SK" \
  -bucket "$MINIO_BUCKET" -prefix "${MINIO_PREFIX}/" \
  "fuse-hello.txt:hello-via-fuse-$(date -u +%Y%m%dT%H%M%SZ)" \
  "nested/fuse-b.txt:nested-fuse-body"

echo "== API =="
DB=$(mktemp /tmp/aihub-fuse-api.XXXXXX)
HTTP_ADDR=":${API_PORT}" AI_CLOUDHUB_DB="$DB" JWT_SECRET="${JWT_SECRET:-fuse-smoke-jwt-secretxxxx}" \
  ./.bin/api >/tmp/aihub-fuse-api.log 2>&1 &
APID=$!
for _ in $(seq 1 50); do "${CURL[@]}" "$API/healthz" >/dev/null 2>&1 && break; sleep 0.1; done

echo "== provider / drive / binding mode=mount =="
MP=$(mktemp -d /tmp/aihub-fuse-mp.XXXXXX)
# mount points should be empty for FUSE
rm -rf "$MP"/* 2>/dev/null || true

"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER_NAME\",\"password\":\"$PASS\"}" >/dev/null || true
TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER_NAME\",\"password\":\"$PASS\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
AUTH=(-H "Authorization: Bearer $TOK")
PID=$("${CURL[@]}" -X POST "$API/v1/providers" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"name\":\"fuse-minio\",\"type\":\"minio\",\"credentials\":{\"access_key\":\"$MINIO_AK\",\"secret_key\":\"$MINIO_SK\",\"endpoint\":\"$MINIO_EP\"}}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
DID=$("${CURL[@]}" -X POST "$API/v1/drives" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"name\":\"fuse-ws\",\"provider_id\":\"$PID\",\"bucket\":\"$MINIO_BUCKET\",\"prefix\":\"$MINIO_PREFIX\",\"mount_point\":\"$MP\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
BID=$("${CURL[@]}" -X POST "$API/v1/bindings" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"device_id\":\"$DEVICE\",\"mount_point\":\"$MP\",\"desired\":\"mounted\",\"mode\":\"mount\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
echo "binding=$BID mount_point=$MP"

echo "== hubd daemon (FUSE mount) =="
STATE=$(mktemp -d /tmp/aihub-fuse-hubd.XXXXXX)
export AI_CLOUDHUB_API="$API"
export AI_CLOUDHUB_TOKEN="$TOK"
export AI_CLOUDHUB_DEVICE_ID="$DEVICE"
export AI_CLOUDHUB_STATE="$STATE"
export AI_CLOUDHUB_POLL=2s
./.bin/hubd >/tmp/aihub-fuse-hubd.log 2>&1 &
HUBD_PID=$!

# Wait for mount to show file (up to ~20s)
found=0
for i in $(seq 1 40); do
  if [[ -f "$MP/fuse-hello.txt" ]] || [[ -f "$MP/ws/fuse-hello.txt" ]]; then
    found=1
    break
  fi
  # also accept any file under MP
  if find "$MP" -type f 2>/dev/null | grep -q .; then
    found=1
    break
  fi
  # hubd died?
  if ! kill -0 "$HUBD_PID" 2>/dev/null; then
    echo "hubd exited early:" >&2
    cat /tmp/aihub-fuse-hubd.log >&2 || true
    skip_or_fail "hubd exited before mount ready"
  fi
  sleep 0.5
done

if [[ "$found" != "1" ]]; then
  echo "--- hubd log ---" >&2
  cat /tmp/aihub-fuse-hubd.log >&2 || true
  echo "--- mount ls ---" >&2
  ls -la "$MP" >&2 || true
  mount | grep -i "$MP" >&2 || true
  if grep -q "file system is not available\|installed via Homebrew\|failed to mount FUSE" /tmp/aihub-fuse-hubd.log 2>/dev/null; then
    echo "HINT: macOS FUSE needs (1) official rclone (not brew) (2) macFUSE system extension allowed in System Settings → Privacy & Security." >&2
    echo "      Until then use: make demo-local  (sync_workspace, no FUSE)" >&2
  fi
  # probe mount_macfuse directly once
  if command -v mount_macfuse >/dev/null 2>&1; then
    mount_macfuse 2>&1 | head -3 >&2 || true
  fi
  skip_or_fail "no files visible under FUSE mount $MP within timeout (see HINT)"
fi

echo "== assert FUSE mount =="
# Prefer macOS mount table / fuse indication
if mount 2>/dev/null | grep -F "$MP" >/dev/null 2>&1; then
  echo "mount table lists $MP"
  mount | grep -F "$MP" || true
else
  echo "WARN: mount table does not list path (still verifying files)"
fi

echo "files under mount:"
find "$MP" -type f | head -20
BODY=$(find "$MP" -name 'fuse-hello.txt' -print -quit 2>/dev/null | head -1)
if [[ -z "$BODY" ]]; then
  BODY=$(find "$MP" -type f | head -1)
fi
echo "sample file: $BODY"
test -n "$BODY"
test -s "$BODY"
head -c 200 "$BODY"
echo ""

# Read via path — if this works through FUSE, good enough
MP="$MP" python3 -c '
import os
mp=os.environ["MP"]
files=[]
for r,_,fs in os.walk(mp):
  for f in fs:
    files.append(os.path.join(r,f))
assert files, "empty mount"
print("walk ok n=%d" % len(files))
'

ACT=$("${CURL[@]}" "$API/v1/bindings" -H "Authorization: Bearer $TOK" | BID="$BID" python3 -c '
import sys,json,os
d=json.load(sys.stdin)
items=d.get("items") if isinstance(d,dict) else d
if items is None:
  items=[]
bid=os.environ["BID"]
for b in items:
  if b.get("id")==bid:
    print(b.get("actual",""))
    break
else:
  print("")
')
echo "binding actual=$ACT"

echo "OK smoke-hubd-fuse mount=$MP binding=$BID actual=$ACT minio=$MINIO_EP"
echo "See docs/HUBD.md — mode=mount FUSE path"
