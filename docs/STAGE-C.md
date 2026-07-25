# Stage C capabilities (honest v0)

> **Not** “500 agents finished the entire 3.0 vision.”  
> This document lists what shipped as **control-plane foundations** and what remains out of scope.

## Shipped foundations

| Area | API / artifact | What it is | What it is not |
|------|----------------|------------|----------------|
| **Data Lineage** | `POST/GET /v1/lineage` | Append-only events (actor, action, entity) | Full warehouse lineage / OpenLineage SaaS |
| **Git/DB/SaaS** | `GET /v1/connectors/catalog`, `/v1/connectors*` | First-class **types + bindings** | Full sync engines / OAuth hosts |
| **Vector Memory** | `POST /v1/memory` + `embedding`, `POST /v1/memory/search` | Client-supplied vectors + cosine top-k | Hosted embedding model |
| **Identity Graph** | `POST/GET /v1/graph` | Subject–relation–object edges | Enterprise IdP graph product |
| **Payment Marketplace** | `price_cents`, `POST …/checkout`, `/v1/purchases`, `…/pay` | Free + pending/paid **stub** | PCI Stripe production integration |
| **Multi-process deploy** | `deploy/docker-compose.modular.yml` | **2× api** + edge LB + shared PG/Redis | Per-domain microservices as default |

## Multi-process policy

- **Default** remains single `cmd/api` (D-002).  
- Modular compose = horizontal **replicas of the same binary**, not a platform runner pool (D-001).  
- Remote PDP remains **your** process via `AI_CLOUDHUB_PDP_URL`.

## Quick examples

```bash
# Lineage
curl -sS -X POST $API/v1/lineage -H "Authorization: Bearer $TOK" \
  -d '{"action":"drive.session","entity":"drive:DID","detail":"issued"}'

# Graph
curl -sS -X POST $API/v1/graph -H "Authorization: Bearer $TOK" \
  -d '{"subject":"agent:A","relation":"can_access","object":"drive:D"}'

# Connector
curl -sS -X POST $API/v1/connectors -H "Authorization: Bearer $TOK" \
  -d '{"type":"git","name":"app-repo","config":{"remote_url":"https://github.com/org/app"}}'

# Vector memory (client embeds text externally)
curl -sS -X POST $API/v1/memory -H "Authorization: Bearer $TOK" \
  -d '{"layer":"semantic","content":"prefers r2","embedding":[0.1,0.2,0.3]}'
curl -sS -X POST $API/v1/memory/search -H "Authorization: Bearer $TOK" \
  -d '{"query":[0.1,0.2,0.3],"k":5}'

# Paid listing checkout (stub)
curl -sS -X POST $API/v1/marketplace -H "Authorization: Bearer $TOK" \
  -d '{"name":"pro-skill","kind":"skill","price_cents":999,"currency":"usd","public":true,"payload":{}}'
# then POST /v1/marketplace/{id}/checkout → pending; POST /v1/purchases/{id}/pay to mark paid
```

## Still out of scope

- Platform multi-tenant **runner pool** (D-001)  
- Splitting every package into a separate default microservice  
- Real card payments without your Stripe account / webhooks verification  
- Automatic Git clone or SaaS data plane through control plane (bytes stay BYOC/BYOS)  
