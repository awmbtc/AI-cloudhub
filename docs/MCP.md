# MCP helper (agent tool server)

`cmd/mcp` is a **stdio JSON-RPC** helper for agents. It is **MCP-compatible-ish** (initialize / tools/list / tools/call), not a full MCP SDK host.

## Why

Agents should not guess workspace paths. AI-cloudhub already injects:

- `AI_CLOUDHUB_WORKSPACE`
- Workspace Manifest at `$AI_CLOUDHUB_WORKSPACE/.ai-cloudhub/manifest.json`

This binary adds three tools so an agent runtime can **list drives**, get **mount instructions**, and read the **env contract** without embedding control-plane HTTP details.

## Build

```bash
export CGO_ENABLED=0
go build -o .bin/mcp ./cmd/mcp
```

Stdlib only; no extra module deps.

## Env

| Variable | Required | Meaning |
|----------|----------|---------|
| `AI_CLOUDHUB_API` | no (default `http://127.0.0.1:8080`) | Control-plane base URL |
| `AI_CLOUDHUB_TOKEN` | for live API tools | Bearer token |
| `AI_CLOUDHUB_MOUNT` | no | Default mount point in hints (`/workspace`) |
| `AI_CLOUDHUB_DEVICE_ID` | no | Used when probing drive sessions |
| `AI_CLOUDHUB_MODE` | no | `mount` (default) when probing |

## Protocol

- **Transport:** newline-delimited JSON-RPC 2.0 on **stdin/stdout** (logs on **stderr**).
- **Methods:**
  - `initialize` — server info + capabilities
  - `tools/list` — tool schemas
  - `tools/call` — `{ "name": "...", "arguments": { ... } }`
  - Direct convenience: `list_drives` / `ensure_mounted_hint` / `workspace_env` as method names
- **Tool results** follow MCP shape: `{ "content": [{ "type": "text", "text": "..." }], "isError": bool }`.

### Example

```bash
export AI_CLOUDHUB_API=http://127.0.0.1:8080
export AI_CLOUDHUB_TOKEN=<token>

printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"workspace_env","arguments":{}}}' \
  '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_drives","arguments":{}}}' \
  '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"ensure_mounted_hint","arguments":{"drive_id":"drv_xxx"}}}' \
  | ./.bin/mcp
```

## Tools

| Tool | Behavior |
|------|----------|
| `list_drives` | `GET /v1/drives` with bearer token |
| `ensure_mounted_hint` | Returns hubd/runner mount instructions. Optional `drive_id` / `binding_id` probes `POST .../session` (does **not** mount locally) |
| `workspace_env` | Documents `AI_CLOUDHUB_*` names from Manifest schema / `internal/manifest` (no network) |

## Wire into an agent host

Point the host’s MCP stdio server command at:

```text
AI_CLOUDHUB_API=... AI_CLOUDHUB_TOKEN=... /path/to/.bin/mcp
```

Exact client config (Claude Desktop, Cursor, etc.) varies; this binary only implements the wire protocol above.

## Non-goals (skeleton)

- No local FUSE/rclone mount from this process (use **hubd** / **runner**)
- No platform multi-tenant runner pool (D-001)
- No full MCP resources/prompts/sampling surface
