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
GET /v1/jobs/stats
```

`stats` returns counts: `pending`, `dispatched`, `running`, `succeeded`, `failed`, `cancelled`, `total`.

## Webhook (optional)

```bash
export AI_CLOUDHUB_JOB_WEBHOOK_URL=https://hooks.example.com/jobs
export AI_CLOUDHUB_JOB_WEBHOOK_SECRET=whsec_xxx
```

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

Verify: `job.VerifyJobWebhookSignature`. Delivery is **best-effort**, not a durable outbox.

## Metrics

See [METRICS.md](./METRICS.md): created/claimed/completed/cancelled, timeout, lease_reclaim, max_attempts, heartbeat, webhook_ok.

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
```

- Cross-user listing; `limit` default 100, max 500.
- **Keyset pagination**: order `created_at DESC, id DESC`. Response may include `next_cursor`; pass as `?cursor=` for the next page (opaque; invalid cursor is treated as first page).
- Global `stats` without `user_id` counts **up to 500 newest** jobs only (honest cap).
- **Admin cancel** any non-terminal job (owner-agnostic); runner still detects via cancel poll.
- Note append: `admin cancel: <note>` when body note set; cancel is idempotent if already cancelled.
- **Admin release** returns `running`/`dispatched` → `pending` (`released: admin: …`) so another runner can claim (force requeue).
- Agent tokens cannot call admin APIs.
- Audit: `admin.jobs.list` / `stats` / `get` / `cancel` / `release`.

## Honesty

- Not a platform scheduler or multi-tenant runner fleet (D-001).
- Soft-refresh / open FUSE handles are hubd concerns ([WINDOWS.md](./WINDOWS.md)).
- Output is capped tails, not log shipping.
- Admin global stats are not a full warehouse aggregation.
