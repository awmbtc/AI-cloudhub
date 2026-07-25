# Policy Engine (built-ins + JSON + OPA + remote PDP)

AI-cloudhub evaluates **agent** access with:

1. **Built-in checks** (always): token scopes, agent `allowed_drive_ids`, agent path prefixes  
2. **Optional JSON file** (`AI_CLOUDHUB_POLICY_FILE`): ordered allow/deny rules  
3. **Optional OPA/Rego** (`AI_CLOUDHUB_OPA_POLICY_FILE`): `data.aicloudhub.authz.allow`  
4. **Optional remote PDP** (`AI_CLOUDHUB_PDP_URL`) — Stage C HTTP decision point  

Humans skip built-in agent checks; JSON / OPA / PDP still apply where configured.

## Enable JSON

```bash
export AI_CLOUDHUB_POLICY_FILE=./protocols/policy.example.json
export AI_CLOUDHUB_POLICY_RELOAD_SEC=30
```

## Enable OPA

```bash
export AI_CLOUDHUB_OPA_POLICY_FILE=./protocols/aicloudhub.rego.example
# optional: OPA deny becomes allow with reason (observe mode)
export AI_CLOUDHUB_OPA_OBSERVE=0
# optional: OPA eval errors deny instead of fail-open
export AI_CLOUDHUB_OPA_STRICT=0
./.bin/api
```

Example Rego: [`protocols/aicloudhub.rego.example`](../protocols/aicloudhub.rego.example).  
Query: **`data.aicloudhub.authz.allow`** (boolean). Input = request fields (`agent_id`, `action`, `drive_id`, `path`, `scopes`, …).

Evaluation order: **built-in → JSON rules → OPA → remote PDP** (later stages can only further deny unless observe / fail-open).

## Enable remote PDP (Stage C)

```bash
# POST JSON body: { "input": { agent_id, action, drive_id, path, scopes, principal, … } }
# Response 200: { "allow": true|false, "reason": "optional" }
export AI_CLOUDHUB_PDP_URL=http://127.0.0.1:8181/v1/data/aicloudhub/authz
# optional bearer for the PDP
# export AI_CLOUDHUB_PDP_TOKEN=…
export AI_CLOUDHUB_PDP_TIMEOUT_MS=500   # default 500, max 10000
export AI_CLOUDHUB_PDP_OBSERVE=0        # 1 = would-deny becomes allow + reason
export AI_CLOUDHUB_PDP_STRICT=0         # 1 = network/HTTP errors deny (default fail-open)
./.bin/api
```

Admin status: `GET /v1/admin/policy` includes `pdp_enabled` / `pdp_url`.

JSON example: [`protocols/policy.example.json`](../protocols/policy.example.json).

## Document schema (version 1)

`_comment` keys are documentation only (ignored by the loader). Full example: [`protocols/policy.example.json`](../protocols/policy.example.json).

```json
{
  "_comment": "version 1; ordered rules; first matching deny|allow wins",
  "version": 1,
  "mode": "enforce",
  "rules": [
    {
      "id": "block-secret-paths",
      "effect": "deny",
      "principals": ["agent"],
      "actions": ["path.read", "path.write", "drive.read", "drive.write", "drive.session"],
      "path_deny_prefixes": [".ssh", ".env", ".aws", "secrets"],
      "reason": "secret paths blocked for agents"
    },
    {
      "id": "jobs-require-scope",
      "_comment": "Built-in already requires job.run on job routes; file require_scopes is an extra/example gate",
      "effect": "deny",
      "principals": ["agent"],
      "actions": ["job.run"],
      "require_scopes": ["job.run"],
      "reason": "job.run scope required"
    },
    {
      "id": "deny-provider-write-for-agents",
      "_comment": "Optional: agents may provider.read (list) but not provider.write",
      "effect": "deny",
      "principals": ["agent"],
      "actions": ["provider.write"],
      "reason": "agents cannot write providers"
    }
  ]
}
```

| Field | Meaning |
|-------|---------|
| `mode` | `enforce` (default) deny on match; `observe` never denies (reason `observe:would-deny:…`) |
| `rules` | **Ordered**; first matching **deny** or **allow** wins |
| `effect` | `deny` \| `allow` |
| `principals` | `agent` \| `human` \| `any` (empty = any) |
| `actions` | e.g. `drive.read`, `drive.write`, `drive.session`, `job.run`, `provider.read`, `provider.write`, `path.read`, `path.write` |
| `agent_ids` | exact agent UUID list (empty = any agent) |
| `drive_ids` | exact drive id list |
| `path_deny_prefixes` | rule matches if request path hits any prefix/segment |
| `path_allow_prefixes` | rule matches if path is under allow list |
| `require_scopes` | when rule matches, all scopes must be present or deny |
| `reason` | returned in 403 / Decision.Reason |

## Evaluation order

```text
Request
  → built-in: scope / drive allowlist / agent path prefixes (agents only)
  → file rules (ordered)
  → default allow
```

Drive HTTP routes call `CheckAccess` with action derived from method (`GET` → read, mutating → write, `/session` → `drive.session`).

Job routes (`POST /v1/jobs`, claim/complete/cancel) call `CheckAccess` with action `job.run` (and drive id when known). Agents need token scope `job.run` **and** pass file rules.

Provider routes call scope `provider.read` / `provider.write` and file policy actions of the same names. `provider.write` implies `provider.read` in the built-in scope map.

`POST /v1/jobs/next/claim` uses **ClaimNextFiltered**: if a claimed job’s drive is denied, the job is **released back to `pending`** (note annotated) and the next pending job is tried (up to 32). Direct `POST /v1/jobs/{id}/claim` also releases on post-claim deny.

## Admin API

```http
GET /v1/admin/policy
GET /v1/admin/policy?rules=1
```

Admin-only. Returns load status; `?rules=1` includes the document.

## Non-goals

- Hosting a multi-tenant SaaS PDP for customers (you bring the URL)  
- Replacing IAM on the object store (BYOS)  
- Full OPA ecosystem (bundles, decision logs as a service) — local `.rego` file remains optional  
- Platform multi-tenant **runner pool** (D-001) — unrelated to PDP  
