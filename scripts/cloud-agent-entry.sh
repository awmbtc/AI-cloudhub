#!/usr/bin/env bash
# 云端 Agent Runner 入口（最小契约）
# 用法：
#   export API_BASE=https://api.example.com
#   export API_TOKEN=...
#   export DRIVE_ID=...
#   export MOUNT_POINT=/workspace
#   ./scripts/cloud-agent-entry.sh -- agent-command args...
set -euo pipefail

API_BASE="${API_BASE:?API_BASE required}"
API_TOKEN="${API_TOKEN:?API_TOKEN required}"
DRIVE_ID="${DRIVE_ID:?DRIVE_ID required}"
MOUNT_POINT="${MOUNT_POINT:-/workspace}"
CONF_PATH="${RCLONE_CONFIG:-/tmp/rclone-clouddrive.conf}"
REMOTE_NAME="clouddrive"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing: $1" >&2; exit 1; }; }
need curl
need rclone
need python3

echo "[runner] fetch mount bundle drive=$DRIVE_ID"
BUNDLE=$(curl -fsS -H "Authorization: Bearer $API_TOKEN" \
  "$API_BASE/v1/drives/$DRIVE_ID/mount")

echo "$BUNDLE" | python3 -c 'import sys,json; print(json.load(sys.stdin)["rclone_conf"])' >"$CONF_PATH"
REMOTE_PATH=$(echo "$BUNDLE" | python3 -c 'import sys,json; print(json.load(sys.stdin)["remote_path"])')

mkdir -p "$MOUNT_POINT"
echo "[runner] mount $REMOTE_PATH -> $MOUNT_POINT"
rclone mount "$REMOTE_PATH" "$MOUNT_POINT" \
  --config "$CONF_PATH" \
  --vfs-cache-mode full \
  --daemon

# wait ready
for i in $(seq 1 30); do
  if mountpoint -q "$MOUNT_POINT" 2>/dev/null || ls "$MOUNT_POINT" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

cleanup() {
  echo "[runner] unmount $MOUNT_POINT"
  fusermount -u "$MOUNT_POINT" 2>/dev/null || umount "$MOUNT_POINT" 2>/dev/null || true
}
trap cleanup EXIT

export WORKDIR="$MOUNT_POINT"
cd "$MOUNT_POINT"

if [[ "${1:-}" == "--" ]]; then
  shift
fi
if [[ $# -eq 0 ]]; then
  echo "[runner] mounted. WORKDIR=$WORKDIR (no command given)"
  exit 0
fi

echo "[runner] exec: $*"
exec "$@"
