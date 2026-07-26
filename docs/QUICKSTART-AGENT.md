# Agent 30-minute quickstart (MCP + hubd)

Goal: from a clean clone to **agent token → MCP tools → optional hubd mount hint** in about 30 minutes.

This is the short path. Full tool tables live in [MCP.md](./MCP.md); production hardening in [PRODUCTION.md](./PRODUCTION.md); policy in [POLICY.md](./POLICY.md). Automated regression: `make smoke-agent` / `make smoke-mcp` / **`make smoke-quickstart-agent`**.

**Verified on v0.2.26** (local SQLite, free high port, no live MinIO): register → agent token → MCP `whoami` / `list_drives` → create/claim/complete job → `ensure_mounted_hint` → offline Qiniu `object_presign_get` (`method=qiniu_download`).

---

## 1. Prerequisites

| Need | Notes |
|------|--------|
| Go toolchain | Module `github.com/awmbtc/AI-cloudhub`; pure Go |
| **`CGO_ENABLED=0`** | Always (Makefile exports it; modernc sqlite — no CGO) |
| curl + python3 | For the curl/json one-liners below |
| **rclone** | Optional until real mount: required for **hubd** FUSE/mount; not needed for API-only + MCP session probe |
| Object store | MinIO/S3-compatible (or any catalog type). Credentials can be dummy for API/agent smoke; live list/presign need a real bucket |

Build binaries:

```bash
cd /path/to/AI-cloudhub
export CGO_ENABLED=0
make build
# → .bin/api  .bin/hubd  .bin/runner  .bin/mcp
# or: go build -o .bin/api ./cmd/api && go build -o .bin/mcp ./cmd/mcp && go build -o .bin/hubd ./cmd/hubd
```

Sources: `cmd/api`, `cmd/mcp`, `cmd/hubd` (runner is optional for this path).

---

## 2. Start API (local SQLite, no STRICT)

Do **not** set `AI_CLOUDHUB_STRICT=1` for this walkthrough (STRICT rejects weak JWT / missing master key).

```bash
mkdir -p data
export JWT_SECRET=local-dev-jwt-secretxx   # ≥16 chars; not for production
export AI_CLOUDHUB_DB=./data/ai-cloudhub.db # default is SQLite; or memory for throwaway
# Prefer a free port if 8080 is busy:
export HTTP_ADDR=:8080
# optional: export AI_CLOUDHUB_MASTER_KEY="$(openssl rand -base64 32)"  # encrypts provider secrets

./.bin/api
```

In another shell:

```bash
export API=http://127.0.0.1:8080   # match HTTP_ADDR
export NO_PROXY=127.0.0.1,localhost
curl -sS --noproxy '*' "$API/healthz"   # expect "version":"0.2.26" (or your build)
curl -sS --noproxy '*' "$API/readyz"
```

Tip: if a proxy hijacks localhost, always use `curl --noproxy '*'` (same as smoke scripts).

| Env | Local default |
|-----|----------------|
| `JWT_SECRET` | Required-ish for real tokens; use a long random string |
| `AI_CLOUDHUB_DB` | SQLite path (e.g. `./data/ai-cloudhub.db`) or `memory` |
| `AI_CLOUDHUB_STRICT` | **Unset / 0** locally |
| `AI_CLOUDHUB_ALLOW_REGISTER` | Default open (first user becomes admin) |

See `.env.example` and [PRODUCTION.md](./PRODUCTION.md) when leaving the laptop.

---

## 3. Register / login, provider + drive

```bash
export API=http://127.0.0.1:8080
CURL=(curl -sS --noproxy '*')

# Register (idempotent-ish: ignore error if user exists)
"${CURL[@]}" -X POST "$API/v1/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"dev","password":"devpassxx"}' || true

TOK=$("${CURL[@]}" -X POST "$API/v1/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"dev","password":"devpassxx"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
echo "human token len=${#TOK}"

# MinIO-shaped provider (swap type/credentials for s3/r2/oss/…)
PID=$("${CURL[@]}" -X POST "$API/v1/providers" \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{"name":"local-minio","type":"minio","credentials":{"access_key":"minioadmin","secret_key":"minioadmin","endpoint":"http://127.0.0.1:9000"}}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

DID=$("${CURL[@]}" -X POST "$API/v1/drives" \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"workspace\",\"provider_id\":\"$PID\",\"bucket\":\"mybucket\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

echo "provider=$PID drive=$DID"
```

Catalog: `GET /v1/providers/catalog`. Agent tokens **cannot** manage devices or create agents (human session only).

---

## 4. Create agent (scopes + drive allowlist)

Recommended scopes for this path: `drive.read`, `drive.write`, `job.run`, `provider.read`.

```bash
AID=$("${CURL[@]}" -X POST "$API/v1/agents" \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"name\":\"mcpbot\",\"default_scopes\":[\"drive.read\",\"drive.write\",\"job.run\",\"provider.read\"],\"allowed_drive_ids\":[\"$DID\"]}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
echo "agent=$AID"
```

| Field | Meaning |
|-------|---------|
| `default_scopes` | Used when minting a token with empty/omitted scopes. Allowed: `drive.read`, `drive.write`, `job.run`, `provider.read`, `provider.write` |
| `allowed_drive_ids` | **Allowlist**. Empty ⇒ agent may not touch any drive (403 *drive not allowed for agent*) |
| `read_prefixes` / `write_prefixes` | Optional path prefix jail (policy layer) |

Same pattern as `scripts/smoke-agent.sh` and `scripts/smoke-mcp-jobs.sh`.

---

## 5. Mint agent token

```bash
# Default: agent.default_scopes, ttl_min defaults to 60
ATOK=$("${CURL[@]}" -X POST "$API/v1/agents/$AID/token" \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d '{}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')

# Or pin scopes / TTL:
# -d '{"scopes":["drive.read","job.run"],"ttl_min":120}'

export AI_CLOUDHUB_API="$API"
export AI_CLOUDHUB_TOKEN="$ATOK"
echo "agent token ready"
```

Check principal:

```bash
"${CURL[@]}" "$API/v1/me" -H "Authorization: Bearer $ATOK"
```

---

## 6. MCP stdio example

Build: `go build -o .bin/mcp ./cmd/mcp` (or `make mcp`).

```bash
export AI_CLOUDHUB_API=http://127.0.0.1:8080
export AI_CLOUDHUB_TOKEN="$ATOK"          # prefer agent token
export AI_CLOUDHUB_WORKSPACE=/workspace   # path jail root (default /workspace)

printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami","arguments":{}}}' \
  '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_drives","arguments":{}}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":5,\"method\":\"tools/call\",\"params\":{\"name\":\"list_objects\",\"arguments\":{\"drive_id\":\"$DID\",\"max\":50}}}" \
  "{\"jsonrpc\":\"2.0\",\"id\":6,\"method\":\"tools/call\",\"params\":{\"name\":\"object_presign_get\",\"arguments\":{\"drive_id\":\"$DID\",\"key\":\"hello.txt\",\"ttl_min\":15}}}" \
  | ./.bin/mcp
```

### Job create → claim → complete (sketch)

BYOC only (D-001: **user** runners claim jobs; no platform pool). Full automated flow: `scripts/smoke-mcp-jobs.sh`.

```bash
# create
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"create_job\",\"arguments\":{\"drive_id\":\"$DID\",\"command\":[\"echo\",\"hello-byoc\"],\"mode\":\"sync_workspace\"}}}" \
  | ./.bin/mcp

# claim next pending (same or another agent token with job.run)
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"claim_next_job","arguments":{}}}' \
  | ./.bin/mcp

# complete (use job_id from claim response content[0].text JSON)
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"complete_job","arguments":{"job_id":"<JOB_ID>","ok":true,"note":"done"}}}' \
  | ./.bin/mcp
```

| Tool | Agent scope (any of) |
|------|----------------------|
| `whoami` | — (token required) |
| `list_drives` / `list_objects` / `object_presign_get` | `drive.read` \| `drive.write` |
| `create_job` / `claim_next_job` / `complete_job` / `cancel_job` | `job.run` |
| `list_providers` | `provider.read` \| `provider.write` |
| `resolve_path` | local jail only (no API) |

MCP does **not** mount FUSE; it can probe sessions and return hubd/runner commands (`ensure_mounted_hint`). Details: [MCP.md](./MCP.md).

---

## 7. hubd path (optional real mount)

MCP / control plane issue **sessions**; **hubd** (desktop) or **runner** (BYOC) perform the mount.

### 7.1 Device id + binding (human token)

```bash
export DEVICE_ID=laptop-1

# Create a binding: desired mount for this device + drive
BID=$("${CURL[@]}" -X POST "$API/v1/bindings" \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' \
  -d "{\"drive_id\":\"$DID\",\"device_id\":\"$DEVICE_ID\",\"mount_point\":\"/workspace\",\"desired\":\"mounted\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
echo "binding=$BID"
```

### 7.2 `ensure_mounted_hint` (MCP — instructions + session probe)

```bash
export AI_CLOUDHUB_TOKEN="$ATOK"   # or human TOK
export AI_CLOUDHUB_DEVICE_ID="$DEVICE_ID"

printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"ensure_mounted_hint\",\"arguments\":{\"drive_id\":\"$DID\",\"mount_point\":\"/workspace\"}}}" \
  | ./.bin/mcp

# Or probe via binding:
# ... "arguments":{"binding_id":"'$BID'"} ...
```

`mount_point` must stay under `AI_CLOUDHUB_WORKSPACE` (path jail). Probe hits `POST /v1/drives/{id}/session` or `POST /v1/bindings/{id}/session` — it does **not** start rclone.

### 7.3 What hubd needs

| Variable | Role |
|----------|------|
| `AI_CLOUDHUB_API` | Control plane base (e.g. `http://127.0.0.1:8080`) |
| `AI_CLOUDHUB_TOKEN` | Bearer (human or capable agent token with drive access) |
| `AI_CLOUDHUB_DEVICE_ID` | Must match binding `device_id` so hubd lists the right bindings |
| rclone on `PATH` | Hard requirement for hubd (see runtime check in `cmd/hubd`) |

```bash
# Install rclone if needed; then:
AI_CLOUDHUB_API=http://127.0.0.1:8080 \
AI_CLOUDHUB_TOKEN="$TOK" \
AI_CLOUDHUB_DEVICE_ID=laptop-1 \
./.bin/hubd
```

hubd polls bindings with `desired=mounted`, issues STS sessions, mounts via rclone, reports actual state. Windows: [WINDOWS.md](./WINDOWS.md). Cloud exec without local FUSE: `./.bin/runner` with `AI_CLOUDHUB_DRIVE_ID` (still **your** machine — D-001).

Equivalent HTTP for session (without MCP):

```bash
"${CURL[@]}" -X POST "$API/v1/bindings/$BID/session" \
  -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -d '{}'
```

---

## 8. Policy / OPA (optional one-liner)

Built-in checks always apply: token **scopes**, agent **`allowed_drive_ids`**, optional path prefixes.

Optional file / Rego:

```bash
# JSON rules
export AI_CLOUDHUB_POLICY_FILE=./protocols/policy.example.json

# and/or OPA/Rego (data.aicloudhub.authz.allow)
export AI_CLOUDHUB_OPA_POLICY_FILE=./protocols/aicloudhub.rego.example
# export AI_CLOUDHUB_OPA_OBSERVE=1   # log would-deny without blocking

./.bin/api
```

See [POLICY.md](./POLICY.md). Smoke: `make smoke-policy`.

---

## 9. Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| **403** `missing scope: …` | Agent token lacks tool/route scope (e.g. no `job.run` for jobs, no `provider.read` for providers) | Re-mint with required scopes; set `default_scopes` on the agent. MCP tools declare `required_scopes_any` |
| **403** `drive not allowed for agent` | Drive id not in `allowed_drive_ids` (empty allowlist ⇒ no drives) | `PATCH/PUT /v1/agents/{id}` with `allowed_drive_ids: ["…"]` including the drive; re-mint if needed |
| **403** on second drive / binding | Allowlist filter (see `scripts/smoke-agent.sh`) | Only list/access allowed drives; human creates resources, agent consumes allowlisted ones |
| **403** `human session required` | Agent token used on agent **management** APIs (`POST /v1/agents`, mint for others, devices, …) | Use human login token for admin/CRUD of agents |
| MCP `mount_point jail` / `resolve_path` `allowed: false` | Path escapes `AI_CLOUDHUB_WORKSPACE` (default `/workspace`) | Keep paths under workspace; do not pass `/etc/passwd` etc. |
| hubd exits: rclone required | No rclone / runtimeenv fail | Install rclone; Windows also WinFsp for mount mode ([WINDOWS.md](./WINDOWS.md)) |
| hubd never mounts | Binding `device_id` ≠ `AI_CLOUDHUB_DEVICE_ID`, or `desired` not `mounted` | Align device id; set binding desired; check API with human token |
| `list_objects` / MinIO presign fails with 5xx | Dummy credentials / MinIO down | Expected offline; live: `make smoke-minio`. **Qiniu** `presign-get` works offline (HMAC) |
| Agent cannot see a second drive | Not in `allowed_drive_ids` | Add drive id to allowlist, re-mint token |
| API refuses to start under STRICT | Weak `JWT_SECRET` / missing master key | Local: leave `AI_CLOUDHUB_STRICT` unset; prod: strong secrets per [PRODUCTION.md](./PRODUCTION.md) |

One-command regression of this doc:

```bash
make smoke-quickstart-agent
```

---

## Checklist (~30 min)

1. `make build` (`CGO_ENABLED=0`)  
2. API up: SQLite + `JWT_SECRET`, no STRICT  
3. Register/login → provider → drive  
4. Agent with scopes + `allowed_drive_ids`  
5. `POST …/agents/{id}/token`  
6. `.bin/mcp` → `whoami` / `list_drives` / objects / jobs sketch  
7. Optional: binding + `ensure_mounted_hint` + hubd with `API`/`TOKEN`/`DEVICE_ID`  
8. Optional: policy file / OPA one-liner  

## See also

| Doc / script | Role |
|--------------|------|
| [MCP.md](./MCP.md) | Full tool list, env, security |
| [POLICY.md](./POLICY.md) | Scopes, allowlist, JSON, OPA |
| [PRODUCTION.md](./PRODUCTION.md) | STRICT, keys, compose |
| [STS.md](./STS.md) | Session / vendor STS |
| [CLOUD-INTEGRATION.md](./CLOUD-INTEGRATION.md) | OSS/COS/Qiniu/OCI runbooks |
| `scripts/smoke-quickstart-agent.sh` | End-to-end of this guide |
| `scripts/smoke-agent.sh` | Agent allowlist + scopes |
| `scripts/smoke-mcp-jobs.sh` | MCP job + provider tools |
| `make smoke-quickstart-agent` / `smoke-agent` / `smoke-mcp` | Regression |
| `cmd/mcp` | Stdio JSON-RPC helper |
| `cmd/hubd` | Local mount reconciler |
| OpenAPI | [openapi.yaml](./openapi.yaml) |
