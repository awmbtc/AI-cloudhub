# Changelog

## Unreleased

## v0.2.55

Production discipline (D-003 P1 — not Job ops):

- Stronger `scripts/prod-preflight.sh`: admin CIDR, HSTS, webhook URL+secret pairing, metrics probe, bind/PG password hints
- `docs/CUTOVER.md` one-page cutover runbook
- `make smoke-prod-preflight` self-test (weak FAIL / strong PASS)
- STRICT config warnings: metrics token, admin CIDRs, JWT prefer 32+
- compose prod default VERSION 0.2.55

## v0.2.54

Stage C Memory vertical (scoped project):

- `docs/STAGE-C-SCOPE-MEMORY.md` written scope (no hosted embedding)
- Memory TTL (`ttl_sec`), size/dim guards, search k honesty (max 50)
- DELETE ownership + audit/lineage; smoke-stage-c extended

## v0.2.53

Live MinIO golden path (real-user BYOS demo):

- `scripts/smoke-golden-minio.sh` + `make smoke-golden-minio` (soft-skip; REQUIRE=1 hard)
- `docs/GOLDEN-PATH.md` Live MinIO section; session + objects inventory + BYOC job

## v0.2.52

Job security hardening (D-003 allowed: security only):

- Cross-user isolation tests (service + HTTP)
- Agent `job.run` + drive allowlist HTTP tests
- Hard stdout/stderr ceiling 256 KiB; webhook secret non-leak tests
- `docs/JOBS.md` ## Security

## v0.2.51

Mainline close-out (not more Job ops):

- **D-003**: freeze default Job/Webhook admin deepening; product spine = P0–P3 + stage A/B + C-v0
- `docs/GOLDEN-PATH.md` + `make smoke-golden` (`scripts/smoke-golden.sh`)
- PROGRESS / README / AGENTS aligned to freeze + golden path

## v0.2.50

Ten-cut ship: region/runner list filters, dispatched gauge, cancel-all, webhook inflight, readyz ops, admin get webhooks, purge cascade, docs/smoke.

## v0.2.49

`PurgeTerminalJobs` cascades delete of related `job_webhook_outbox` rows.

## v0.2.48

`GET /v1/admin/jobs/{id}` includes `webhooks` outbox rows (job fields remain top-level).

## v0.2.47

Admin job list filters: `region`, `runner_id`.

## v0.2.46

`AI_CLOUDHUB_JOB_WEBHOOK_MAX_INFLIGHT` (default 1 serial; max 32) for outbox delivery parallelism.

## v0.2.45

`/readyz` includes `jobs_running` / `jobs_pending` / `webhook_outbox_*` when jobs wired.

## v0.2.44

`POST /v1/admin/jobs/cancel-all?user_id=&status=&limit=` batch admin cancel.

## v0.2.43

User job list `region=` / `runner_id=` store push-down filters.

## v0.2.42

Scrape gauge `aicloudhub_jobs_dispatched`.

## v0.2.41

User + admin list filter by `region_hint` / `claimed_by_runner_id` (store).

## v0.2.40

healthz includes live `jobs` + `webhook_outbox` snapshot counts.

## v0.2.39

Smoke/OpenAPI coverage for admin reclaim, complete, purge-terminal, webhook stats.

## v0.2.38

Indexes `idx_jobs_status` / `idx_jobs_status_updated` for reclaim and terminal purge.

## v0.2.37

Terminal job TTL purge (`AI_CLOUDHUB_JOB_RETAIN_SEC`, default off) + `POST /v1/admin/jobs/purge-terminal`.

## v0.2.36

`POST /v1/admin/jobs/reclaim?user_id=` global or per-user lease/timeout reclaim.

## v0.2.35

`POST /v1/admin/jobs/{id}/complete` force-complete any non-terminal job.

## v0.2.34

Background job maintenance worker: global reclaim + optional terminal purge (`AI_CLOUDHUB_JOB_RECLAIM_POLL_SEC`, default 30).

## v0.2.33

Scrape-time job status gauges: `jobs_pending` / `jobs_running` / `jobs_succeeded` / `jobs_failed` / `jobs_cancelled`.

## v0.2.32

`AI_CLOUDHUB_JOB_WEBHOOK_TIMEOUT_SEC` (default 5, max 120) for outbox HTTP client.

## v0.2.31

`GET /v1/admin/job-webhooks/stats` → pending/delivered/dead/total.

## v0.2.30

ReclaimStale scans only running jobs.

### Jobs
- Store `ListRunningJobs(userID)` on memory/sqlite/postgres
- `ReclaimStale` no longer `ListJobs` (all statuses) for lease/timeout reclaim

## v0.2.29

Admin job-webhooks filter by event type.

### Admin / Webhook
- `GET /v1/admin/job-webhooks?event=` and `POST …/retry-all?event=`
- Allowed: `job.succeeded` | `job.failed` | `job.cancelled` | other `job.*`
- Combines with status / job_id / user_id filters

## v0.2.28

Prometheus gauges for job webhook outbox queue depth.

### Metrics / Jobs
- Store `CountWebhookOutbox` (pending/delivered/dead) on memory/sqlite/postgres
- `GET /metrics` scrape-time refresh: `aicloudhub_jobs_webhook_pending` / `_delivered` / `_dead` gauges
- Complements lifetime counters `jobs_webhook_ok|fail|dead|purged_total`

## v0.2.27

User job list filter push-down to store.

### Jobs
- `ListJobsPage` on memory/sqlite/postgres: agent_id, claimed_by_agent_id, status, labels (JSON extract), keyset cursor + limit
- `GET /v1/jobs` List no longer loads all user jobs then filters in memory

## v0.2.26

Admin job-webhooks filter by `job_id` / `user_id`.

### Admin / Webhook
- `GET /v1/admin/job-webhooks` accepts `job_id` and `user_id` query filters
- `POST /v1/admin/job-webhooks/retry-all` accepts the same filters (scope batch requeue)
- Store indexes on `job_id` and `user_id`

## v0.2.25

Admin batch requeue for job webhook outbox.

### Admin / Webhook
- `POST /v1/admin/job-webhooks/retry-all?status=&limit=` (default status=dead, limit 100 max 500)
- Returns `{requeued, status, limit}`; kicks one delivery pass when any requeued
- Audit `admin.job_webhooks.retry_all`

## v0.2.24

Full job status count aggregation (no 500-row scan cap).

### Jobs / Admin
- Store `CountJobsByStatus` (`GROUP BY status`) on memory/sqlite/postgres
- `GET /v1/jobs/stats` and `GET /v1/admin/jobs/stats` use full aggregation
- Admin global stats no longer limited to newest 500 jobs

## v0.2.23

Job webhook outbox TTL purge for delivered/dead rows.

### Jobs / Webhook
- Auto-purge delivered (`delivered_at`) and dead (`updated_at`) older than `AI_CLOUDHUB_JOB_WEBHOOK_RETAIN_SEC` (default 7d; `0` disables)
- Worker interval `AI_CLOUDHUB_JOB_WEBHOOK_PURGE_SEC` (default 60)
- `POST /v1/admin/job-webhooks/purge?older_than_sec=` force purge (admin)
- Metric `jobs_webhook_purged_total`; pending rows never auto-purged

## v0.2.22

Admin list/get/retry for job webhook outbox.

### Admin / Jobs
- `GET /v1/admin/job-webhooks?status=&limit=` (status: pending|delivered|dead)
- `GET /v1/admin/job-webhooks/{id}` includes full envelope payload
- `POST /v1/admin/job-webhooks/{id}/retry` requeues (attempts=0, next now); works for dead and re-fire delivered
- Audit `admin.job_webhooks.list|get|retry`

## v0.2.21

Durable job webhook outbox with retry / dead-letter.

### Jobs / Webhook
- Terminal notifications enqueue `job_webhook_outbox` (memory/sqlite/postgres) instead of fire-and-forget
- Background worker (`StartWebhookWorker`) polls due rows; exponential backoff; max attempts → `dead`
- Env: `AI_CLOUDHUB_JOB_WEBHOOK_MAX_ATTEMPTS` (default 8), `AI_CLOUDHUB_JOB_WEBHOOK_POLL_SEC` (default 2)
- Metrics: `jobs_webhook_fail_total`, `jobs_webhook_dead_total` (+ existing `jobs_webhook_ok_total`)
- At-least-once delivery; receivers de-dupe on `event_id`

## v0.2.20

User job list keyset pagination (`GET /v1/jobs` cursor / next_cursor).

### Jobs
- `GET /v1/jobs` supports `limit` (default 100, max 500) and opaque `cursor`; response may include `next_cursor` / `count`
- Order: `created_at DESC, id DESC` (same keyset as admin list)
- `status=pending` claimable list still unbounded (no cursor); MCP `list_jobs` accepts `limit` / `cursor`

## v0.2.19

Admin jobs list keyset pagination (`cursor` / `next_cursor`).

### Admin / Jobs
- `GET /v1/admin/jobs` supports opaque `cursor` query; response may include `next_cursor`
- Order: `created_at DESC, id DESC` (stable pages across memory/sqlite/postgres)
- `limit` still default 100, max 500; service peeks limit+1 to detect next page

## v0.2.18

Admin force-release running jobs to pending.

### Admin / Jobs
- `POST /v1/admin/jobs/{id}/release` with optional `{note}` → pending + `released: admin: …`
- Clears claimer fields so another BYOC runner can claim
- Audit `admin.jobs.release`

## v0.2.17

Admin cancel any BYOC job.

### Admin / Jobs
- `POST /v1/admin/jobs/{id}/cancel` with optional `{note}` → append `admin cancel: …`
- Idempotent if already cancelled; runner still picks up via cancel poll
- Audit `admin.jobs.cancel`; metrics cancel counter

## v0.2.16

Admin cross-user job listing and stats.

### Admin / Jobs
- `GET /v1/admin/jobs` with `user_id`, `status`, `limit` (default 100, max 500)
- `GET /v1/admin/jobs/stats` (optional `user_id`; global capped at 500 newest)
- `GET /v1/admin/jobs/{id}` any job by id
- Store: `ListJobsAdmin`, `GetJobByID`; audit `admin.jobs.*`
- docs/JOBS.md admin section

## v0.2.15

Idempotency conflict (409), job stats, JOBS runbook.

### Jobs
- Same `idempotency_key` + different payload → HTTP 409; same payload → 200 replay / 201 create
- `GET /v1/jobs/stats` per-status counts; MCP `job_stats`
- docs/JOBS.md operational runbook

## v0.2.14

Job create idempotency and runner cancel detection.

### Jobs
- `idempotency_key` on create (unique per user; replay returns same job)
- Runner polls job status while running; remote cancel kills agent (`AI_CLOUDHUB_CANCEL_POLL`)
- Complete is no-op when job already terminal (incl. cancelled)

## v0.2.13

Job labels, region-aware claim, webhook event envelope.

### Jobs
- `labels` map on create; list filter via repeated `label=key:value`
- Claim next region filter: `X-AI-Cloudhub-Region` / body `region` / runner `AI_CLOUDHUB_REGION`
- Webhook body is envelope `{event_id,event,occurred_at,job}` with Event-Id / Event headers

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
