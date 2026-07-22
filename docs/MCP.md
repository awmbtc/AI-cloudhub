# MCP helper (agent tool server)

`cmd/mcp` is a **stdio JSON-RPC** helper for agents. MCP-compatible-ish (`initialize` / `tools/list` / `tools/call`), not a full MCP SDK host.

## Version 0.2 security

| Control | Behavior |
|---------|----------|
| **Token** | `AI_CLOUDHUB_TOKEN` required for API tools |
| **Scopes** | Agent tokens: tools declare `required_scopes_any`; enforced via `GET /v1/me` |
| **Path jail** | `mount_point` / `resolve_path` must stay under workspace root |
| **No local mount** | Session probe only; mount via **hubd** / **runner** |

Human login tokens have full tool access. Agent tokens need e.g. `drive.read` for list/session tools.

## Build

```bash
export CGO_ENABLED=0
go build -o .bin/mcp ./cmd/mcp
```

## Env

| Variable | Required | Meaning |
|----------|----------|---------|
| `AI_CLOUDHUB_API` | no (default `http://127.0.0.1:8080`) | Control-plane base URL |
| `AI_CLOUDHUB_TOKEN` | for live API tools | Bearer (human or agent token) |
| `AI_CLOUDHUB_WORKSPACE` / `AI_CLOUDHUB_MOUNT` | no | Jail root (default `/workspace`) |
| `AI_CLOUDHUB_DEVICE_ID` | no | Device id for session probes |
| `AI_CLOUDHUB_MODE` | no | `mount` default when probing |

## Tools

| Tool | Scopes (agent) | Behavior |
|------|----------------|----------|
| `whoami` | — | `GET /v1/me` principal + scopes |
| `list_drives` | drive.read\|write | `GET /v1/drives` |
| `ensure_mounted_hint` | drive.read\|write | Instructions + optional session probe; path jail on mount_point |
| `workspace_env` | — | Env contract (local) |
| `resolve_path` | — | Local path jail check |
| `list_snapshots` | drive.read\|write | `GET /v1/drives/{id}/snapshots` |
| `create_snapshot` | drive.write | `POST /v1/drives/{id}/snapshots` |
| `list_objects` | drive.read\|write | `GET /v1/drives/{id}/objects` live inventory |
| `object_restore_plan` | drive.read\|write | restore guidance: CLI + optional presign + api path |
| `object_presign_get` | drive.read\|write | short-lived GET URL (optional `version_id`); bytes client↔store |
| `object_restore_version` | drive.write | BYOS server-side `CopyObject` version→current (no body proxy) |

## Example

```bash
export AI_CLOUDHUB_API=http://127.0.0.1:8080
export AI_CLOUDHUB_TOKEN=<token>   # prefer agent token with limited scopes

printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami","arguments":{}}}' \
  '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"resolve_path","arguments":{"path":"/etc/passwd"}}}' \
  '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"list_drives","arguments":{}}}' \
  | ./.bin/mcp
```

## Non-goals

- No FUSE mount from this process  
- No platform multi-tenant runner pool (D-001)  
- No full MCP resources/prompts surface  
