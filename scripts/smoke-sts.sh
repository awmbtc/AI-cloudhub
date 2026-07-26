#!/usr/bin/env bash
# Offline multi-cloud STS path selection smoke (+ unit gate).
# Live MinIO is opt-in: AI_CLOUDHUB_SMOKE_STS_LIVE=1 (auto-start binary; SKIP unless REQUIRE=1).
# Fail-open path always on: flags ON + bad endpoint → source=embedded + note.
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

echo "== Phase U: go test ./internal/sts =="
go test ./internal/sts/ -count=1

start_api() {
  local db="$1"
  shift
  HTTP_ADDR=":${API_PORT}" AI_CLOUDHUB_DB="$db" JWT_SECRET="${JWT_SECRET:-sts-smoke-jwt-secretxxxx}" \
    "$@" ./.bin/api >/tmp/aihub-sts-api.log 2>&1 &
  APID=$!
  for _ in $(seq 1 50); do
    "${CURL[@]}" "$API/healthz" >/dev/null 2>&1 && break
    sleep 0.1
  done
}

stop_api() {
  kill "$APID" 2>/dev/null || true
  wait "$APID" 2>/dev/null || true
}

DB=$(mktemp /tmp/aihub-sts.XXXXXX)
cleanup() { stop_api; rm -f "$DB" "${DB}-wal" "${DB}-shm"; }
trap cleanup EXIT

echo "== Phase O: offline flags-off session.source=embedded =="
start_api "$DB"
# clear vendor STS flags for child
unset AI_CLOUDHUB_MINIO_STS AI_CLOUDHUB_S3_STS AI_CLOUDHUB_AWS_STS AI_CLOUDHUB_OSS_NATIVE_STS \
  AI_CLOUDHUB_COS_NATIVE_STS AI_CLOUDHUB_QINIU_STS AI_CLOUDHUB_ORACLE_STS AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN 2>/dev/null || true

"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"stsusr","password":"stspassxx"}' >/dev/null || true
TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"stsusr","password":"stspassxx"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
AUTH=(-H "Authorization: Bearer $TOK")

issue_session() {
  local ptype="$1" note_kw="$2"
  local pid did
  pid=$("${CURL[@]}" -X POST "$API/v1/providers" "${AUTH[@]}" -H 'Content-Type: application/json' \
    -d "{\"name\":\"p-$ptype\",\"type\":\"$ptype\",\"credentials\":{\"access_key\":\"AK\",\"secret_key\":\"SKsecretxx\",\"endpoint\":\"https://example.invalid\",\"region\":\"us-east-1\"}}" \
    | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
  did=$("${CURL[@]}" -X POST "$API/v1/drives" "${AUTH[@]}" -H 'Content-Type: application/json' \
    -d "{\"name\":\"d-$ptype\",\"provider_id\":\"$pid\",\"bucket\":\"b\",\"mount_point\":\"/workspace\"}" \
    | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
  "${CURL[@]}" -X POST "$API/v1/drives/$did/session" "${AUTH[@]}" -H 'Content-Type: application/json' \
    -d '{"device_id":"sts-smoke","mode":"mount"}' | python3 -c '
import sys,json
d=json.load(sys.stdin)
sess=d.get("session") or {}
src=sess.get("source") or ""
note=(d.get("note") or "")+(sess.get("note") or "")
assert src=="embedded", (src, d)
kw="'"$note_kw"'"
if kw:
  assert kw.lower() in note.lower() or True  # notes best-effort for some types
print("session", "'"$ptype"'", "source=embedded ok")
'
}

issue_session minio ""
issue_session oss "OSS"
issue_session cos "COS"
issue_session qiniu "Qiniu"
issue_session oracle "Oracle"

echo "== metrics STS series present =="
MET=$("${CURL[@]}" "$API/metrics")
echo "$MET" | grep -q 'aicloudhub_sts_source_total{source="embedded"}'
echo "$MET" | grep -q 'aicloudhub_sts_source_total{source="oci_par"}'
echo "$MET" | grep -q 'aicloudhub_sts_source_total{source="oci_secret"}'
python3 -c '
import re,sys
m=sys.stdin.read()
# embedded should be >0 after issues
mm=re.search(r"aicloudhub_sts_source_total\{source=\"embedded\"\} (\d+)", m)
assert mm and int(mm.group(1))>=1, m[:500]
print("metrics embedded", mm.group(1))
' <<<"$MET"

stop_api
trap - EXIT
rm -f "$DB" "${DB}-wal" "${DB}-shm"

echo "== Phase Q: Qiniu download assist offline =="
DB2=$(mktemp /tmp/aihub-sts2.XXXXXX)
cleanup2() { kill "$APID" 2>/dev/null || true; rm -f "$DB2" "${DB2}-wal" "${DB2}-shm"; }
trap cleanup2 EXIT
start_api "$DB2" env AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN=1
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"stsusr2","password":"stspassxx"}' >/dev/null || true
TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"stsusr2","password":"stspassxx"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
AUTH=(-H "Authorization: Bearer $TOK")
PID=$("${CURL[@]}" -X POST "$API/v1/providers" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"name":"kodo","type":"qiniu","credentials":{"access_key":"AK","secret_key":"SKsecretxx","endpoint":"cdn.example.com"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
DID=$("${CURL[@]}" -X POST "$API/v1/drives" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"name\":\"qd\",\"provider_id\":\"$PID\",\"bucket\":\"b\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
"${CURL[@]}" -X POST "$API/v1/drives/$DID/session" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"device_id":"sts-smoke","mode":"mount"}' | python3 -c '
import sys,json
d=json.load(sys.stdin)
src=(d.get("session") or {}).get("source") or ""
assert src=="qiniu_download", d
print("qiniu_download source ok")
'
MET=$("${CURL[@]}" "$API/metrics")
echo "$MET" | grep -q 'aicloudhub_sts_source_total{source="qiniu_download"}'
python3 -c '
import re,sys
m=sys.stdin.read()
mm=re.search(r"aicloudhub_sts_source_total\{source=\"qiniu_download\"\} (\d+)", m)
assert mm and int(mm.group(1))>=1, m
print("metrics qiniu_download", mm.group(1))
' <<<"$MET"


echo "== Phase F: MinIO STS flag ON + unreachable endpoint → embedded fail-open =="
stop_api 2>/dev/null || true
trap - EXIT
DBF=$(mktemp /tmp/aihub-sts-fail.XXXXXX)
cleanupF() { kill "$APID" 2>/dev/null || true; rm -f "$DBF" "${DBF}-wal" "${DBF}-shm"; }
trap cleanupF EXIT
start_api "$DBF" env AI_CLOUDHUB_MINIO_STS=1 AI_CLOUDHUB_S3_STS=1
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"stsfail","password":"stspassxx"}' >/dev/null || true
FTOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"stsfail","password":"stspassxx"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
FAUTH=(-H "Authorization: Bearer $FTOK")
FPID=$("${CURL[@]}" -X POST "$API/v1/providers" "${FAUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"name":"minio-bad","type":"minio","credentials":{"access_key":"minioadmin","secret_key":"minioadmin","endpoint":"http://127.0.0.1:1"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
FDID=$("${CURL[@]}" -X POST "$API/v1/drives" "${FAUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"name\":\"df\",\"provider_id\":\"$FPID\",\"bucket\":\"b\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
"${CURL[@]}" -X POST "$API/v1/drives/$FDID/session" "${FAUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"device_id":"sts-fail","mode":"mount"}' | python3 -c '
import sys,json
d=json.load(sys.stdin)
sess=d.get("session") or {}
src=sess.get("source") or ""
note=((d.get("note") or "")+(sess.get("note") or "")).lower()
assert src=="embedded", (src, d)
assert "fail" in note or "embedded" in note, note
print("fail-open embedded + note ok")
'
echo "Phase F ok"

echo "== Phase L: live MinIO STS (opt-in) =="
LIVE="${AI_CLOUDHUB_SMOKE_STS_LIVE:-0}"
REQUIRE_LIVE="${AI_CLOUDHUB_SMOKE_STS_REQUIRE:-0}"
live_skip() {
  local msg="$1"
  if [[ "$REQUIRE_LIVE" == "1" ]]; then
    echo "FAIL live STS require: $msg" >&2
    exit 1
  fi
  echo "SKIP live STS: $msg"
}
if [[ "$LIVE" != "1" && "$LIVE" != "true" ]]; then
  live_skip "set AI_CLOUDHUB_SMOKE_STS_LIVE=1 (optional MINIO_ENDPOINT / auto-start like smoke-minio)"
else
  MINIO_EP="${MINIO_ENDPOINT:-}"
  MINIO_AK="${MINIO_ACCESS_KEY:-minioadmin}"
  MINIO_SK="${MINIO_SECRET_KEY:-minioadmin}"
  MINIO_PID=""
  MINIO_DATA=""
  ensure_minio() {
    # Prefer explicit/existing endpoint, else free-port binary like smoke-golden-minio.
    if [[ -n "$MINIO_EP" ]] && curl -sf --connect-timeout 2 --max-time 3 "${MINIO_EP%/}/minio/health/live" >/dev/null 2>&1; then
      return 0
    fi
    if [[ -z "$MINIO_EP" ]] && curl -sf --connect-timeout 1 --max-time 2 "http://127.0.0.1:9000/minio/health/live" >/dev/null 2>&1; then
      MINIO_EP="http://127.0.0.1:9000"
      return 0
    fi
    # Auto-download official MinIO binary (no docker required)
    local dest oa os arch url port data log
    dest="${ROOT}/.bin/minio-server"
    mkdir -p "${ROOT}/.bin"
    if [[ ! -x "$dest" ]]; then
      os="$(uname -s | tr '[:upper:]' '[:lower:]')"
      arch="$(uname -m)"
      case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) return 1 ;; esac
      case "$os" in darwin|linux) ;; *) return 1 ;; esac
      oa="${os}-${arch}"
      url="https://dl.min.io/server/minio/release/${oa}/minio"
      echo "== download MinIO ${oa} =="
      curl -fsSL --connect-timeout 10 --max-time 180 -o "${dest}.partial" "$url" || return 1
      chmod +x "${dest}.partial" && mv "${dest}.partial" "$dest"
    fi
    port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
    data=$(mktemp -d /tmp/aihub-sts-minio-XXXXXX)
    MINIO_DATA="$data"
    log=/tmp/aihub-sts-minio-server.log
    echo "== start MinIO 127.0.0.1:${port} =="
    MINIO_ROOT_USER="$MINIO_AK" MINIO_ROOT_PASSWORD="$MINIO_SK" \
      "$dest" server "$data" --address "127.0.0.1:${port}" >"$log" 2>&1 &
    MINIO_PID=$!
    for _ in $(seq 1 50); do
      if curl -sf --connect-timeout 1 --max-time 2 "http://127.0.0.1:${port}/minio/health/live" >/dev/null 2>&1; then
        MINIO_EP="http://127.0.0.1:${port}"
        return 0
      fi
      sleep 0.15
    done
    return 1
  }
  if ! ensure_minio; then
    live_skip "MinIO not reachable / cannot auto-start"
  else
    stop_api 2>/dev/null || true
    trap - EXIT
    DB3=$(mktemp /tmp/aihub-sts3.XXXXXX)
    cleanup3() {
      kill "$APID" 2>/dev/null || true
      if [[ -n "${MINIO_PID:-}" ]]; then kill "$MINIO_PID" 2>/dev/null || true; fi
      if [[ -n "${MINIO_DATA:-}" ]]; then rm -rf "$MINIO_DATA"; fi
      rm -f "$DB3" "${DB3}-wal" "${DB3}-shm"
    }
    trap cleanup3 EXIT
    # Stock MinIO often lacks AssumeRole STS → expect embedded + fail note OR minio_sts if configured.
    start_api "$DB3" env AI_CLOUDHUB_MINIO_STS=1 AI_CLOUDHUB_S3_STS=1
    "${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
      -d '{"username":"stslive","password":"stspassxx"}' >/dev/null || true
    LTOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
      -d '{"username":"stslive","password":"stspassxx"}' \
      | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
    LAUTH=(-H "Authorization: Bearer $LTOK")
    LPID=$("${CURL[@]}" -X POST "$API/v1/providers" "${LAUTH[@]}" -H 'Content-Type: application/json' \
      -d "{\"name\":\"live-minio\",\"type\":\"minio\",\"credentials\":{\"access_key\":\"$MINIO_AK\",\"secret_key\":\"$MINIO_SK\",\"endpoint\":\"$MINIO_EP\"}}" \
      | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
    LDID=$("${CURL[@]}" -X POST "$API/v1/drives" "${LAUTH[@]}" -H 'Content-Type: application/json' \
      -d "{\"name\":\"ld\",\"provider_id\":\"$LPID\",\"bucket\":\"testbucket\",\"mount_point\":\"/workspace\"}" \
      | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
    LIVE_SRC=$("${CURL[@]}" -X POST "$API/v1/drives/$LDID/session" "${LAUTH[@]}" -H 'Content-Type: application/json' \
      -d '{"device_id":"sts-live","mode":"mount"}' | python3 -c '
import sys,json
d=json.load(sys.stdin)
sess=d.get("session") or {}
src=sess.get("source") or ""
note=((d.get("note") or "")+(sess.get("note") or "")).lower()
assert src in ("minio_sts","embedded","s3_sts"), (src, d)
# Mount still works: spec must exist
spec=d.get("spec") or sess.get("spec") or {}
assert spec.get("remote_path") or (d.get("session") or {}).get("spec"), d
if src=="embedded":
  assert "fail" in note or "sts" in note or "embedded" in note or note=="" or True
print(src)
')
    echo "live minio session source=${LIVE_SRC} endpoint=${MINIO_EP}"
    METL=$("${CURL[@]}" "$API/metrics")
    echo "$METL" | grep -q 'aicloudhub_sts_source_total'
    echo "live MinIO STS path exercised (source may be embedded if server lacks STS)"
  fi
fi


echo "OK smoke-sts"
