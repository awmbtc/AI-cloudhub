# Changelog

## v0.2.1

### Fixes
- fix: Makefile `.PHONY` line break — missing newline glued `smoke-all` and `all:` targets, causing GNU make "multiple target patterns" and breaking CI `make smoke-all`

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
