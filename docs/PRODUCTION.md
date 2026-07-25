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

## Optional policy / STS

| Feature | Env | Docs |
|---------|-----|------|
| JSON policy | `AI_CLOUDHUB_POLICY_FILE` (+ optional `POLICY_RELOAD_SEC`) | [POLICY.md](./POLICY.md) |
| OPA/Rego | `AI_CLOUDHUB_OPA_POLICY_FILE` | [POLICY.md](./POLICY.md) |
| OPA observe | `AI_CLOUDHUB_OPA_OBSERVE=1` | Log-would-deny without blocking |
| MinIO/AWS/S3 STS | `AI_CLOUDHUB_*_STS=1` | [STS.md](./STS.md) |
| Qiniu download assist | `AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN=1` | Session note; object GET via `presign-get` always for type=qiniu |
| OCI API-key check | `AI_CLOUDHUB_ORACLE_NATIVE_IAM=1` + OCI key env | Cache: `AI_CLOUDHUB_OCI_IAM_CACHE_SEC` |

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

## Runtime hosts (user side)

- Install **rclone** (+ FUSE / WinFsp on Windows). See [WINDOWS.md](./WINDOWS.md).
- hubd: `AI_CLOUDHUB_API` + human/device token + `AI_CLOUDHUB_DEVICE_ID`.
- runner / jobs: user machine only; never run a multi-tenant pool for strangers (D-001).
- Optional Linux sandbox: [SECCOMP.md](./SECCOMP.md), `scripts/runner-*.sh`.

## Releases (multi-arch binaries)

Tag a version to publish GitHub Release artifacts (linux/darwin/windows × amd64/arm64):

```bash
git tag v0.2.0
git push origin v0.2.0
# → workflow .github/workflows/release.yml builds dist/* + checksums.txt
```

Local dry-run:

```bash
make release-binaries VERSION=0.2.0
# dist/aicloudhub_0.2.0_linux_amd64.tar.gz …
# dist/checksums.txt
```

Each archive contains `api`, `hubd`, `runner`, `mcp` (pure Go, `CGO_ENABLED=0`).

## Smoke before cutover

```bash
export CGO_ENABLED=0
make test
make smoke-all          # policy includes OPA; mcp; jobs; objects (Qiniu offline HMAC)
# optional live MinIO:
# make smoke-minio
```

CI (GitHub Actions) on every push/PR to `main`:

| Job | What |
|-----|------|
| Test & Build | `go test ./…` + strip build of all `cmd/*` |
| Smoke suite | `make smoke-all` (no live object store) |
| Smoke MinIO live | Docker MinIO + `make smoke-minio` with `REQUIRE=1` |
| Docker image | multi-stage distroless API image build + `/healthz` |
| Release (on `v*` tags) | multi-arch binary archives + checksums |

Images: `deploy/Dockerfile` (distroless API + `healthcheck` subcommand), `deploy/Dockerfile.all` (alpine multi-binary).  
Compose: postgres/redis/api all have `healthcheck`; api depends on healthy DB/Redis.
## What we intentionally do not run in “platform production”

- Large multi-tenant runner pools  
- Object body proxy through the API  
- Remote PDP (local JSON / OPA file only)  
- Auto-minting OCI S3 customer secrets / PARs  

See [KNOWN_LIMITATIONS.md](./KNOWN_LIMITATIONS.md).
