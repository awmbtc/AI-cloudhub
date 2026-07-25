# Changelog

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
