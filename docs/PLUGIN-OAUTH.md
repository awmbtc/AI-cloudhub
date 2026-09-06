# Plugin host OAuth / marketplace — design (D-003 adjacent)

**Status:** design only (no OAuth server implemented).  
**Audience:** agents / hosts that want 「安装插件 → 用户登录 → 拿到 Agent Token」.  
**Related:** [PLUGIN-FOR-AGENTS.md](./PLUGIN-FOR-AGENTS.md) · [CURSOR-MCP.md](./CURSOR-MCP.md) · [MCP.md](./MCP.md) · [DECISIONS.md](./DECISIONS.md)

---

## Codebase reality (searched)

| Area | Finding |
|------|---------|
| `internal/auth` | Username/password register + login, JWT access/refresh, Agent Token mint/revoke, scopes — **no OAuth/OIDC/device-code** |
| Connectors / STAGE-C | Docs mention future **connector** OAuth / sync engines — unrelated to host plugin login |
| Marketplace package | `internal/marketplace` is in-product item catalog, **not** Cursor/Claude app-store packaging |
| Current Cursor path | Manual Agent Token + `scripts/cursor-mcp-install.sh` (Phase 0 below) |

There is **nothing substantial** to extend into a host OAuth flow yet. Shipping a half-broken authorize/callback server would create security debt without a host that can consume it. Prefer this design + Phase 0 artifacts.

---

## Goals

1. **Host installs plugin/MCP** (Cursor / Claude Desktop / other) without pasting long-lived cloud SecretKeys.
2. **User authenticates to AI-cloudhub** (human identity).
3. **Host receives a short-lived Agent Token** with explicit `scopes` + `allowed_drive_ids` (never the user’s password; never provider SK).
4. **Revocation works**: disable agent / bump token version → host calls fail immediately (already true for Agent Tokens).

Non-goals for this doc / D-003:

- Hosting provider OAuth to R2/S3/etc. (BYOS keys stay in AI-cloudhub vault).
- Full bidirectional SaaS connector OAuth.
- Becoming an OIDC IdP for third-party websites.

---

## Proposed endpoints (future)

All under control-plane; names illustrative.

### Device-code (preferred for CLI / IDE)

| Step | Method | Path | Notes |
|------|--------|------|-------|
| Start | `POST` | `/v1/auth/device-code` | Body: `client_id`, `scope` hint, optional `agent_template` → `{ device_code, user_code, verification_uri, interval, expires_in }` |
| User | browser | `/device` or printed URL | User logs in with existing session / password; consents to agent scopes + drive allowlist |
| Poll | `POST` | `/v1/auth/device-token` | Body: `device_code`, `client_id` → Agent Token (+ refresh if we add agent refresh) or `authorization_pending` |
| Revoke | existing | agent disable / token version | No new surface required |

### Authorization-code + PKCE (browser hosts)

| Step | Method | Path | Notes |
|------|--------|------|-------|
| Authorize | `GET` | `/v1/oauth/authorize` | `response_type=code`, `client_id`, `redirect_uri`, `state`, `code_challenge`, `code_challenge_method=S256`, scope/drive consent UI |
| Callback | host | registered `redirect_uri` | `?code=&state=` only — **no secrets in query beyond one-time code** |
| Exchange | `POST` | `/v1/oauth/token` | `grant_type=authorization_code`, `code`, `code_verifier`, `client_id` → Agent Token JWT |
| Refresh (optional) | `POST` | `/v1/oauth/token` | Only if we introduce **agent refresh** tokens bound to agent_id + version |

### Security invariants

- **PKCE required** for public clients (Cursor/Claude plugins are public).
- **No provider SecretKey** ever sent to the host; host only gets Agent Token + drive metadata.
- **Redirect URI allowlist** per `client_id`; localhost loopback OK for Phase 1.
- **Consent screen** must show scopes and drive allowlist (or “create agent with these drives”).
- **One-time codes**, short TTL, bind to `code_challenge`.
- Prefer minting via existing `IssueAgentToken` so revoke/disable semantics stay unified.

---

## Phased plan

### Phase 0 — Cursor manual token (now)

- `scripts/cursor-mcp-install.sh` + `deploy/cursor/mcp.json.example` + [CURSOR-MCP.md](./CURSOR-MCP.md)
- User: register/login API → create agent → mint token → paste into MCP env
- HTTP: `GET /v1/drives/by-alias/{alias}` for stable 「A 盘」 resolution without list filtering

### Phase 1 — Device-code or OAuth against AI-cloudhub

- Implement device-code **or** auth-code+PKCE against **this** control plane (not a third-party IdP first).
- Tiny stub only if it fits cleanly on `internal/auth` + tests; otherwise wait for a real host callback consumer.
- Ship a `client_id=cursor-mcp` (public) with loopback redirect / device verification URI.

### Phase 2 — Marketplace listing

- Package metadata for Cursor / Claude **plugin directories** when their formats stabilize.
- Listing is marketing + install deep-link into Phase 1; it is **not** a substitute for Agent Token security.

---

## Cursor / Claude marketplace realities (honest)

| Topic | Reality |
|-------|---------|
| Cursor MCP | Configured via `mcp.json` / settings; **no official “OAuth install for arbitrary MCP”** that mints our Agent Tokens today |
| Claude Desktop | Similar MCP JSON config; marketplace ≠ automatic AI-cloudhub login |
| “App store one-click” | Usually means **install binary + open config**; login remains product work (Phase 1) |
| Out of scope for D-003 | Shipping a fake store listing, scraping host sessions, or embedding long-lived human JWTs in the plugin package |

D-003 product narrative (plugin for agents / drive-letter UX) is satisfied by **alias + MCP + install script + this OAuth design**, not by pretending a marketplace SSO exists.

---

## Decision

**Design doc only** for OAuth. Do **not** implement authorize/callback/token endpoints until Phase 1 has a concrete host consumer and tests. Phase 0 install script is the supported path.
