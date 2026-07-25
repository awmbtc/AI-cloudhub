# Changelog

## Unreleased

### Ops / BYOC
- Runner `AI_CLOUDHUB_MATERIALIZE_ONLY=1`: connector materialize without rclone (JSON report)
- `make smoke-byoc` — local bare git clone + postgres/mysql env inject
- `make prod-preflight` — production env checklist script
- PRODUCTION.md: Stage C/Stripe/PDP, BYOC cutover checklist

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

### MCP Stage C tools
- Tools: `list_marketplace`, `install_marketplace`, `list_memory`, `put_memory`, `search_memory`, `list_graph`, `link_graph`, `list_connectors`, `connectors_catalog`, `list_lineage`, `record_lineage`
- API tools always require `AI_CLOUDHUB_TOKEN` (local-only: `workspace_env`, `resolve_path`)
- Agents may install `skill` / `manifest`; `agent_template` remains human-only
- `make smoke-mcp` covers Stage C tools

### Payments
- Checkout returns `checkout_url` + `session_id` (mock without secret; live Session with `AI_CLOUDHUB_STRIPE_SECRET_KEY`)
- Optional `AI_CLOUDHUB_STRIPE_SUCCESS_URL` / `CANCEL_URL`
- Still PCI-free: no card data on control plane; webhook / dev pay complete purchase

### Connectors / Runner
- Connector `config` marshals as JSON object (`json.RawMessage`), not base64
- Postgres: expanded catalog fields; strip password/dsn; require host+database
- Runner materializes postgres → `AI_CLOUDHUB_PG_*` + password-less `DSN_TEMPLATE`
- Sandbox `PassLibpq` for host `PGPASSWORD` when postgres materializes
- `AI_CLOUDHUB_PG_STRICT` / `AI_CLOUDHUB_PASS_PG=0`
- Git materialization refactored into shared `materializeConnector`

### Docs
- PAYMENTS.md, CONNECTORS.md, STAGE-C.md, KNOWN_LIMITATIONS (remote PDP), ROADMAP C3b

## v0.2.2

Stage C deepen: marketplace skill/manifest install, install side effects, BYOC git clone notes.

## v0.2.1

Makefile `.PHONY` fix + ops docs pack (CLOUD-INTEGRATION, QUICKSTART-AGENT, METRICS).

## v0.2.0

2.0 control-plane close-out: agent identity, policy, multi-vendor STS, BYOS objects, production ops.
