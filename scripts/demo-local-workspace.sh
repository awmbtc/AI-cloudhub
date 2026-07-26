#!/usr/bin/env bash
# Local usable workspace WITHOUT FUSE mount (mode=sync_workspace).
# Pulls real MinIO objects into a local directory via hubd once.
#
# Usage:
#   ./scripts/demo-local-workspace.sh
#   OPEN_DIR=1 ./scripts/demo-local-workspace.sh   # open Finder on macOS
#
# Docs: docs/HUBD.md · docs/RUNTIME.md
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export CGO_ENABLED=0
export NO_PROXY="127.0.0.1,localhost,::1"
export no_proxy="$NO_PROXY"
CURL=(curl -sS --noproxy '*' --connect-timeout 3 --max-time 30)

if [[ -z "${API_PORT:-}" ]]; then
  API_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
fi
API="http://127.0.0.1:${API_PORT}"
DEVICE="${AI_CLOUDHUB_DEVICE_ID:-demo-local-device}"
WS="${AI_CLOUDHUB_LOCAL_WS:-$HOME/aihub-demo-workspace}"
USER_NAME="${USER_NAME:-demolocal}"
PASS="${PASS:-demolocalpass}"

MINIO_AK="${MINIO_ACCESS_KEY:-minioadmin}"
MINIO_SK="${MINIO_SECRET_KEY:-minioadmin}"
MINIO_BUCKET="${MINIO_BUCKET:-demo-local-bucket}"
MINIO_PREFIX="ws"
MINIO_PID=""
MINIO_DATA=""
APID=""
DB=""

cd "$ROOT"
mkdir -p .bin "$WS"

cleanup() {
  if [[ -n "${APID:-}" ]]; then kill "$APID" 2>/dev/null || true; fi
  if [[ -n "${MINIO_PID:-}" ]]; then kill "$MINIO_PID" 2>/dev/null || true; wait "$MINIO_PID" 2>/dev/null || true; fi
  if [[ -n "${DB:-}" ]]; then rm -f "$DB" "${DB}-wal" "${DB}-shm"; fi
  if [[ -n "${MINIO_DATA:-}" ]]; then rm -rf "$MINIO_DATA"; fi
}
trap cleanup EXIT

free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'
}

minio_health() {
  "${CURL[@]}" "${1%/}/minio/health/live" >/dev/null 2>&1
}

download_minio() {
  local dest oa os arch url
  dest="${ROOT}/.bin/minio-server"
  mkdir -p "${ROOT}/.bin"
  if [[ -x "$dest" ]]; then printf '%s' "$dest"; return 0; fi
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) return 1 ;; esac
  case "$os" in darwin|linux) ;; *) return 1 ;; esac
  oa="${os}-${arch}"
  url="https://dl.min.io/server/minio/release/${oa}/minio"
  echo "== download MinIO ${oa} =="
  curl -fsSL --connect-timeout 10 --max-time 180 -o "${dest}.partial" "$url"
  chmod +x "${dest}.partial" && mv "${dest}.partial" "$dest"
  printf '%s' "$dest"
}

echo "== 0) prerequisites =="
if ! command -v rclone >/dev/null 2>&1; then
  echo "rclone required. Install: brew install rclone" >&2
  exit 1
fi
go build -o .bin/api ./cmd/api
go build -o .bin/hubd ./cmd/hubd
go build -o .bin/minio-seed ./scripts/minio-seed 2>/dev/null || true

echo "== 1) MinIO =="
MINIO_EP="${MINIO_ENDPOINT:-}"
if [[ -n "$MINIO_EP" ]] && minio_health "$MINIO_EP"; then
  echo "use existing $MINIO_EP"
else
  bin="$(download_minio)"
  port="$(free_port)"
  MINIO_DATA="$(mktemp -d /tmp/aihub-demo-minio.XXXXXX)"
  MINIO_ROOT_USER="$MINIO_AK" MINIO_ROOT_PASSWORD="$MINIO_SK" \
    "$bin" server "$MINIO_DATA" --address "127.0.0.1:${port}" >/tmp/aihub-demo-minio.log 2>&1 &
  MINIO_PID=$!
  MINIO_EP="http://127.0.0.1:${port}"
  for _ in $(seq 1 50); do minio_health "$MINIO_EP" && break; sleep 0.15; done
  minio_health "$MINIO_EP" || { echo "MinIO failed"; cat /tmp/aihub-demo-minio.log; exit 1; }
  echo "MinIO $MINIO_EP"
fi

echo "== 2) seed objects =="
go build -o .bin/minio-seed ./scripts/minio-seed
# minio-seed wants host:port without scheme
MINIO_HP="${MINIO_EP#http://}"
MINIO_HP="${MINIO_HP#https://}"
MINIO_HP="${MINIO_HP%%/*}"
.bin/minio-seed -endpoint "$MINIO_HP" -ak "$MINIO_AK" -sk "$MINIO_SK" \
  -bucket "$MINIO_BUCKET" -prefix "${MINIO_PREFIX}/" \
  "hello-from-cloud.txt:hello from MinIO $(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  "notes/readme.txt:sync_workspace demo — no FUSE required"

echo "== 3) API =="
DB=$(mktemp /tmp/aihub-demo-api.XXXXXX)
HTTP_ADDR=":${API_PORT}" AI_CLOUDHUB_DB="$DB" JWT_SECRET="${JWT_SECRET:-demo-local-jwt-secretxxxx}" \
  ./.bin/api >/tmp/aihub-demo-api.log 2>&1 &
APID=$!
for _ in $(seq 1 50); do "${CURL[@]}" "$API/healthz" >/dev/null 2>&1 && break; sleep 0.1; done

echo "== 4) register / provider / drive / binding (sync_workspace) =="
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER_NAME\",\"password\":\"$PASS\"}" >/dev/null || true
TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER_NAME\",\"password\":\"$PASS\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
AUTH=(-H "Authorization: Bearer $TOK")
PID=$("${CURL[@]}" -X POST "$API/v1/providers" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"name\":\"demo-minio\",\"type\":\"minio\",\"credentials\":{\"access_key\":\"$MINIO_AK\",\"secret_key\":\"$MINIO_SK\",\"endpoint\":\"$MINIO_EP\"}}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
DID=$("${CURL[@]}" -X POST "$API/v1/drives" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"name\":\"demo-ws\",\"provider_id\":\"$PID\",\"bucket\":\"$MINIO_BUCKET\",\"prefix\":\"$MINIO_PREFIX\",\"mount_point\":\"$WS\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
BID=$("${CURL[@]}" -X POST "$API/v1/bindings" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"device_id\":\"$DEVICE\",\"mount_point\":\"$WS\",\"desired\":\"mounted\",\"mode\":\"sync_workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "== 5) hubd once (pull remote → local, no FUSE) =="
export AI_CLOUDHUB_API="$API"
export AI_CLOUDHUB_TOKEN="$TOK"
export AI_CLOUDHUB_DEVICE_ID="$DEVICE"
export AI_CLOUDHUB_STATE="${AI_CLOUDHUB_STATE:-$(mktemp -d /tmp/aihub-demo-hubd.XXXXXX)}"
# once: one reconcile then exit (pull happens in startMount for sync_workspace)
./.bin/hubd once 2>&1 | tee /tmp/aihub-demo-hubd.log | tail -20

echo "== 6) local files =="
if [[ ! -f "$WS/hello-from-cloud.txt" && ! -f "$WS/ws/hello-from-cloud.txt" ]]; then
  # prefix may strip or nest; list tree
  echo "listing $WS:"
  find "$WS" -type f 2>/dev/null | head -20 || true
  # hard assert any file present
  n=$(find "$WS" -type f 2>/dev/null | wc -l | tr -d ' ')
  if [[ "${n:-0}" -lt 1 ]]; then
    echo "FAIL: no files under $WS" >&2
    echo "--- hubd log ---" >&2
    cat /tmp/aihub-demo-hubd.log >&2 || true
    exit 1
  fi
fi
echo "workspace: $WS"
find "$WS" -type f | head -20
echo ""
echo "OK demo-local-workspace (sync_workspace, no FUSE)"
echo "  MinIO:  $MINIO_EP"
echo "  API:    $API  (exiting with cleanup; files stay in $WS)"
echo "  drive:  $DID  binding: $BID"
echo "  open:   open $WS   # macOS Finder"
if [[ "${OPEN_DIR:-0}" == "1" ]] && command -v open >/dev/null 2>&1; then
  open "$WS"
fi
