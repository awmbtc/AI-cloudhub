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
| `list_bindings` | drive.read\|write | `GET /v1/bindings` (optional `device_id`) |
| `ensure_mounted_hint` | drive.read\|write | Instructions + optional session probe; path jail on mount_point |
| `workspace_env` | — | Env contract (local) |
| `resolve_path` | — | Local path jail check |
| `list_snapshots` | drive.read\|write | `GET /v1/drives/{id}/snapshots` |
| `create_snapshot` | drive.write | `POST /v1/drives/{id}/snapshots` |
| `list_objects` | drive.read\|write | `GET /v1/drives/{id}/objects` live inventory |
| `object_restore_plan` | drive.read\|write | restore guidance: CLI + optional presign + api path |
| `object_presign_get` | drive.read\|write | short-lived GET URL; `type=qiniu` → `method=qiniu_download`; else S3 presign |
| `object_restore_version` | drive.write | BYOS server-side `CopyObject` version→current (no body proxy) |
| `list_jobs` | job.run | status / agent / labels 过滤 |
| `job_stats` | job.run | `GET /v1/jobs/stats` 各状态计数 |
| `get_job` | job.run | 含 exit/duration/labels/stdout… |
| `create_job` | job.run | BYOC；可选 timeout / max_attempts / priority / labels / `idempotency_key` |
| `claim_next_job` | job.run | priority claim；可选 `runner_id` + `region` |
| `complete_job` | job.run | complete — optional exit/duration/stdout/stderr + `*_truncated` |
| `heartbeat_job` | job.run | `POST /v1/jobs/{id}/heartbeat` — refresh lease while running |
| `cancel_job` | job.run | `POST /v1/jobs/{id}/cancel` |
| `list_providers` | provider.read\|write | `GET /v1/providers` (public fields) |
| `list_marketplace` | auth | `GET /v1/marketplace` |
| `install_marketplace` | auth | `POST …/install` — **skill/manifest OK for agents**; `agent_template` human-only |
| `marketplace_checkout` | auth (human API) | `POST …/checkout` → `checkout_url` + metadata |
| `list_memory` / `put_memory` / `search_memory` | auth | Memory Kernel (+ vector search) |
| `list_graph` / `link_graph` | auth | Identity Graph edges |
| `list_connectors` / `connectors_catalog` | auth | Connector bindings / types |
| `create_connector` | auth (human API) | `POST /v1/connectors` (secrets stripped) |
| `get_connector` | auth | `GET /v1/connectors/{id}` |
| `delete_connector` | auth (human API) | `DELETE /v1/connectors/{id}` |
| `list_lineage` / `record_lineage` | auth | Data Lineage events |

Live smoke: `make smoke-mcp` (`scripts/smoke-mcp-jobs.sh`) covers jobs + Stage C tools.

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

## See also

- [QUICKSTART-AGENT.md](./QUICKSTART-AGENT.md) — zero → agent token + MCP + optional hubd in ~30 minutes  
- [POLICY.md](./POLICY.md) — scopes, drive allowlist, optional OPA  
- `scripts/smoke-mcp-jobs.sh` / `make smoke-mcp` — live stdio job tools  
- `scripts/smoke-agent.sh` / `make smoke-agent` — agent allowlist + scopes  

