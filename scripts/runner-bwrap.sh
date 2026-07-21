#!/usr/bin/env bash
# Optional Linux filesystem/network isolation for BYOC runner using bubblewrap or firejail.
#
# Usage:
#   AI_CLOUDHUB_MOUNT=/workspace ./scripts/runner-bwrap.sh -- ./.bin/runner -- agent-cmd
#
# Prefers: bwrap (bubblewrap) → firejail → runner-netns.sh → plain exec.
# Still BYOC on user machines only (D-001).

set -euo pipefail

MOUNT="${AI_CLOUDHUB_MOUNT:-/workspace}"
export AI_CLOUDHUB_NETWORK="${AI_CLOUDHUB_NETWORK:-deny}"
export AI_CLOUDHUB_JAIL="${AI_CLOUDHUB_JAIL:-1}"

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "runner-bwrap.sh: non-Linux; exec plain" >&2
  exec "$@"
fi

# bubblewrap: private /tmp, bind workspace RW, ro bind essentials
if command -v bwrap >/dev/null 2>&1; then
  echo "runner-bwrap.sh: using bwrap (workspace=$MOUNT)" >&2
  # Ensure mount exists on host
  mkdir -p "$MOUNT" 2>/dev/null || true
  exec bwrap \
    --ro-bind /usr /usr \
    --ro-bind /bin /bin \
    --ro-bind /lib /lib \
    --ro-bind-try /lib64 /lib64 \
    --ro-bind /etc /etc \
    --tmpfs /tmp \
    --dev /dev \
    --proc /proc \
    --bind "$MOUNT" "$MOUNT" \
    --chdir "$MOUNT" \
    --die-with-parent \
    --unshare-pid \
    -- "$@"
fi

if command -v firejail >/dev/null 2>&1; then
  echo "runner-bwrap.sh: using firejail" >&2
  exec firejail --quiet --private-tmp --net=none --whitelist="$MOUNT" -- "$@"
fi

# Fall back to netns wrapper if present
ROOT="$(cd "$(dirname "$0")" && pwd)"
if [[ -x "$ROOT/runner-netns.sh" ]]; then
  echo "runner-bwrap.sh: falling back to runner-netns.sh" >&2
  exec "$ROOT/runner-netns.sh" -- "$@"
fi

echo "runner-bwrap.sh: no bwrap/firejail/netns; plain exec + NETWORK=deny" >&2
exec "$@"
