# Memory Kernel v0 (Stage C)

Small **control-plane** memories for agents/humans — **client-supplied** optional embeddings, not a hosted embedding model or vector-DB SaaS.

书面深挖范围：[STAGE-C-SCOPE-MEMORY.md](./STAGE-C-SCOPE-MEMORY.md)（D-003 P2）。

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
{ "layer": "working", "key": "task", "content": "…", "agent_id": "optional", "ttl_sec": 3600, "embedding": [0.1, 0.2] }

GET /v1/memory?layer=working&agent_id=…&drive_id=…&key=…&limit=100
GET /v1/memory/{id}
DELETE /v1/memory/{id}

POST /v1/memory/search
{ "query": [0.1, 0.2], "k": 5, "layer": "semantic" }
```

- Agent tokens may only write/list with their own `agent_id` (forced); get/delete enforce same ownership when `agent_id` is set on the entry.
- Content max **64 KiB** per entry; meta same cap.
- Optional embedding / search query: max **4096 dims** (reject oversize).
- `ttl_sec` → `expires_at`; list / search / get **skip expired** (lazy; no purge cron in this cut).
- Search `k`: default **10**, max **50** (oversize → **400**, not silent clamp).
- Audit: `memory.put` / `memory.delete`; lineage hooks on put/delete when lineage module present.

## Limits (honest)

- **Not** a hosted embedding API — client embeds text externally
- Cosine top-k only (brute force over listed rows; list cap 500 for search pool)
- No cross-tenant sharing / multi-tenant vector SaaS
- Persistence is SQLite / Postgres / memory same as control plane
