# Logical modules (Stage C) — not forced microservices

Default deployment is still **one process**: `cmd/api` (plus user-side `hubd` / `runner`).

Stage C adds **package boundaries** so Memory / Marketplace / Policy can later move
behind HTTP without rewriting product semantics.

## Principles (D-002 + D-001)

| Do | Don't |
|----|--------|
| Monorepo modules with clear packages | Split 10 services before scale |
| Optional remote PDP (`AI_CLOUDHUB_PDP_URL`) | Platform multi-tenant runner fleet |
| User-side hubd/runner (BYOC) | Platform pays for all Agent compute |

## Registry

```http
GET /v1/modules
```

Returns `deployment: monolith` and the module table from `internal/modules`.

## Module map

| ID | Package | Process today |
|----|---------|----------------|
| identity | `internal/auth` | api |
| policy | `internal/policy` | api (+ optional external PDP) |
| drive | `internal/drive` | api |
| sts | `internal/sts` | api |
| jobs | `internal/job` | api |
| memory | `internal/memkernel` | api |
| marketplace | `internal/marketplace` | api |
| runtime | `cmd/hubd`, `cmd/runner` | **user host only** |

## When to actually split a process

Only when metrics show independent scaling needs (e.g. PDP QPS, marketplace CDN).
Until then, keep one binary — simpler ops, same interfaces.
