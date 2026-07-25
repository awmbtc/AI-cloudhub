# Connectors (Git / DB / SaaS) — Stage C

Control plane holds **type + non-secret config**. **Bytes and credentials live on the user runner** (BYOC).

## Catalog

```http
GET /v1/connectors/catalog
GET /v1/connectors
POST /v1/connectors   { "type":"git", "name":"app", "config":{ "remote_url":"…", "branch":"main", "path_prefix":"src" } }
```

Types: `git`, `postgres`, `mysql`, `notion`, `slack`, `github` (see `internal/connector`).

**Never** put `password` / `token` / `secret` in `config` — they are stripped on create.

## Git + runner contract

| Side | Responsibility |
|------|----------------|
| API | Store connector binding (remote_url, branch, path_prefix) |
| Runner | `AI_CLOUDHUB_CONNECTOR_ID=<id>` after mount/sync → `git clone` into workspace |

```bash
# After registering a git connector (id=CID):
AI_CLOUDHUB_API=… AI_CLOUDHUB_TOKEN=… \
AI_CLOUDHUB_DRIVE_ID=… \
AI_CLOUDHUB_CONNECTOR_ID=CID \
./.bin/runner -- your-agent

# Auth for private repos: standard git env on the runner host
# GIT_ASKPASS / SSH keys / gh auth — not stored in control plane
```

Clone is **shallow** (`--depth 1`). If `path_prefix` is set, dest is `$MOUNT/$path_prefix`; else `$MOUNT/repo`. Existing `.git` skips clone.

DB/SaaS connectors are **registry only** in this build: agents read config from API and talk to the SaaS/DB from the runner with local credentials.

## Security

- D-001: runner is **your** machine, not a platform pool  
- Secrets stay on runner  
- Optional lineage: `connector.register` is recorded on create  
