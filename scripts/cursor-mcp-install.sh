#!/usr/bin/env bash
# Thin Cursor MCP one-click helper for AI-cloudhub.
# Builds .bin/mcp if needed and prints (or writes) an mcp.json snippet.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${ROOT}/.bin/mcp"
API="${AI_CLOUDHUB_API:-http://127.0.0.1:8080}"
WORKSPACE="${AI_CLOUDHUB_WORKSPACE:-/workspace}"
TOKEN_HINT='<agent_token — mint via POST /v1/agents/{id}/token; see docs/CURSOR-MCP.md>'
FORCE_BUILD=0
WRITE_EXAMPLE=0
OUT=""

usage() {
  cat <<USAGE
Usage: $(basename "$0") [options]

  Builds .bin/mcp (CGO_ENABLED=0) if missing, then prints a Cursor MCP config
  snippet with absolute paths and placeholders for API URL + token.

Options:
  --force-build       Rebuild .bin/mcp even if it exists
  --write-example     Copy deploy/cursor/mcp.json.example → deploy/cursor/mcp.json
                      (gitignored if you add it; default only prints to stdout)
  --out PATH          Also write the generated JSON snippet to PATH
  -h, --help          Show this help

Env:
  AI_CLOUDHUB_API         Default API base in the snippet (default: http://127.0.0.1:8080)
  AI_CLOUDHUB_WORKSPACE   Workspace jail path (default: /workspace)
  AI_CLOUDHUB_TOKEN_FILE  If set and readable, snippet uses "\${AI_CLOUDHUB_TOKEN}" and
                          prints an export line that loads the file (token never embedded)

Docs: docs/CURSOR-MCP.md · docs/MCP.md · docs/PLUGIN-OAUTH.md (Phase 0 = manual token)
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --force-build) FORCE_BUILD=1; shift ;;
    --write-example) WRITE_EXAMPLE=1; shift ;;
    --out) OUT="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage >&2; exit 2 ;;
  esac
done

need_build=0
if [[ ! -x "$BIN" ]]; then
  need_build=1
fi
if [[ "$FORCE_BUILD" -eq 1 ]]; then
  need_build=1
fi

if [[ "$need_build" -eq 1 ]]; then
  echo "==> Building MCP binary → ${BIN}" >&2
  mkdir -p "${ROOT}/.bin"
  (cd "$ROOT" && CGO_ENABLED=0 go build -o "$BIN" ./cmd/mcp)
  echo "==> Built OK" >&2
else
  echo "==> Using existing ${BIN}" >&2
fi

TOKEN_LINE="$TOKEN_HINT"
EXPORT_HINT=""
if [[ -n "${AI_CLOUDHUB_TOKEN_FILE:-}" && -r "${AI_CLOUDHUB_TOKEN_FILE}" ]]; then
  TOKEN_LINE='${AI_CLOUDHUB_TOKEN}'
  EXPORT_HINT="export AI_CLOUDHUB_TOKEN=\"\$(cat ${AI_CLOUDHUB_TOKEN_FILE})\"   # load before starting Cursor"
fi

SNIPPET=$(cat <<JSON
{
  "mcpServers": {
    "ai-cloudhub": {
      "command": "${BIN}",
      "args": [],
      "env": {
        "AI_CLOUDHUB_API": "${API}",
        "AI_CLOUDHUB_TOKEN": "${TOKEN_LINE}",
        "AI_CLOUDHUB_WORKSPACE": "${WORKSPACE}"
      }
    }
  }
}
JSON
)

echo
echo "==> Paste into Cursor MCP config (project .cursor/mcp.json or global MCP settings):"
echo "$SNIPPET"
echo
echo "Notes:"
echo "  - command is absolute so Cursor cwd does not matter"
echo "  - replace AI_CLOUDHUB_TOKEN with an Agent Token (drive.read/write + allowlist)"
echo "  - HTTP alias resolve: GET ${API}/v1/drives/by-alias/{alias}"
if [[ -n "$EXPORT_HINT" ]]; then
  echo "  - ${EXPORT_HINT}"
fi
echo "  - full walkthrough: docs/CURSOR-MCP.md"

if [[ -n "$OUT" ]]; then
  mkdir -p "$(dirname "$OUT")"
  printf '%s\n' "$SNIPPET" > "$OUT"
  echo "==> Wrote snippet to ${OUT}" >&2
fi

if [[ "$WRITE_EXAMPLE" -eq 1 ]]; then
  src="${ROOT}/deploy/cursor/mcp.json.example"
  dst="${ROOT}/deploy/cursor/mcp.json"
  if [[ ! -f "$src" ]]; then
    echo "missing ${src}" >&2
    exit 1
  fi
  # Fill absolute command into a local (untracked) mcp.json for convenience
  python3 - "$src" "$dst" "$BIN" "$API" "$WORKSPACE" <<'PY'
import json, sys
src, dst, bin_path, api, ws = sys.argv[1:6]
with open(src) as f:
    data = json.load(f)
srv = data["mcpServers"]["ai-cloudhub"]
srv["command"] = bin_path
srv["env"]["AI_CLOUDHUB_API"] = api
srv["env"]["AI_CLOUDHUB_WORKSPACE"] = ws
# keep token placeholder
with open(dst, "w") as f:
    json.dump(data, f, indent=2)
    f.write("\n")
print(f"wrote {dst} (token still placeholder)", file=sys.stderr)
PY
fi

echo "==> Done." >&2
