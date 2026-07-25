# Changelog

## v0.1.1

### Security
- User passwords hashed with `golang.org/x/crypto/bcrypt` on register
- Login uses `bcrypt.CompareHashAndPassword`
- Legacy plaintext passwords upgraded to bcrypt on next successful login (`UpdateUserPassword`)

## Unreleased

### STS / Policy extensions
- Qiniu native private download tokens (`AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN`, source `qiniu_download`)
- Qiniu object-level signed GET via `POST …/objects/presign-get` (`method=qiniu_download`)
- OCI API-key IAM validation (`AI_CLOUDHUB_ORACLE_NATIVE_IAM` + private key env, source `oci_iam`) with short Identity cache
- Optional OPA/Rego (`AI_CLOUDHUB_OPA_POLICY_FILE`, query `data.aicloudhub.authz.allow`); smoke-policy covers OPA
- Production checklist (`docs/PRODUCTION.md`); prod compose requires `JWT_SECRET` / `MASTER_KEY`, enables STRICT
- smoke-objects + smoke-mcp cover Qiniu offline `qiniu_download` presign
- Fix: agent `drive.read` may POST object helpers (`presign-get` / `restore-plan` / `version-hint`); only `restore-version` needs write
- CI: `make smoke-all` on push/PR; smoke scripts use free ports
- CI: live MinIO smoke (`REQUIRE=1`) + Docker image build/healthz
- deploy: distroless API Dockerfile (`-s -w`, version ldflags); alpine multi-binary; `.dockerignore`
- deploy: nginx + Caddy TLS reverse-proxy examples
- `api healthcheck` subcommand for distroless compose probes; prod compose postgres/redis/api healthchecks
- Release workflow on `v*` tags + `scripts/release-build.sh` / `make release-binaries` (multi-arch)

### Close-out
- 1.x→2.0 mainline closed: P0–P3, hardening waves 1–4, Stage A (Agent Identity + scopes + path jail), Stage B (drive allowlist, Policy Engine + `AI_CLOUDHUB_POLICY_FILE`, Manifest 2.0, audit.agent_id, Snapshot v0, sandbox env filter)
- Jobs: `agent_id` / `claimed_by_agent_id`, claim release on policy deny, list filters, job.* audit
- MCP jobs tools: `list_jobs` / `create_job` / `claim_next_job` / `complete_job` / `cancel_job` + `list_providers` (`make smoke-mcp`)
- Binding agent gates (scope + allowlist/policy; list filter); Devices API rejects agent tokens
- Multi-vendor STS best-effort (MinIO/AWS/S3-compat, Aliyun RAM, Tencent CAM, Qiniu download token, OCI API-key IAM); objects/snapshots/OpenAPI/seccomp as previously landed
- Optional OPA/Rego closes the prior “remaining only” triad (docs: STS.md, POLICY.md, KNOWN_LIMITATIONS)

### STS
- Shared S3-compatible AssumeRole + per-vendor flags; Aliyun RAM / Tencent CAM native; Qiniu/Oracle source labels + STS endpoint overrides; metrics + docs/STS.md

### Hardening
- Auth/session: bcrypt, refresh tokens, jti/token_version revoke, login lockout, register gate, password policy
- Quotas, admin audit filters, JWT/master-key strict config, security headers, metrics token, HSTS, Admin CIDRs
- Runtime: path jail, env filter, seccomp profiles, network deny wrappers; smoke-agent / smoke-policy / smoke-objects / smoke-minio

### Ops
- PostgreSQL store; Redis shared rate limit; `deploy/docker-compose.prod.yml`; graceful shutdown; CORS; Makefile; Dockerfile.all; OpenAPI

### Admin / audit
- List users, change password, audit log (auth + provider/drive/binding/job); atomic job claim + region filter

## v0.1.0 (architecture MVP complete)

### Control plane
- Auth, providers, drives, bindings, devices, jobs (SQLite durable)
- STS mount sessions + refresh; multi-vendor best-effort native STS:
  - MinIO AssumeRole (`AI_CLOUDHUB_MINIO_STS=1` → `source=minio_sts`)
  - AWS AssumeRole (`AI_CLOUDHUB_AWS_STS=1` + AWS-looking S3 endpoint + `AI_CLOUDHUB_AWS_STS_ROLE_ARN` → `source=aws_sts`)
  - R2/B2/OSS/COS/Qiniu/Oracle: embedded short session + `Session.Note` (no harmful STS probe)

- Workspace Manifest + schema
- Write barrier, rate limit, binding quota
- Envelope encryption for provider secrets (`AI_CLOUDHUB_MASTER_KEY`)
- `GET /metrics` Prometheus text
- `GET /v1/runtime/check`

### Runtimes (BYOC)
- `hubd` auto-mount, soft session refresh, unmount barrier, `sync_workspace`
- `runner` one-shot + `AI_CLOUDHUB_WORKER=1` claim loop
- `mcp` stdio tools for agents

### Vendors
- A: s3, r2, minio
- B: b2, oss, cos
- C: qiniu, oracle

### Decisions
- D-001: no default large platform runner pools

### Smokes
- smoke-p0, smoke-p1, smoke-job
