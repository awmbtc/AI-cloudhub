# BYOC Jobs runbook

Honest control-plane job queue for **user-owned runners** only (D-001: no platform runner pool).

## Lifecycle

```text
pending ──claim──► running ──complete──► succeeded | failed
   ▲                  │
   │                  ├──cancel──► cancelled
   │                  │
   └──lease reclaim───┘  (or fail: timeout / max_attempts)
```

| Status | Meaning |
|--------|---------|
| `pending` / `dispatched` | Claimable |
| `running` | Claimed; needs heartbeat if lease enabled |
| `succeeded` / `failed` / `cancelled` | Terminal |

## Create

```http
POST /v1/jobs
{
  "drive_id": "...",
  "command": ["echo", "hi"],
  "mode": "sync_workspace",
  "region_hint": "us-east",
  "priority": 10,
  "timeout_sec": 3600,
  "max_attempts": 3,
  "labels": {"env": "prod"},
  "idempotency_key": "client-req-1",
  "connector_id": "..."
}
```

| Field | Notes |
|-------|--------|
| `priority` | Higher claimed first (±1000 clamp) |
| `timeout_sec` | Wall clock from `claimed_at`; API fail exit 124; runner `CommandContext` |
| `max_attempts` | After N claims, lease expiry fails job |
| `labels` | Max 16 keys; filter with `GET /v1/jobs?label=env:prod` |
| `idempotency_key` | Unique per user. Same key + same payload → **200** same job. Same key + different payload → **409**. New key → **201**. |
| `region_hint` | Matched by claim region filter |

Global defaults: `AI_CLOUDHUB_JOB_TIMEOUT_SEC`, `AI_CLOUDHUB_JOB_LEASE_SEC` (default 300), `AI_CLOUDHUB_JOB_OUTPUT_MAX` (8192).

## Claim / heartbeat / complete

```http
POST /v1/jobs/next/claim
X-AI-Cloudhub-Runner-Id: gpu-1
X-AI-Cloudhub-Region: us-east

POST /v1/jobs/{id}/heartbeat
POST /v1/jobs/{id}/complete
{ "ok": true, "exit_code": 0, "duration_ms": 12, "stdout": "...", "stderr": "...",
  "stdout_truncated": false, "stderr_truncated": false }
```

- Claim order: **priority DESC**, then `created_at ASC`.
- Region header/body / runner `AI_CLOUDHUB_REGION`: only jobs with matching `region_hint`.
- Runner identity: `AI_CLOUDHUB_RUNNER_ID` or hostname → `claimed_by_runner_id`.
- Lease: no heartbeat within `AI_CLOUDHUB_JOB_LEASE_SEC` → release to pending (or max_attempts fail).
- Cancel: `POST /v1/jobs/{id}/cancel`. Runner polls GET job (`AI_CLOUDHUB_CANCEL_POLL`, default 5s) and kills agent.
- Complete on already-terminal job is a **no-op** (keeps cancelled/succeeded/failed).

## List / stats

```http
GET /v1/jobs?status=succeeded
GET /v1/jobs?label=env:prod&label=team:ml
GET /v1/jobs?status=pending&region=us-east
GET /v1/jobs?limit=50&cursor=
GET /v1/jobs/stats
```

- Non-`pending` list: keyset pagination (`created_at DESC, id DESC`); `limit` default 100, max 500; response may include `next_cursor` / `count` / `limit`.
- Filters (`status`, `agent_id`, `claimed_by_agent_id`, `label=`) and cursor are applied in the store (not full-table scan + in-memory filter).
- `status=pending` remains the claimable set (pending+dispatched), no cursor (full claimable list + optional `region`).
- `stats` returns counts: `pending`, `dispatched`, `running`, `succeeded`, `failed`, `cancelled`, `total`.

## Webhook (optional, durable outbox)

```bash
export AI_CLOUDHUB_JOB_WEBHOOK_URL=https://hooks.example.com/jobs
export AI_CLOUDHUB_JOB_WEBHOOK_SECRET=whsec_xxx          # optional HMAC
export AI_CLOUDHUB_JOB_WEBHOOK_MAX_ATTEMPTS=8            # default 8, max 32
export AI_CLOUDHUB_JOB_WEBHOOK_POLL_SEC=2                # outbox worker poll
export AI_CLOUDHUB_JOB_WEBHOOK_RETAIN_SEC=604800         # keep delivered/dead 7d; 0=never purge
export AI_CLOUDHUB_JOB_WEBHOOK_PURGE_SEC=60              # worker purge interval
# AI_CLOUDHUB_JOB_WEBHOOK_BACKOFF_SEC=0                  # tests only: ~1ms retry
```

On terminal status (`succeeded` / `failed` / `cancelled`), the control plane **enqueues** a row in `job_webhook_outbox` and a background worker delivers at-least-once (survives API restart). Receivers should de-dupe on `event_id`.

**Retention**: delivered (by `delivered_at`) and dead (by `updated_at`) rows older than `RETAIN_SEC` are deleted by the worker (and via admin purge). Pending rows are never auto-purged.

Body:

```json
{
  "event_id": "uuid",
  "event": "job.succeeded",
  "occurred_at": "...",
  "job": { ... }
}
```

Headers: `X-AI-Cloudhub-Event-Id`, `X-AI-Cloudhub-Event`, `X-AI-Cloudhub-Timestamp`, optional `X-AI-Cloudhub-Signature: sha256=<hex>` over `timestamp + "." + body`.

Verify: `job.VerifyJobWebhookSignature`. Failed attempts use exponential backoff (5s → 4h); after max attempts the row is marked `dead` (metrics `webhook_dead`).

## Metrics

See [METRICS.md](./METRICS.md): created/claimed/completed/cancelled, timeout, lease_reclaim, max_attempts, heartbeat, webhook_ok / webhook_fail / webhook_dead.

## MCP

Tools: `create_job`, `list_jobs`, `get_job`, `claim_next_job`, `complete_job`, `heartbeat_job`, `cancel_job` — [MCP.md](./MCP.md).

## Smoke

```bash
make smoke-job
make smoke-mcp
```

## Admin (role=admin, human session only)

```http
GET  /v1/admin/jobs?user_id=&status=&limit=&cursor=
GET  /v1/admin/jobs/stats?user_id=
GET  /v1/admin/jobs/{id}
POST /v1/admin/jobs/{id}/cancel
{ "note": "optional reason" }

POST /v1/admin/jobs/{id}/release
{ "note": "optional reason" }

GET  /v1/admin/job-webhooks?status=&job_id=&user_id=&limit=
GET  /v1/admin/job-webhooks/{id}
POST /v1/admin/job-webhooks/{id}/retry
POST /v1/admin/job-webhooks/retry-all?status=dead&job_id=&user_id=&limit=
POST /v1/admin/job-webhooks/purge?older_than_sec=
```

- Cross-user listing; `limit` default 100, max 500.
- **Keyset pagination**: order `created_at DESC, id DESC`. Response may include `next_cursor`; pass as `?cursor=` for the next page (opaque; invalid cursor is treated as first page).
- Global/user `stats` use full `COUNT … GROUP BY status` aggregation (no row scan cap).
- **Admin cancel** any non-terminal job (owner-agnostic); runner still detects via cancel poll.
- Note append: `admin cancel: <note>` when body note set; cancel is idempotent if already cancelled.
- **Admin release** returns `running`/`dispatched` → `pending` (`released: admin: …`) so another runner can claim (force requeue).
- **Admin job-webhooks**: list/get outbox; filters `status` / `job_id` / `user_id`; retry requeues any row (same `event_id`/`payload`, attempts=0); **retry-all** batch requeue (default `status=dead`, same filters, limit default 100 max 500); **purge** deletes old delivered/dead (`older_than_sec` optional).
- Agent tokens cannot call admin APIs.
- Audit: `admin.jobs.*` / `admin.job_webhooks.list|get|retry|retry_all|purge`.

## Honesty

- Not a platform scheduler or multi-tenant runner fleet (D-001).
- Soft-refresh / open FUSE handles are hubd concerns ([WINDOWS.md](./WINDOWS.md)).
- Output is capped tails, not log shipping.
- Admin/user job stats are SQL-style status counts, not time-series warehouse analytics.
