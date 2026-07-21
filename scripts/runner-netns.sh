#!/usr/bin/env bash
# Optional Linux network isolation wrapper for BYOC runner (soft netns).
#
# Usage:
#   ./scripts/runner-netns.sh -- ./.bin/runner -- /path/to/agent
#
# Requires: Linux with unshare (util-linux). Does NOT work on macOS/Windows.
# Creates a new network namespace for the child process (no external interfaces
# unless you add them). This is stronger than AI_CLOUDHUB_NETWORK=deny env filter.
#
# Still BYOC: runs on user machines only (D-001).

set -euo pipefail

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "runner-netns.sh: Linux only (got $(uname -s)); falling back without netns" >&2
  exec "$@"
fi

if ! command -v unshare >/dev/null 2>&1; then
  echo "runner-netns.sh: unshare not found; falling back" >&2
  exec "$@"
fi

# New network + UTS/IPC/PID namespaces where permitted. Drop root requirement:
# unshare -n often needs CAP_SYS_ADMIN; if it fails, fall back.
if unshare -n -- true 2>/dev/null; then
  echo "runner-netns.sh: launching with unshare -n (empty netns)" >&2
  export AI_CLOUDHUB_NETWORK=deny
  exec unshare -n -- "$@"
fi

echo "runner-netns.sh: unshare -n not permitted (need CAP_SYS_ADMIN); env-only deny" >&2
export AI_CLOUDHUB_NETWORK=deny
exec "$@"
