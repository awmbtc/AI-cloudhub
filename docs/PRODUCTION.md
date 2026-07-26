# Production checklist (control plane)

AI-cloudhub control plane is thin: **auth, policy, STS/session, metadata, BYOS helpers**.  
Object bytes stay client↔your store. Jobs run only on **user** runners (D-001: no platform pool).

## Minimum security

| Item | Env / action | Notes |
|------|----------------|-------|
| Strong JWT | `JWT_SECRET` ≥ 16 chars, not default | Prefer 32+ random |
| Strict mode | `AI_CLOUDHUB_STRICT=1` | Rejects weak JWT / default secrets at boot |
| Master key | `AI_CLOUDHUB_MASTER_KEY` (e.g. `openssl rand -base64 32`) | Encrypts provider secrets at rest |
| Close public register | `AI_CLOUDHUB_ALLOW_REGISTER=0` | After bootstrap admin exists |
| Metrics gate | `AI_CLOUDHUB_METRICS_TOKEN` | Else `/metrics` is open; see [METRICS.md](./METRICS.md) + [deploy/grafana/](../deploy/grafana/) |
| Admin IP (optional) | `AI_CLOUDHUB_ADMIN_CIDRS` | e.g. `10.0.0.0/8,127.0.0.1` |
| Body limit | `AI_CLOUDHUB_MAX_BODY_BYTES` (default 1MiB) | — |
| HSTS (HTTPS only) | `AI_CLOUDHUB_HSTS=1` | Behind TLS terminator |

## Data plane

| Item | Env | Notes |
|------|-----|-------|
| DB | `AI_CLOUDHUB_DB=postgres://…` | SQLite is single-writer; multi-replica → Postgres |
| Redis rate limit | `AI_CLOUDHUB_REDIS=redis://…` | Multi-instance shared limiter |
| Listen | `HTTP_ADDR=:8080` | Put TLS / reverse proxy in front |

## Optional policy / STS / Stage C

| Feature | Env | Docs |
|---------|-----|------|
| JSON policy | `AI_CLOUDHUB_POLICY_FILE` (+ optional `POLICY_RELOAD_SEC`) | [POLICY.md](./POLICY.md) |
| OPA/Rego | `AI_CLOUDHUB_OPA_POLICY_FILE` | [POLICY.md](./POLICY.md) |
| OPA observe | `AI_CLOUDHUB_OPA_OBSERVE=1` | Log-would-deny without blocking |
| Remote PDP | `AI_CLOUDHUB_PDP_URL` | [POLICY.md](./POLICY.md) — your process; fail-open default |
| MinIO/AWS/S3 STS | `AI_CLOUDHUB_*_STS=1` | [STS.md](./STS.md) |
| Qiniu download assist | `AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN=1` | Session note; object GET via `presign-get` always for type=qiniu |
| OCI API-key check | `AI_CLOUDHUB_ORACLE_NATIVE_IAM=1` + OCI key env | Cache: `AI_CLOUDHUB_OCI_IAM_CACHE_SEC` |
| Stripe webhooks | `AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET` | [PAYMENTS.md](./PAYMENTS.md) |
| Live Checkout URL | `AI_CLOUDHUB_STRIPE_SECRET_KEY` (+ SUCCESS/CANCEL URLs) | mock URL if unset |

## Docker Compose (prod-ish)

API image is **distroless** (`deploy/Dockerfile`): static binary, nonroot, no shell.  
Prefer Postgres (compose default) so the container does not need a host bind for SQLite.

```bash
export JWT_SECRET="$(openssl rand -hex 32)"
export AI_CLOUDHUB_MASTER_KEY="$(openssl rand -base64 32)"
# optional
export AI_CLOUDHUB_METRICS_TOKEN="$(openssl rand -hex 16)"

docker compose -f deploy/docker-compose.prod.yml up -d --build
# or: make docker-api && docker run --rm -p 127.0.0.1:8080:8080 -e ...

curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
```

Compose enables `STRICT=1`, `ALLOW_REGISTER=0` after first user is created manually (or set register open once, create admin, then set `0` and restart).

**Not included:** hubd/runner fleet, object store, TLS certs. Bring your own.

## Metrics / Prometheus

Lightweight process-local counters at `GET /metrics` (no latency histograms).  
Set `AI_CLOUDHUB_METRICS_TOKEN` in production; scrape with Bearer auth.

- Metric list, PromQL, scrape notes: [METRICS.md](./METRICS.md)  
- Importable Grafana dashboard + example Prometheus job: [deploy/grafana/](../deploy/grafana/)

```bash
curl -sS -H "Authorization: Bearer $AI_CLOUDHUB_METRICS_TOKEN" \
  http://127.0.0.1:8080/metrics | head
```

## TLS / reverse proxy

Terminate TLS at the edge; keep the API on **loopback** (`HTTP_ADDR=127.0.0.1:8080` or Docker publish `127.0.0.1:8080:8080`).

| Proxy | Example |
|-------|---------|
| **nginx** | [`deploy/nginx.conf.example`](../deploy/nginx.conf.example) |
| **Caddy** (auto HTTPS) | [`deploy/Caddyfile.example`](../deploy/Caddyfile.example) |

Forward `X-Forwarded-Proto` / `X-Real-IP` so audit and admin CIDR checks see real clients.  
If nginx/Caddy sets HSTS, leave `AI_CLOUDHUB_HSTS=0` on the API.

Browser CORS is off by default for pure agent/CLI use; enable only if a SPA shares the origin policy you configure at the proxy.

## Runtime hosts (user side / BYOC)

- Install **rclone** (+ FUSE / WinFsp on Windows). See [WINDOWS.md](./WINDOWS.md).
- hubd: `AI_CLOUDHUB_API` + human/device token + `AI_CLOUDHUB_DEVICE_ID`.
- runner / jobs: **your machine only** — never a multi-tenant pool (D-001).
- Optional Linux sandbox: [SECCOMP.md](./SECCOMP.md), `scripts/runner-*.sh`.

### Connectors on the runner

Register non-secret config on the control plane; materialize on the user runner:

| Type | Runner effect | Host secrets |
|------|---------------|--------------|
| `git` | shallow clone → `$MOUNT/repo` (or `path_prefix`) | `GIT_ASKPASS` / SSH |
| `postgres` | inject `AI_CLOUDHUB_PG_*` | `PGPASSWORD` |
| `mysql` | inject `AI_CLOUDHUB_MYSQL_*` | `MYSQL_PWD` |

Jobs: set `connector_id` on create; claim sets `AI_CLOUDHUB_CONNECTOR_ID`.  
联调 without rclone: `AI_CLOUDHUB_MATERIALIZE_ONLY=1` + `AI_CLOUDHUB_CONNECTOR_ID` (see [CONNECTORS.md](./CONNECTORS.md)).

```bash
# Local regression of git clone + DB env inject (no FUSE):
make smoke-byoc
```

## Releases (multi-arch binaries)

```bash
git tag v0.2.50
git push origin v0.2.50
# → .github/workflows/release.yml → dist/* + checksums.txt
```

Local dry-run: `make release-binaries VERSION=0.2.50`  
Archives: `api`, `hubd`, `runner`, `mcp` (`CGO_ENABLED=0`).

## Preflight + smoke before cutover

```bash
# 1) Env checklist (fails on missing JWT/STRICT/MASTER_KEY)
export JWT_SECRET=… AI_CLOUDHUB_MASTER_KEY=… AI_CLOUDHUB_STRICT=1
export AI_CLOUDHUB_ALLOW_REGISTER=0 AI_CLOUDHUB_METRICS_TOKEN=…
export AI_CLOUDHUB_DB=postgres://…   # recommended multi-replica
make prod-preflight                  # scripts/prod-preflight.sh

# 2) Functional regression
export CGO_ENABLED=0
make test
make smoke-all          # includes smoke-stage-c + mcp
make smoke-byoc         # git/pg/mysql materialize on this host
# optional live MinIO: make smoke-minio
```

CI (GitHub Actions) on every push/PR to `main`:

| Job | What |
|-----|------|
| Test & Build | `go test ./…` + strip build of all `cmd/*` |
| Smoke suite | `make smoke-all` (no live object store) |
| Smoke MinIO live | Docker MinIO + `make smoke-minio` with `REQUIRE=1` |
| Docker image | multi-stage distroless API image build + `/healthz` |
| Release (on `v*` tags) | multi-arch binary archives + checksums |

Images: `deploy/Dockerfile` (distroless API), `deploy/Dockerfile.all` (alpine multi-binary).  
Compose: postgres/redis/api healthchecks; api depends on healthy DB/Redis.

## Cutover checklist (copy/paste)

**Full runbook:** [CUTOVER.md](./CUTOVER.md) · self-test: `make smoke-prod-preflight`

1. [ ] `make prod-preflight` green (or only expected warnings)  
2. [ ] Postgres + Redis for multi-replica API  
3. [ ] TLS at edge; API on loopback; metrics token set  
4. [ ] First admin created; `ALLOW_REGISTER=0`  
5. [ ] Policy file / OPA / PDP URL as required by your org  
6. [ ] Stripe webhook secret if using paid marketplace  
7. [ ] Job webhook: URL and HMAC secret **paired** (or both unset)  
8. [ ] User runners installed (rclone + optional git/psql clients) — **not** platform pool  
9. [ ] `make smoke-all` + `make smoke-golden` on a canary host  

## What we intentionally do not run in “platform production”

- Large multi-tenant runner pools (D-001)  
- Object body proxy through the API  
- Hosted embedding / OpenLineage warehouse / PCI card collection  
- Control-plane storage of DB passwords or git deploy keys  

See [KNOWN_LIMITATIONS.md](./KNOWN_LIMITATIONS.md) · [STAGE-C.md](./STAGE-C.md) · [CONNECTORS.md](./CONNECTORS.md).
