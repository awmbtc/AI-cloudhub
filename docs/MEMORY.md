# Memory Kernel v0 (Stage C)

Small **control-plane** memories for agents/humans — not embeddings, not a vector DB.

## Layers

| Layer | Use |
|-------|-----|
| `working` | Current task scratch (short TTL recommended) |
| `episodic` | Session / event notes |
| `semantic` | Durable facts & preferences |

## API

```http
POST /v1/memory
Authorization: Bearer <token>
{ "layer": "working", "key": "task", "content": "…", "agent_id": "optional", "ttl_sec": 3600 }

GET /v1/memory?layer=working&agent_id=…
GET /v1/memory/{id}
DELETE /v1/memory/{id}
```

- Agent tokens may only write/list with their own `agent_id` (forced).
- Content max **64 KiB** per entry.
- Expired entries are filtered on list (lazy).

## Limits (honest)

- No search ranking / RAG pipeline
- No cross-tenant sharing
- Persistence is SQLite/Postgres/memory same as control plane
