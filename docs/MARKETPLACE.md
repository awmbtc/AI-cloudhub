# Agent Marketplace v0 (Stage C)

Catalog of **templates and skills** — not a full PCI storefront. Checkout/pay is webhook-stubbed; install is control-plane materialization.

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
POST /v1/marketplace          # human: publish (agent_template|skill|manifest)
POST /v1/marketplace/{id}/install   # human: install by kind
POST /v1/marketplace/{id}/checkout  # purchase pending|paid (free → paid)
DELETE /v1/marketplace/{id}   # human: unpublish own item
```

### Install by kind

| Kind | Behavior | Response highlights |
|------|----------|---------------------|
| `agent_template` | Creates user-owned agent via Agent Identity APIs (**human session only**) | `agent_id`, `scopes`, `memory_id` |
| `skill` | Payload-only (tool hints / docs); **no agent**; **agents may install** | `kind=skill`, `payload`, `memory_id` |
| `manifest` | Payload-only workspace skeleton; **no agent**; **agents may install** | `kind=manifest`, `payload`, `memory_id` |

MCP: `list_marketplace` / `install_marketplace` (see [MCP.md](./MCP.md)).

**Paid gate:** listings with `price_cents > 0` require a purchase with `status=paid` (checkout + Stripe webhook or pay stub) before any install succeeds (`HasPaidAccess`).

### Side effects on successful install

Best-effort (install still succeeds if any side effect fails):

| Side effect | Detail |
|-------------|--------|
| Episodic memory | `key=marketplace.install.<item_id>`; response includes `memory_id`; meta includes `kind` + payload |
| Identity graph | Always `user:… --installed--> item:…`. Agent kinds also get `agent --from_item--> item` and `user --owns_agent--> agent` |
| Lineage | `action=marketplace.install`, `entity=item:<id>` |
| Audit | `marketplace.install` |

## Limits

- No signing / review workflow
- Skill/manifest install is metadata into memory/graph, not binary distribution
- No full PCI Stripe storefront (webhook signature + purchase status only)
- System items cannot be deleted
