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

**Paid gate:** listings with `price_cents > 0` require a purchase with `status=paid` (checkout + Stripe webhook or pay stub) before install succeeds.

### Side effects on successful install

Best-effort (install still succeeds if any side effect fails):

| Side effect | Detail |
|-------------|--------|
| Episodic memory | `key=marketplace.install.<item_id>`, content notes agent materialization; response includes `memory_id` |
| Identity graph | `user:… --installed--> item:…`, `agent:… --from_item--> item:…`, `user:… --owns_agent--> agent:…` |
| Lineage | `action=marketplace.install`, `entity=item:<id>`, parent `agent:<id>` |
| Audit | `marketplace.install` |

## Limits

- No signing / review workflow
- Skill/manifest kinds are publishable + checkoutable but **install** materializes only `agent_template`
- No full PCI Stripe storefront (webhook signature + purchase status only)
- System items cannot be deleted
