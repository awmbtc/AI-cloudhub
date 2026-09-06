# Cursor: wire AI-cloudhub MCP + Agent Token

Goal: in **Cursor**, install this repo’s stdio MCP helper so agents can say **「写到 A 盘」** and resolve alias `A` → drive id (BYOS; no object body proxy; no platform runner pool — D-001 / D-003).

Related: [MCP.md](./MCP.md) · [QUICKSTART-AGENT.md](./QUICKSTART-AGENT.md) · [PLUGIN-FOR-AGENTS.md](./PLUGIN-FOR-AGENTS.md)

---

## 0. One-command install (recommended)

From the repo root:

```bash
./scripts/cursor-mcp-install.sh
# optional:
#   AI_CLOUDHUB_API=https://hub.example.com ./scripts/cursor-mcp-install.sh
#   ./scripts/cursor-mcp-install.sh --write-example   # also writes deploy/cursor/mcp.json from example
```

The script:

1. Builds `.bin/mcp` with `CGO_ENABLED=0` if missing (or if `--force-build`)
2. Prints a ready-to-paste Cursor MCP snippet (`mcpServers.ai-cloudhub`) with **absolute** `command` path
3. Uses placeholders for `AI_CLOUDHUB_API` and token (prefer reading a local token file — never commit tokens)

Committed example config: [`deploy/cursor/mcp.json.example`](../deploy/cursor/mcp.json.example).

Then mint an Agent Token (section 2), put it in env or a file your shell exports, and reload Cursor MCP.

HTTP equivalent of `resolve_drive` by alias: `GET /v1/drives/by-alias/{alias}` (scope `drive.read`; agent allowlist same as get-by-id).

---

## 1. Build the MCP binary

```bash
cd /path/to/AI-cloudhub
export CGO_ENABLED=0
go build -o .bin/mcp ./cmd/mcp
# or: make build   # also builds api/hubd/runner
```

Use an **absolute path** to `.bin/mcp` in Cursor config (Cursor may start MCP with a different cwd).

---

## 2. Mint an Agent Token (drive allowlist)

With the API running and a human session token:

```bash
export API=http://127.0.0.1:8080
export TOKEN=<human_jwt>

# Create agent (scopes + allowlist). Replace DRIVE_ID_A with the id of alias A.
curl -sS -X POST "$API/v1/agents" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "cursor-writer",
    "default_scopes": ["drive.read", "drive.write"],
    "allowed_drive_ids": ["DRIVE_ID_A"]
  }'

# Issue agent token
curl -sS -X POST "$API/v1/agents/<agent_id>/token" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"ttl_sec": 86400}'
# → copy access_token / token field into AI_CLOUDHUB_TOKEN below
```

Create drives with stable aliases when possible:

```bash
curl -sS -X POST "$API/v1/drives" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "R2 reports",
    "alias": "A",
    "provider_id": "<provider_id>",
    "bucket": "my-bucket",
    "mount_point": "/workspace"
  }'
```

**Alias rules:** optional; pattern `^[A-Za-z][A-Za-z0-9_-]{0,15}$`; stored **uppercase**; **unique per user** when set. Prefer `A` / `B` for drive-letter UX; longer labels like `work` → `WORK` are fine.

---

## 3. Cursor `mcp.json` snippet

Cursor MCP config (project `.cursor/mcp.json` or global MCP settings — see current Cursor docs for the exact file location):

```json
{
  "mcpServers": {
    "ai-cloudhub": {
      "command": "/absolute/path/to/AI-cloudhub/.bin/mcp",
      "args": [],
      "env": {
        "AI_CLOUDHUB_API": "http://127.0.0.1:8080",
        "AI_CLOUDHUB_TOKEN": "<agent_token>",
        "AI_CLOUDHUB_WORKSPACE": "/workspace"
      }
    }
  }
}
```

| Env | Meaning |
|-----|---------|
| `AI_CLOUDHUB_API` | Control-plane base URL |
| `AI_CLOUDHUB_TOKEN` | Prefer **Agent Token** with `drive.read` / `drive.write` + allowlist |
| `AI_CLOUDHUB_WORKSPACE` | Path jail root for `resolve_path` / mount hints |

Restart Cursor MCP / reload window after editing.

---

## 4. Example prompts

After tools show up (`list_drives`, `resolve_drive`, …):

> 把报告写到 AI-cloudhub 的 **A 盘** `/reports/weekly.md`。先用 `resolve_drive`（alias=`A`）拿到 drive id，再用挂载路径 / `ensure_mounted_hint`，不要向我要云厂商 SecretKey。

Agent should:

1. `resolve_drive` with `alias: "A"` → `{ id, alias, name, mount_point }`  
2. Write under that mount / workspace (or hubd-mounted path)  
3. Never request long-lived AK/SK

List tools also return `alias` on each drive.

---

## 5. Smoke locally (optional)

```bash
export AI_CLOUDHUB_API=http://127.0.0.1:8080
export AI_CLOUDHUB_TOKEN=<agent_token>
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"resolve_drive","arguments":{"alias":"A"}}}' \
  | .bin/mcp
```

Full tool table: [MCP.md](./MCP.md). Broader agent path: [QUICKSTART-AGENT.md](./QUICKSTART-AGENT.md).
