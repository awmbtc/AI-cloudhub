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

### Jobs with `connector_id`

```http
POST /v1/jobs
{ "drive_id":"…", "command":["make","test"], "connector_id":"<git connector id>" }
```

When a BYOC runner **claims** the job, it sets `AI_CLOUDHUB_CONNECTOR_ID` from the job field and runs the same git materialization after mount/sync. MCP `create_job` accepts `connector_id` too.

### Job note: clone path / failure

After materialization, the worker **appends** to the job note on `complete`:

| Outcome | Note fragment | Job status (default) |
|---------|---------------|----------------------|
| Clone OK | `cloned to /workspace/repo` (or `$MOUNT/$path_prefix`) | agent result |
| Clone fail (soft) | `clone failed: <reason>` | agent may still **succeed** |
| Clone fail + strict | same note | **failed** |

Env:

| Variable | Meaning |
|----------|---------|
| `AI_CLOUDHUB_CLONE_STRICT=1` | Fail the job when git connector materialization fails (mirror of `AI_CLOUDHUB_SECCOMP_STRICT`) |
| unset / `0` | **Default soft:** continue agent; note still records `clone failed: …` |

Create-time D-001 / user notes are preserved (`Complete` appends, does not replace). On success the agent child also sees `AI_CLOUDHUB_CLONE_PATH`.

## Postgres + runner contract

| Side | Responsibility |
|------|----------------|
| API | Store non-secret config: `host`, `port?`, `database`, `user?`, `schema?`, `sslmode?`, `dsn_template?` |
| Runner | Inject `AI_CLOUDHUB_PG_*` into agent env; host holds `PGPASSWORD` |

```http
POST /v1/connectors
{ "type":"postgres", "name":"app-db",
  "config":{ "host":"db.example.com", "database":"app", "user":"app_ro", "sslmode":"require" } }
```

Password / full DSN keys are **stripped** on create. `dsn_template` must not embed `user:pass@`.

```bash
export PGPASSWORD='…'   # host only
AI_CLOUDHUB_CONNECTOR_ID=<postgres-id> ./.bin/runner -- your-agent
# or job.connector_id → claim sets the same env
```

Injected (non-secret):

| Env | Meaning |
|-----|---------|
| `AI_CLOUDHUB_PG_HOST` / `_PORT` / `_DATABASE` / `_USER` / `_SCHEMA` / `_SSLMODE` | connection fields |
| `AI_CLOUDHUB_PG_DSN_TEMPLATE` | password-less DSN or custom template |

When jail is on, parent **`PGPASSWORD`** (and related libpq keys) pass through only if postgres materialization succeeded (`PassLibpq`). Set `AI_CLOUDHUB_PASS_PG=0` to disable. Soft fail note: `pg failed: …`; `AI_CLOUDHUB_PG_STRICT=1` fails the job.

MySQL / SaaS connectors remain **registry only** in this build (same binding API; no runner materializer yet).

## Security

- D-001: runner is **your** machine, not a platform pool  
- Secrets stay on runner  
- Optional lineage: `connector.register` is recorded on create  
