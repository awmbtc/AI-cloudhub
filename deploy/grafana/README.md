# Grafana + Prometheus example (AI-cloudhub)

Importable dashboard and scrape snippet for the **sparse** counters exported by `GET /metrics`.  
Full metric list and PromQL: [docs/METRICS.md](../../docs/METRICS.md).

## Prerequisites

1. AI-cloudhub API listening (e.g. `:8080`).
2. Prometheus scraping `/metrics` (see [`prometheus.yml.example`](./prometheus.yml.example)).
3. Grafana with a Prometheus datasource that can query that Prometheus.

Optional production gate:

```bash
export AI_CLOUDHUB_METRICS_TOKEN="$(openssl rand -hex 16)"
# set the same value in Prometheus authorization.credentials
```

## Prometheus

1. Merge `prometheus.yml.example` into your Prometheus config (or use it as a standalone file).
2. Point `targets` at the API (host network, Docker DNS, or k8s Service).
3. If `AI_CLOUDHUB_METRICS_TOKEN` is set, enable the `authorization` block.
4. Reload Prometheus and confirm the `aicloudhub` job is **UP**.

Smoke:

```bash
curl -sS http://127.0.0.1:8080/metrics | head
# or with token:
# curl -sS -H "Authorization: Bearer $AI_CLOUDHUB_METRICS_TOKEN" http://127.0.0.1:8080/metrics | head
```

## Import dashboard

1. Grafana → **Dashboards** → **New** → **Import**.
2. Upload [`dashboard.json`](./dashboard.json) (or paste its contents).
3. Select your **Prometheus** datasource when prompted.
4. Open **AI-cloudhub / control plane**.

Panels use `job=~"$job"` (default `aicloudhub`) and optional `instance` multi-select.  
If your scrape job name differs, change the dashboard variable or Prometheus `job_name`.

## What you will see

Only counters that exist in code:

- HTTP request rate  
- Sessions issued  
- STS source rate by `source` label  
- BYOC job create / claim / complete / cancel  
- Rate-limited requests  
- Snapshots created  

No latency heatmaps, no per-route breakdown, no Go runtime metrics — those are not exported yet.

## Related

- [docs/METRICS.md](../../docs/METRICS.md)  
- [docs/PRODUCTION.md](../../docs/PRODUCTION.md)  
