# Prometheus metrics

AI-cloudhub exposes a **lightweight, process-local** Prometheus text exposition at `GET /metrics`.  
Counters live in memory (`internal/metrics/metrics.go`); there is **no** official Prometheus client library, no histograms, and no multi-instance aggregation. On restart, all values reset to zero.

Source of truth: [`internal/metrics/metrics.go`](../internal/metrics/metrics.go). Document only what that file exports.

## Scraping `/metrics`

| Item | Value |
|------|--------|
| Path | `GET /metrics` |
| Content-Type | `text/plain; version=0.0.4; charset=utf-8` |
| Auth (optional) | `AI_CLOUDHUB_METRICS_TOKEN` |
| Default | **Open** (no auth) if the env var is unset |

### Token gate

When `AI_CLOUDHUB_METRICS_TOKEN` is set, scrapers must present one of:

```http
Authorization: Bearer <token>
```

or

```http
GET /metrics?token=<token>
```

Otherwise the handler returns `401` with `metrics token required`.  
Production checklist: [PRODUCTION.md](./PRODUCTION.md) · example Grafana stack: [deploy/grafana/](../deploy/grafana/).

### Manual checks

```bash
# Open scrape (dev)
curl -sS http://127.0.0.1:8080/metrics

# Gated scrape
export AI_CLOUDHUB_METRICS_TOKEN=secret
curl -sS -H "Authorization: Bearer $AI_CLOUDHUB_METRICS_TOKEN" \
  http://127.0.0.1:8080/metrics
# or
curl -sS "http://127.0.0.1:8080/metrics?token=$AI_CLOUDHUB_METRICS_TOKEN"
```

Prefer Bearer over `?token=` so tokens do not land in access logs.

## Metric table

Most series are **counters** (monotonic within a process lifetime). Webhook outbox depth series are **gauges** refreshed on each scrape when the jobs service is wired.

| Metric | Type | Labels | Meaning |
|--------|------|--------|---------|
| `aicloudhub_http_requests_total` | counter | — | HTTP hits that pass the server’s request counter path (authenticated + public tracked hits) |
| `aicloudhub_sessions_issued_total` | counter | — | Mount / STS sessions issued (drive session + refresh paths that call `IncSession`) |
| `aicloudhub_sts_source_total` | counter | `source` | Sessions broken down by STS credential source (see labels below) |
| `aicloudhub_jobs_created_total` | counter | — | BYOC jobs created |
| `aicloudhub_jobs_claimed_total` | counter | — | BYOC jobs claimed by a user runner |
| `aicloudhub_jobs_completed_total` | counter | — | BYOC jobs completed |
| `aicloudhub_jobs_cancelled_total` | counter | — | BYOC jobs cancelled |
| `aicloudhub_jobs_with_connector_created_total` | counter | — | Jobs created with non-empty `connector_id` |
| `aicloudhub_jobs_completed_with_connector_total` | counter | — | Completions for jobs that had `connector_id` |
| `aicloudhub_jobs_timeout_total` | counter | — | Jobs failed by hard wall-clock timeout |
| `aicloudhub_jobs_lease_reclaim_total` | counter | — | Running jobs released to pending after lease expiry |
| `aicloudhub_jobs_max_attempts_total` | counter | — | Jobs failed when lease expired and `max_attempts` reached |
| `aicloudhub_jobs_heartbeat_total` | counter | — | Successful job lease heartbeats |
| `aicloudhub_jobs_webhook_ok_total` | counter | — | Terminal job webhooks delivered HTTP &lt;300 (durable outbox) |
| `aicloudhub_jobs_webhook_fail_total` | counter | — | Failed outbox delivery attempts (will retry if under max) |
| `aicloudhub_jobs_webhook_dead_total` | counter | — | Outbox rows marked dead after max attempts (lifetime counter) |
| `aicloudhub_jobs_webhook_purged_total` | counter | — | Delivered/dead outbox rows deleted by TTL purge |
| `aicloudhub_jobs_webhook_pending` | gauge | — | Current pending outbox rows (queue depth; scrape-time `CountWebhookOutbox`) |
| `aicloudhub_jobs_webhook_delivered` | gauge | — | Current delivered outbox rows not yet purged |
| `aicloudhub_jobs_webhook_dead` | gauge | — | Current dead-letter outbox rows not yet purged |
| `aicloudhub_rate_limited_total` | counter | — | Requests rejected by rate limiting |
| `aicloudhub_snapshots_created_total` | counter | — | Metadata snapshots created |
| `aicloudhub_marketplace_installs_total` | counter | — | Successful marketplace installs |
| `aicloudhub_marketplace_checkouts_total` | counter | — | Successful checkouts |
| `aicloudhub_marketplace_paid_total` | counter | — | Purchases marked paid (pay stub or Stripe webhook) |
| `aicloudhub_connectors_created_total` | counter | — | Connector bindings registered |
| `aicloudhub_memory_puts_total` | counter | — | Memory Kernel puts via API |
| `aicloudhub_memory_searches_total` | counter | — | Vector memory searches |

### Webhook outbox gauges

On each `GET /metrics`, when the jobs service is available, the server calls `store.CountWebhookOutbox()` and sets:

- `aicloudhub_jobs_webhook_pending`
- `aicloudhub_jobs_webhook_delivered`
- `aicloudhub_jobs_webhook_dead`

These are **current row counts** in `job_webhook_outbox` (not rates). They fall as rows are delivered/purged. If jobs is not wired, gauges stay at last stored value (typically `0` at process start).

### `aicloudhub_sts_source_total` label values

| `source` | When |
|----------|------|
| `embedded` | Default / embedded credentials (or unknown source string) |
| `refresh` | Session refresh path |
| `minio_sts` | MinIO AssumeRole-style STS |
| `aws_sts` | AWS STS |
| `s3_sts` | Generic S3-compatible AssumeRole (non-minio / non-aws) |
| `aliyun_sts` | Aliyun RAM STS |
| `tencent_sts` | Tencent CAM STS |
| `qiniu_sts` | Qiniu S3-compat STS |
| `oracle_sts` | Oracle S3-compat STS |
| `qiniu_download` | Qiniu native HMAC download token assist |
| `oci_iam` | OCI API-key IAM validate path |
| `oci_par` | OCI ObjectRead PAR sample assist |
| `oci_secret` | OCI Customer Secret Key mint assist |

Offline STS path smoke: `make smoke-sts` (`scripts/smoke-sts.sh`).

Every known source series is always emitted (value may be `0`). Unknown sources increment `embedded`.

### Honesty / sparseness

What exists today is intentionally small:

- **No** request latency / duration histograms  
- **No** per-route or per-status HTTP labels  
- **No** Go runtime / process metrics from `prometheus/client_golang`  
- **No** multi-replica shared counters (each API process has its own set)  
- **No** gauges for open sessions or mount health (webhook outbox depth gauges are the exception)  

Use these counters for coarse traffic and STS-source mix; pair with logs/audit for detail. Webhook backlog: `aicloudhub_jobs_webhook_pending` (and dead/delivered for residual terminal rows).

## Example Prometheus scrape config

See also [`deploy/grafana/prometheus.yml.example`](../deploy/grafana/prometheus.yml.example).

```yaml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: aicloudhub
    metrics_path: /metrics
    static_configs:
      - targets: ["127.0.0.1:8080"]
        labels:
          service: aicloudhub-api
    # When AI_CLOUDHUB_METRICS_TOKEN is set:
    authorization:
      type: Bearer
      credentials: "<same token as AI_CLOUDHUB_METRICS_TOKEN>"
    # Alternative (avoid if logs capture URLs):
    # params:
    #   token: ["<token>"]
```

Behind a reverse proxy, scrape the internal address (loopback / Docker network), not the public TLS edge, unless the edge is intentionally exposing `/metrics` only to a private network.

## Example Grafana / PromQL queries

Job name / instance labels depend on your scrape config. Examples assume `job="aicloudhub"`.

### STS source rates (sessions/sec by source)

```promql
sum by (source) (rate(aicloudhub_sts_source_total[5m]))
```

Absolute totals:

```promql
aicloudhub_sts_source_total
```

Share of one source (e.g. AWS):

```promql
sum(rate(aicloudhub_sts_source_total{source="aws_sts"}[5m]))
/
clamp_min(sum(rate(aicloudhub_sts_source_total[5m])), 1e-9)
```

### Sessions issued

```promql
rate(aicloudhub_sessions_issued_total[5m])
```

```promql
increase(aicloudhub_sessions_issued_total[1h])
```

### HTTP-ish counter

```promql
rate(aicloudhub_http_requests_total[5m])
```

```promql
increase(aicloudhub_http_requests_total[1h])
```

### Job counters (BYOC)

```promql
rate(aicloudhub_jobs_created_total[5m])
```

```promql
rate(aicloudhub_jobs_claimed_total[5m])
```

```promql
rate(aicloudhub_jobs_completed_total[5m])
```

```promql
rate(aicloudhub_jobs_cancelled_total[5m])
```

Throughput snapshot (created vs completed):

```promql
rate(aicloudhub_jobs_created_total[5m])
- rate(aicloudhub_jobs_completed_total[5m])
```

### Job webhook outbox depth

```promql
aicloudhub_jobs_webhook_pending
```

```promql
aicloudhub_jobs_webhook_dead
```

```promql
aicloudhub_jobs_webhook_delivered
```

Delivery success/fail rates (counters):

```promql
rate(aicloudhub_jobs_webhook_ok_total[5m])
```

```promql
rate(aicloudhub_jobs_webhook_fail_total[5m])
```

### Rate limits & snapshots

```promql
rate(aicloudhub_rate_limited_total[5m])
```

```promql
rate(aicloudhub_snapshots_created_total[5m])
```

## Importable dashboard

Pre-built panels: [`deploy/grafana/dashboard.json`](../deploy/grafana/dashboard.json).  
Import steps: [`deploy/grafana/README.md`](../deploy/grafana/README.md).

## Related

- OpenAPI: `GET /metrics` in [openapi.yaml](./openapi.yaml)  
- STS sources: [STS.md](./STS.md)  
- Production token: [PRODUCTION.md](./PRODUCTION.md)  
