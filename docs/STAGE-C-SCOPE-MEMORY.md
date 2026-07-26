# Stage C · Memory Kernel 深挖范围（D-003 P2）

> 单独立项书面范围。对照 [D-003](./DECISIONS.md) · [MEMORY.md](./MEMORY.md) · [STAGE-C.md](./STAGE-C.md)。  
> **English:** Written scope for Stage C Memory deepen — client-side embedding store/search only.

## Goal / 目标

Memory Kernel 作为**控制面**小记忆 + **客户端向量**存储与余弦检索：

- 三层：`working` | `episodic` | `semantic`
- 可选 client-supplied embedding；服务端 **不做** 托管 embedding / OpenAI 调用
- 持久化与 control plane 同一 store（memory / SQLite / Postgres）

**Not:** hosted embedding models, multi-tenant vector DB SaaS, RAG pipeline product.

## In scope（本切片）

| 项 | 说明 |
|----|------|
| **TTL / expiry（可选）** | `POST` 支持 `ttl_sec` → `expires_at`；list / search / get 跳过已过期（lazy） |
| **Layer list** | `GET /v1/memory?layer=&agent_id=&drive_id=&key=&limit=` |
| **Delete** | `DELETE /v1/memory/{id}` + audit；尽量 lineage hook |
| **Search k 诚实上限** | `k` 默认 10；合法范围 **1..50**；越界 **报错**（不静默改写） |
| **尺寸护栏** | content ≤ 64 KiB；embedding / query dims ≤ 4096 |
| **Audit / lineage** | put 已有；delete 审计 + 可选 lineage `memory.delete` |
| **验收** | `CGO_ENABLED=0 go test ./internal/memkernel/...` + `scripts/smoke-stage-c.sh` memory 段 |

## Out of scope（明确不做）

- 托管 embedding 模型 / OpenAI（或任意云）服务端嵌入
- 多租户向量库 SaaS、ANN 索引、跨租户共享
- 微服务拆分（D-002）
- Job admin / webhook ops 表面（D-003 freeze）
- 主动 purge cron（本切片仅 lazy skip expired）

## Acceptance / 验收

```bash
CGO_ENABLED=0 go test ./internal/memkernel/...
# smoke：memory put / layer list / vector search / delete / short TTL expiry
make smoke-stage-c   # 或 bash scripts/smoke-stage-c.sh
```

## API surface（本切片）

```http
POST   /v1/memory              # put（content, layer, optional embedding, ttl_sec）
GET    /v1/memory              # list（layer / agent_id / drive_id / key / limit）
GET    /v1/memory/{id}         # get（expired → not found）
DELETE /v1/memory/{id}         # delete
POST   /v1/memory/search       # { query, k?, layer? } client vectors only
```

## Status

| 字段 | 内容 |
|------|------|
| **决策依据** | D-003 P2（书面范围后深挖） |
| **切片状态** | Accepted scope · implement in-tree |
| **文档** | [MEMORY.md](./MEMORY.md) · OpenAPI `/v1/memory*` |
