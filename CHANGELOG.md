# Changelog

## Unreleased

## v0.2.12

Job priority queue, runner identity on claim, webhook HMAC.

### Jobs
- `priority` (higher first, then FIFO); create + claim sort
- `claimed_by_runner_id` via `X-AI-Cloudhub-Runner-Id` or body `runner_id`
- Runner sets identity from `AI_CLOUDHUB_RUNNER_ID` or hostname
- Webhook HMAC: `AI_CLOUDHUB_JOB_WEBHOOK_SECRET` signs `timestamp.body` → `X-AI-Cloudhub-Signature`

## v0.2.11

Job attempt budget, ops metrics, optional terminal webhook.

### Jobs
- `attempt_count` increments on claim; `max_attempts` fails on lease expiry when budget exhausted
- Metrics: `jobs_timeout_total`, `jobs_lease_reclaim_total`, `jobs_max_attempts_total`, `jobs_heartbeat_total`, `jobs_webhook_ok_total`
- Optional `AI_CLOUDHUB_JOB_WEBHOOK_URL` async POST of job JSON on terminal status

## v0.2.10

Job hard timeout, output truncation flags, hubd mount path probe + force remount.

### Jobs
- `timeout_sec` on create; `claimed_at` on claim; hard fail (exit 124) on claim path when overdue
- Global `AI_CLOUDHUB_JOB_TIMEOUT_SEC`; runner `CommandContext` kill
- `stdout_truncated` / `stderr_truncated` on complete (API cap and runner limitedBuffer)

### hubd / Windows
- Mount path liveness: `ReadDir` timeout → error + remount
- `AI_CLOUDHUB_FORCE_REMOUNT_ON_REFRESH=1` full remount instead of soft conf rewrite
- Windows stop uses Kill; Linux stop fusermount/umount best-effort

## v0.2.9

Job process output on complete, list status filters, CI live STS.

### Jobs
- `stdout` / `stderr` on Job + complete body (tail-capped; default 8KiB, `AI_CLOUDHUB_JOB_OUTPUT_MAX`)
- Runner captures agent streams (mirrors to process + complete payload)
- `GET /v1/jobs?status=` exact match for running|succeeded|failed|cancelled|dispatched (`pending` still = claimable set)

### MCP
- `complete_job` accepts `stdout` / `stderr`
- `list_jobs` status filter docs

### CI
- `smoke-sts-live` job: MinIO + `AI_CLOUDHUB_SMOKE_STS_LIVE=1` + REQUIRE

## v0.2.8

Job structured complete + lease/heartbeat for BYOC runners; STS live smoke opt-in.

### Jobs
- Structured complete: `exit_code` (nullable), `duration_ms` on Job + complete body
- Soft migrate sqlite/postgres; runner reports exit + wall duration
- Lease: `heartbeat_at`, `POST /v1/jobs/{id}/heartbeat`, reclaim stale running → pending
- `AI_CLOUDHUB_JOB_LEASE_SEC` (default 300; `0` disables); runner `AI_CLOUDHUB_HEARTBEAT` interval

### MCP
- `get_job` tool (`GET /v1/jobs/{id}`)
- `heartbeat_job` tool

### STS
- Phase L live MinIO STS path when `AI_CLOUDHUB_SMOKE_STS_LIVE=1` (docker auto-start or MINIO_ENDPOINT)

## v0.2.7

hubd mode-from-binding and post-mount process health.

### hubd
- Mode resolution: binding.mode → session.mode → AI_CLOUDHUB_MODE → mount
- Detect dead rclone mount process → report actual=error and remount
- Remount when binding.mode changes while active
- Unit tests: `go test ./cmd/hubd`
- Docs: WINDOWS.md, KNOWN_LIMITATIONS soft-refresh honesty

## v0.2.6

STS offline 联调 smoke, Windows hubd hardening, Stage C observability counters.

### STS 联调
- `make smoke-sts` / `scripts/smoke-sts.sh` — offline path selection + unit gate + optional live SKIP
- Docs: STS.md, CLOUD-INTEGRATION.md offline table
- Included in `make smoke-all` / CI

### hubd Windows
- WinFsp missing → refuse `mode=mount` with install/sync_workspace hint
- Drive-letter mount points: no MkdirAll; sync_workspace requires directory path
- rclone discovery under common Windows install paths
- rclone mount fail-fast if process exits immediately
- `install-deps.ps1 -CheckOnly`, `scripts/windows/smoke-windows.ps1`, WINDOWS.md checklist

### Observability
- Counters: marketplace install/checkout/paid, connectors created, memory put/search, jobs with connector
- Grafana Stage C row; METRICS.md + STS labels `oci_par`/`oci_secret`
- `smoke-stage-c` asserts Stage C metric series

## v0.2.5

Ops: BYOC connector 联调 smoke + production preflight.

### Runner / BYOC
- `AI_CLOUDHUB_MATERIALIZE_ONLY=1` — materialize git/postgres/mysql without rclone
- Optional `AI_CLOUDHUB_MATERIALIZE_REPORT` JSON path
- `make smoke-byoc` — local bare git clone + PG/MySQL env inject + job `connector_id`

### Production
- `make prod-preflight` / `scripts/prod-preflight.sh` — JWT/STRICT/MASTER_KEY checklist
- PRODUCTION.md: Stage C, Stripe, remote PDP, BYOC table, cutover checklist
- AGENTS.md smoke targets updated

## v0.2.4

Stage C contract + MCP write surface for connectors; MySQL BYOC.

### MCP
- `create_connector` / `get_connector` / `delete_connector` (create/delete require human session on API)
- `marketplace_checkout` → `checkout_url` + `stripe_metadata`
- Existing Stage C tools: memory, marketplace install, graph, lineage, list connectors

### Connectors
- MySQL BYOC env materializer: `AI_CLOUDHUB_MYSQL_*`, host `MYSQL_PWD` via sandbox `PassMysql`
- `AI_CLOUDHUB_MYSQL_STRICT` / `AI_CLOUDHUB_PASS_MYSQL=0`
- Postgres / git materializers unchanged

### OpenAPI
- Stage C paths and schemas: memory, marketplace, checkout_url, connectors, lineage, graph, modules, purchases, Stripe webhook
- Job `connector_id` documented

### Docs
- MCP.md, CONNECTORS.md, STAGE-C.md, PROGRESS.md

## v0.2.3

Stage C deepen: MCP tools, Stripe checkout_url, postgres BYOC env, config JSON fix.

## v0.2.2

Marketplace skill/manifest install, install side effects, BYOC git clone notes.

## v0.2.1

Makefile `.PHONY` fix + ops docs pack.

## v0.2.0

2.0 control-plane close-out.
