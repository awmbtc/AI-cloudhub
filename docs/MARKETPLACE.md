# Agent Marketplace v0 (Stage C)

Catalog of **templates and skills** — not payments, not third-party hosting of binaries.

## System catalog

Built-in items (id prefix `sys.`):

| ID | Kind | Purpose |
|----|------|---------|
| `sys.agent.readonly` | agent_template | drive.read agent |
| `sys.agent.jobs` | agent_template | job.run + drive.read |
| `sys.skill.qiniu_presign` | skill | docs for Qiniu presign |
| `sys.manifest.workspace_v2` | manifest | Manifest v2 skeleton |

## API

```http
GET /v1/marketplace
GET /v1/marketplace/{id}
POST /v1/marketplace          # human: publish
POST /v1/marketplace/{id}/install   # human: install agent_template → creates agent
DELETE /v1/marketplace/{id}   # human: unpublish own item
```

Install only supports `kind=agent_template` and creates a user-owned agent via existing Agent Identity APIs.

## Limits

- No signing / review workflow
- No billable storefront
- System items cannot be deleted
