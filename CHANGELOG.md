# Changelog

## v0.1.1

### Security
- User passwords hashed with `golang.org/x/crypto/bcrypt` on register
- Login uses `bcrypt.CompareHashAndPassword`
- Legacy plaintext passwords upgraded to bcrypt on next successful login (`UpdateUserPassword`)

## Unreleased

### Hardening
- Provider health probe: `GET|POST /v1/providers/{id}/health` (ListBuckets, 8s timeout)
- Drive quota (default 20/user) and provider quota (default 20/user); binding remains 10
- Admin audit filter: `GET /v1/admin/audit?user_id=&limit=`
- Config validation: JWT min length 16, reject default secret / missing master key when `AI_CLOUDHUB_STRICT=1`
- Password/username policy; token TTL; register gate; login lockout + fail audit
- Last-admin demotion blocked; security headers; max body size; optional metrics token
- Session revoke: jti denylist + token_version; logout; admin revoke-sessions; password change invalidates all
- Audit filter by action: `GET /v1/admin/audit?action=`
- Refresh tokens (opaque, hashed); `POST /v1/auth/refresh` with rotation
- Admin create user `POST /v1/admin/users`; optional HSTS; JSON Content-Type 415
- ROADMAP-2.0 + D-002: Agent Identity CRUD, capability scopes on tokens, runner path jail
- Docs: formal evolution roadmap aligned with architecture
- Stage B: agent drive allowlist, Policy Engine v0, Manifest 2.0 permissions, audit.agent_id
- Sandbox v1 env filter in runner; Snapshot v0 metadata API under /v1/drives/{id}/snapshots
- MCP v0.2: tool scopes via /v1/me, path jail, whoami/resolve_path/snapshots tools
- Admin API IP allowlist: AI_CLOUDHUB_ADMIN_CIDRS
- Snapshot restore apply=true rehydrates drive metadata; 50 snapshots/drive cap
- Runner AI_CLOUDHUB_NETWORK=deny strips proxy env; scripts/smoke-agent.sh
- Snapshot object inventory (include_objects) + inventory diff API
- STS source metrics; docs/STS.md; scripts/runner-netns.sh (Linux unshare)
- Live GET /v1/drives/{id}/objects (+ versions, version-hint); Retry-After on 429
- scripts/runner-bwrap.sh; MCP list_objects
- BYOS version assist: POST objects/presign-get, restore-plan, restore-version (CopyObject, no body proxy)
- MCP object_presign_get / object_restore_plan / object_restore_version
- scripts/seccomp/runner-default.json + scripts/runner-seccomp.sh (Linux skeleton)
- OpenAPI objects paths; scripts/smoke-objects.sh (make smoke-objects)
- OpenAPI drive snapshots (list/create/get/delete/diff/restore); smoke-agent snapshot coverage

### Ops
- PostgreSQL store: `AI_CLOUDHUB_DB=postgres://...`
- Redis shared rate limit: `AI_CLOUDHUB_REDIS=...`
- `deploy/docker-compose.prod.yml` (api + postgres + redis; no runner pool)
- Graceful shutdown, CORS (`AI_CLOUDHUB_CORS_ORIGINS`), `X-Request-ID`
- Makefile + `deploy/Dockerfile.all`
- OpenAPI: `docs/openapi.yaml`

### Admin / audit
- List users, change password, audit log (auth + provider/drive/binding mutations)
- Atomic job claim + region filter on pending jobs

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
