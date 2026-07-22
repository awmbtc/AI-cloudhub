# Policy Engine (v0 built-ins + v1 JSON file)

AI-cloudhub evaluates **agent** access with:

1. **Built-in checks** (always): token scopes, agent `allowed_drive_ids`, agent path prefixes  
2. **Optional JSON file** (`AI_CLOUDHUB_POLICY_FILE`): ordered allow/deny rules  

Humans (non-agent tokens) skip built-in agent checks; file rules with `"principals":["human"]` or `"any"` still apply.

This is **not OPA**. No Rego. Simple JSON, pure Go, CGO-free.

## Enable

```bash
export AI_CLOUDHUB_POLICY_FILE=./protocols/policy.example.json
# optional: re-stat file every N seconds and reload on mtime change
export AI_CLOUDHUB_POLICY_RELOAD_SEC=30
./.bin/api
```

Example file: [`protocols/policy.example.json`](../protocols/policy.example.json).

## Document schema (version 1)

```json
{
  "version": 1,
  "mode": "enforce",
  "rules": [
    {
      "id": "block-secret-paths",
      "effect": "deny",
      "principals": ["agent"],
      "actions": ["path.read", "path.write", "drive.read", "drive.write", "drive.session"],
      "path_deny_prefixes": [".ssh", ".env"],
      "reason": "secret paths blocked for agents"
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
| `actions` | e.g. `drive.read`, `drive.write`, `drive.session`, `job.run`, `path.read`, `path.write` |
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

## Admin API

```http
GET /v1/admin/policy
GET /v1/admin/policy?rules=1
```

Admin-only. Returns load status; `?rules=1` includes the document.

## Non-goals

- OPA / Rego / external PDP  
- Per-request remote policy network calls  
- Replacing IAM on the object store (BYOS)  
