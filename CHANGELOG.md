# Changelog

## Unreleased

### Connectors
- MySQL BYOC env materializer (parity with postgres): `AI_CLOUDHUB_MYSQL_*`, host `MYSQL_PWD` via sandbox `PassMysql`
- `AI_CLOUDHUB_MYSQL_STRICT` / `AI_CLOUDHUB_PASS_MYSQL=0`

### OpenAPI
- Stage C paths and schemas: memory, marketplace, checkout_url, connectors, lineage, graph, modules, purchases, Stripe webhook
- Job `connector_id` documented

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

### Marketplace
- `POST /v1/marketplace/{id}/install` supports `agent_template` | `skill` | `manifest`
- `InstallSkill` / `InstallManifest`: paid gate via `HasPaidAccess`; no agent create; returns payload + `memory_id`
- Install side effects: episodic memory, identity graph, lineage (agent edges only for templates)
- Paid install still requires purchase `status=paid`

### Jobs / Runner (BYOC)
- Job `Complete` **appends** notes (preserves D-001 create trail; cap 2000)
- Jobs: `connector_id`; runner claim sets `AI_CLOUDHUB_CONNECTOR_ID`
- Git clone success → note `cloned to <path>` + env `AI_CLOUDHUB_CLONE_PATH`
- Git clone fail → note `clone failed: …` (always recorded)
- `AI_CLOUDHUB_CLONE_STRICT=1|true|yes` → fail job on clone error (default soft continue)

### Payments / Stripe
- Webhook signature verify (`AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET`)
- Checkout returns `stripe_metadata` for Checkout Session

### Stage C foundations (carried from Unreleased)
- Remote PDP, OCI PAR/secret STS sources
- Memory Kernel + vector search; Identity Graph; Data Lineage
- Connectors catalog; modular compose (api replicas, not runner pool)
- `make smoke-stage-c`

### Docs
- MARKETPLACE.md, CONNECTORS.md, STAGE-C.md, PAYMENTS.md, PROGRESS.md

## v0.2.1

### Fixes
- fix: Makefile `.PHONY` line break — missing newline glued `smoke-all` and `all:` targets, causing GNU make "multiple target patterns" and breaking CI `make smoke-all`

### Docs (ops pack)
- [docs/CLOUD-INTEGRATION.md](docs/CLOUD-INTEGRATION.md) — OSS / COS / Qiniu / OCI copy-paste runbooks
- [docs/QUICKSTART-AGENT.md](docs/QUICKSTART-AGENT.md) — agent token + MCP + hubd
- [docs/METRICS.md](docs/METRICS.md) + [deploy/grafana/](deploy/grafana/)
- `make smoke-quickstart-agent`

## v0.2.0

2.0 control-plane close-out: agent identity, policy (JSON + optional OPA), multi-vendor STS, BYOS objects, production ops, multi-arch releases.
