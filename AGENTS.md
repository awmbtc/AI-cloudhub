# AGENTS.md — AI-cloudhub

Short rules for AI coding agents. Prefer this over inventing product direction.

## Build / Go

- **Module**: `github.com/awmbtc/AI-cloudhub`
- **Always** `CGO_ENABLED=0` (Makefile exports it; pure Go / modernc sqlite).
- Build: `make build` → `.bin/{api,hubd,runner,mcp}`
- Unit tests: `make test` (`go test ./...`)

## Non‑negotiable product rules

| ID | Rule | Do / Don't |
|----|------|------------|
| **D-001** | No platform runner mega-pool | Jobs are **BYOC**: user-owned `hubd` / `runner` only. Do **not** invent a multi-tenant platform fleet, default K8s runner farm, or “we host all compute” path. |
| **BYOS** | User brings object storage | Control plane is thin: STS, metadata, CopyObject, presign. **No object body proxy** through API (no download/upload relay of file bytes). |
| Runtime | Same contract | Local `hubd` and cloud `runner` share mount/session/manifest contract; cloud runs on **user** machines/accounts. |

When tempted to add “platform pool / free cloud agents / body streaming proxy”, **stop** — see `docs/DECISIONS.md` (D-001) and `docs/ARCHITECTURE.md`.

## Smoke targets (`Makefile`)

| Target | Script / notes |
|--------|----------------|
| `make smoke` | `scripts/smoke-p0.sh` (build + P0) |
| `make smoke-agent` | `scripts/smoke-agent.sh` |
| `make smoke-objects` | `scripts/smoke-objects.sh` |
| `make smoke-minio` | live MinIO inventory/snapshot; soft-skip unless `AI_CLOUDHUB_SMOKE_MINIO_REQUIRE=1` |
| `make smoke-policy` | `scripts/smoke-policy.sh` |
| `make smoke-job` | `scripts/smoke-job.sh` (BYOC jobs) |
| `make smoke-mcp` | `scripts/smoke-mcp-jobs.sh` |
| `make smoke-all` | smoke + agent + objects + policy + job + mcp (not minio) |

Also present under `scripts/`: `smoke-drive.sh`, `smoke-p1.sh` (not all wired as `make` aliases).

## Key packages

| Path | Role |
|------|------|
| `cmd/api` | HTTP control plane entry |
| `cmd/hubd` | Local mount daemon (user machine) |
| `cmd/runner` | BYOC runner / optional job worker |
| `cmd/mcp` | Stdio MCP-ish agent tools |
| `internal/httpserver` | Routes, middleware, server wiring |
| `internal/drive` | Drives, bindings, objects, barrier, snapshot (**no body proxy**) |
| `internal/job` | Durable BYOC job queue (claim by owner only) |
| `internal/policy` | Quotas, rate limits, auth limits, engine |
| `internal/sts` | Short-lived credentials (AWS/MinIO/Aliyun/Tencent/…) |
| `internal/provider` | Provider catalog + encrypted credentials |
| `internal/auth` | API keys, sessions, scopes |
| `internal/store` | memory / sqlite / postgres |
| `internal/sandbox` | path jail, seccomp helpers |
| `internal/mountlib` | rclone mount integration |

## Docs map

- Architecture: `docs/ARCHITECTURE.md`
- Decisions (D-001…): `docs/DECISIONS.md`
- Limits: `docs/KNOWN_LIMITATIONS.md`
- OpenAPI: `docs/openapi.yaml`
- MCP tools: `docs/MCP.md`

## Agent hygiene

1. Keep `CGO_ENABLED=0`; do not reintroduce CGO deps.
2. Do **not** design platform mega-pool / multi-tenant shared runner fleet.
3. Do **not** stream or proxy object **bodies** via control plane; prefer STS, presign, server-side CopyObject.
4. Prefer existing packages above; match OpenAPI + smoke scripts when changing APIs.
5. Jobs: owner-authenticated claim only; annotate D-001 when touching pool-like behavior.
