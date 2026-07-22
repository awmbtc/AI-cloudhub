#!/usr/bin/env bash
# Optional Linux seccomp wrapper for BYOC runner (skeleton).
#
# Usage:
#   AI_CLOUDHUB_MOUNT=/workspace ./scripts/runner-seccomp.sh -- ./.bin/runner -- agent-cmd
#
# Prefers (in order):
#   1) bwrap + --seccomp FD   (if AI_CLOUDHUB_SECCOMP_BPF is a compiled BPF filter)
#   2) firejail --seccomp
#   3) docker run --security-opt seccomp=scripts/seccomp/runner-default.json (if AI_CLOUDHUB_SECCOMP_DOCKER=1)
#   4) runner-bwrap.sh → runner-netns.sh → plain
#
# Profile JSON: scripts/seccomp/runner-default.json
# Full in-process BPF (libseccomp-golang) is intentionally deferred — see docs/KNOWN_LIMITATIONS.md.
# D-001: runs only on user machines; no platform mega runner pool.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
PROFILE="${AI_CLOUDHUB_SECCOMP_PROFILE:-$ROOT/seccomp/runner-default.json}"
export AI_CLOUDHUB_NETWORK="${AI_CLOUDHUB_NETWORK:-deny}"
export AI_CLOUDHUB_JAIL="${AI_CLOUDHUB_JAIL:-1}"

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "runner-seccomp.sh: non-Linux; exec plain" >&2
  exec "$@"
fi

# Pre-compiled BPF filter via bubblewrap (advanced; optional)
if [[ -n "${AI_CLOUDHUB_SECCOMP_BPF:-}" && -f "${AI_CLOUDHUB_SECCOMP_BPF}" ]] && command -v bwrap >/dev/null 2>&1; then
  echo "runner-seccomp.sh: bwrap + seccomp BPF=$AI_CLOUDHUB_SECCOMP_BPF" >&2
  # Open BPF as FD 3 for bwrap --seccomp
  exec 3<"$AI_CLOUDHUB_SECCOMP_BPF"
  MOUNT="${AI_CLOUDHUB_MOUNT:-/workspace}"
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
    --seccomp 3 \
    -- "$@"
fi

if command -v firejail >/dev/null 2>&1; then
  echo "runner-seccomp.sh: firejail --seccomp (profile=$PROFILE note: firejail uses own filters)" >&2
  MOUNT="${AI_CLOUDHUB_MOUNT:-/workspace}"
  exec firejail --quiet --seccomp --private-tmp --net=none --whitelist="$MOUNT" -- "$@"
fi

if [[ "${AI_CLOUDHUB_SECCOMP_DOCKER:-}" == "1" ]] && command -v docker >/dev/null 2>&1 && [[ -f "$PROFILE" ]]; then
  echo "runner-seccomp.sh: docker --security-opt seccomp=$PROFILE (debug only)" >&2
  # Docker path is for profile testing; not the primary BYOC path.
  exec docker run --rm -i \
    --security-opt "seccomp=$PROFILE" \
    -e AI_CLOUDHUB_NETWORK -e AI_CLOUDHUB_JAIL \
    -v "${AI_CLOUDHUB_MOUNT:-/workspace}:/workspace" \
    -w /workspace \
    "${AI_CLOUDHUB_SECCOMP_IMAGE:-alpine:3.20}" \
    "$@"
fi

echo "runner-seccomp.sh: no BPF/firejail; falling back to runner-bwrap.sh" >&2
if [[ -x "$ROOT/runner-bwrap.sh" ]]; then
  exec "$ROOT/runner-bwrap.sh" -- "$@"
fi
if [[ -x "$ROOT/runner-netns.sh" ]]; then
  exec "$ROOT/runner-netns.sh" -- "$@"
fi
exec "$@"
