# Changelog

## Unreleased (Stage C)

### Policy
- Remote PDP: `AI_CLOUDHUB_PDP_URL` HTTP POST `{input}` → `{allow,reason}`; observe/strict/timeout; after OPA in evaluate chain

### OCI / STS
- `AI_CLOUDHUB_OCI_CREATE_SECRET=1`: best-effort Customer Secret Key mint via Identity API (`source=oci_secret`)
- `AI_CLOUDHUB_OCI_PAR=1` + `OCI_NAMESPACE` + `OCI_PAR_BUCKET`: ObjectRead PAR sample in session.note (`source=oci_par`)
- Metrics: `oci_par`, `oci_secret` STS sources

### Memory / Marketplace / modules
- Memory Kernel v0: `POST/GET/DELETE /v1/memory` (working|episodic|semantic)
- Vector search: `embedding` on put + `POST /v1/memory/search` (client vectors, cosine)
- Marketplace v0: system catalog + publish + install + `price_cents` checkout/pay stub
- `GET /v1/modules` logical module registry (monolith default; D-002)

### Lineage / Graph / Connectors / deploy
- Data Lineage v0: `/v1/lineage`
- Identity Graph v0: `/v1/graph`
- Connectors: Git/DB/SaaS catalog + bindings (`/v1/connectors*`)
- `deploy/docker-compose.modular.yml` multi-api replicas + edge (not a runner pool)

### Docs
- STAGE-C.md honesty table; ROADMAP C3e–h; D-001 reaffirmed

### Stage C follow-up
- Stripe webhook signature verify (`/v1/webhooks/stripe`, `AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET`)
- Checkout returns `stripe_metadata` for Checkout Session
- Runner: `AI_CLOUDHUB_CONNECTOR_ID` git shallow clone (BYOC)
- Paid marketplace `install` requires purchase status=paid
- Jobs: `connector_id` field; runner claim applies it; MCP create_job support
- `make smoke-stage-c`; CONNECTORS.md + PAYMENTS.md
- Install side effects: episodic memory + identity graph + lineage; response `memory_id`
- Job `Complete` **appends** note (preserves D-001); runner writes `cloned to <path>` + `AI_CLOUDHUB_CLONE_PATH`

## v0.2.1

### Fixes
- fix: Makefile `.PHONY` line break — missing newline glued `smoke-all` and `all:` targets, causing GNU make "multiple target patterns" and breaking CI `make smoke-all`

### Docs (ops pack)
- [docs/CLOUD-INTEGRATION.md](docs/CLOUD-INTEGRATION.md) — OSS / COS / Qiniu / OCI copy-paste runbooks (decision tree, RoleArn env, offline checks)
- [docs/QUICKSTART-AGENT.md](docs/QUICKSTART-AGENT.md) — agent token + MCP + hubd in ~30 minutes (verified on v0.2.1)
- [docs/METRICS.md](docs/METRICS.md) + [deploy/grafana/](deploy/grafana/) — Prometheus scrape + importable Grafana dashboard
- `make smoke-quickstart-agent` — regression for the agent quickstart path

## v0.2.0

2.0 control-plane close-out: agent identity, policy (JSON + optional OPA), multi-vendor STS, BYOS objects, production ops, multi-arch releases.

### STS / Policy
- Qiniu native private download tokens (`AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN`, source `qiniu_download`)
- Qiniu object-level signed GET via `POST …/objects/presign-get` (`method=qiniu_download`)
- OCI API-key IAM validation (`AI_CLOUDHUB_ORACLE_NATIVE_IAM` + private key env, source `oci_iam`) with short Identity cache
- Optional OPA/Rego (`AI_CLOUDHUB_OPA_POLICY_FILE`, query `data.aicloudhub.authz.allow`); smoke-policy covers OPA
- Multi-vendor STS best-effort: MinIO/AWS/S3-compat, Aliyun RAM, Tencent CAM, Qiniu/Oracle labels
- Fix: agent `drive.read` may POST object helpers (`presign-get` / `restore-plan` / `version-hint`); only `restore-version` needs write

### Agent / Jobs / MCP
- Stage A/B: Agent Identity + scopes + path jail; drive allowlist; Policy Engine + `AI_CLOUDHUB_POLICY_FILE`; Manifest 2.0; audit.agent_id; Snapshot v0
- Jobs: `agent_id` / `claimed_by_agent_id`, claim release on policy deny, list filters, job.* audit
- MCP: `list_jobs` / `create_job` / `claim_next_job` / `complete_job` / `cancel_job` + `list_providers` + object tools
- Binding agent gates; Devices API rejects agent tokens

### Security / Hardening
- Auth: bcrypt, refresh tokens, jti/token_version revoke, login lockout, register gate, password policy
- Quotas, admin audit filters, JWT/master-key strict config, security headers, metrics token, HSTS, Admin CIDRs
- Runtime: path jail, env filter, seccomp profiles, network deny wrappers

### Ops / CI / Release
- docs/PRODUCTION.md; prod compose requires `JWT_SECRET` / `MASTER_KEY`, STRICT; postgres/redis/api healthchecks
- Distroless API image (`-s -w`); alpine multi-binary; `.dockerignore`; nginx + Caddy TLS examples
- `api healthcheck` subcommand for distroless probes
- CI: `make smoke-all`, live MinIO smoke, Docker image healthz
- Multi-arch release on `v*` tags (`scripts/release-build.sh` / `make release-binaries`)

### Storage / Objects
- Objects inventory, presign-get, restore-plan/version; snapshots + diff
- PostgreSQL store; Redis shared rate limit; OpenAPI 0.2

## v0.1.1

### Security
- User passwords hashed with `golang.org/x/crypto/bcrypt` on register
- Login uses `bcrypt.CompareHashAndPassword`
- Legacy plaintext passwords upgraded to bcrypt on next successful login (`UpdateUserPassword`)

## v0.1.0 (architecture MVP complete)

- P0 STS/manifest/binding/hubd/runner; P1 sqlite/secretbox/ratelimit; vendor A/B/C templates
