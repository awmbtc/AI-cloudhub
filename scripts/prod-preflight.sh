#!/usr/bin/env bash
# Production env preflight (no API start required for core checks).
# Exit 0 if minimum security/env look production-ready; 1 with actionable list otherwise.
#
# Docs: docs/PRODUCTION.md · docs/CUTOVER.md
set -euo pipefail

FAIL=0
WARN=0
ok() { echo "  OK  $*"; }
bad() { echo "  FAIL $*"; FAIL=$((FAIL + 1)); }
warn() { echo "  WARN $*"; WARN=$((WARN + 1)); }

echo "== AI-cloudhub production preflight =="

# --- required secrets ---
if [[ -z "${JWT_SECRET:-}" ]]; then
  bad "JWT_SECRET unset"
elif [[ ${#JWT_SECRET} -lt 16 ]]; then
  bad "JWT_SECRET too short (<16)"
elif [[ "$JWT_SECRET" == *"change-me"* || "$JWT_SECRET" == "secret" || "$JWT_SECRET" == "dev" || "$JWT_SECRET" == "dev-change-me" ]]; then
  bad "JWT_SECRET looks like a default/dev value"
else
  ok "JWT_SECRET length=${#JWT_SECRET}"
  if [[ ${#JWT_SECRET} -lt 32 ]]; then
    warn "JWT_SECRET length < 32 — prefer openssl rand -hex 32"
  fi
fi

if [[ -z "${AI_CLOUDHUB_MASTER_KEY:-}" ]]; then
  bad "AI_CLOUDHUB_MASTER_KEY unset (provider secrets at rest)"
else
  ok "AI_CLOUDHUB_MASTER_KEY set (len=${#AI_CLOUDHUB_MASTER_KEY})"
  if [[ ${#AI_CLOUDHUB_MASTER_KEY} -lt 24 ]]; then
    warn "AI_CLOUDHUB_MASTER_KEY short — prefer openssl rand -base64 32"
  fi
fi

STRICT="${AI_CLOUDHUB_STRICT:-0}"
if [[ "$STRICT" != "1" && "$STRICT" != "true" && "$STRICT" != "yes" ]]; then
  bad "AI_CLOUDHUB_STRICT not enabled (set to 1 for prod)"
else
  ok "AI_CLOUDHUB_STRICT on"
fi

REG="${AI_CLOUDHUB_ALLOW_REGISTER:-1}"
if [[ "$REG" == "1" || "$REG" == "true" || "$REG" == "yes" || -z "${AI_CLOUDHUB_ALLOW_REGISTER+x}" ]]; then
  # unset defaults to open in API; treat as warn
  if [[ -z "${AI_CLOUDHUB_ALLOW_REGISTER:-}" || "$REG" == "1" || "$REG" == "true" || "$REG" == "yes" ]]; then
    warn "AI_CLOUDHUB_ALLOW_REGISTER open or unset — close after bootstrap (set 0)"
  fi
else
  ok "AI_CLOUDHUB_ALLOW_REGISTER closed"
fi

if [[ -z "${AI_CLOUDHUB_METRICS_TOKEN:-}" ]]; then
  warn "AI_CLOUDHUB_METRICS_TOKEN unset — /metrics is public"
else
  ok "AI_CLOUDHUB_METRICS_TOKEN set"
fi

if [[ -z "${AI_CLOUDHUB_ADMIN_CIDRS:-}" ]]; then
  warn "AI_CLOUDHUB_ADMIN_CIDRS unset — admin APIs not IP-restricted"
else
  ok "AI_CLOUDHUB_ADMIN_CIDRS set"
fi

if [[ "${AI_CLOUDHUB_HSTS:-0}" == "1" || "${AI_CLOUDHUB_HSTS:-}" == "true" ]]; then
  ok "AI_CLOUDHUB_HSTS on (only behind HTTPS terminator)"
else
  warn "AI_CLOUDHUB_HSTS off — enable if TLS terminates at/before this process"
fi

# --- data plane ---
DB="${AI_CLOUDHUB_DB:-}"
if [[ -z "$DB" ]]; then
  warn "AI_CLOUDHUB_DB unset — default SQLite (single-writer; not multi-replica)"
elif [[ "$DB" == postgres://* || "$DB" == postgresql://* ]]; then
  ok "AI_CLOUDHUB_DB postgres"
  if [[ "$DB" == *"aihub:aihub@"* || "$DB" == *":aihub@"* ]]; then
    warn "Postgres DSN may use compose default password — rotate for real prod"
  fi
else
  ok "AI_CLOUDHUB_DB set (${DB%%\?*})"
fi

if [[ -n "${AI_CLOUDHUB_REDIS:-}" ]]; then
  ok "AI_CLOUDHUB_REDIS set (shared rate limit)"
else
  warn "AI_CLOUDHUB_REDIS unset — per-process limiter only"
fi

# --- CORS (rarely wanted open in pure API deploys) ---
if [[ -n "${AI_CLOUDHUB_CORS_ORIGINS:-}" || -n "${AI_CLOUDHUB_CORS:-}" ]]; then
  warn "CORS env set — confirm origins are intentional for browser clients"
fi

# --- Job webhook (optional; secret must pair with URL) ---
WH_URL="${AI_CLOUDHUB_JOB_WEBHOOK_URL:-}"
WH_SEC="${AI_CLOUDHUB_JOB_WEBHOOK_SECRET:-}"
if [[ -n "$WH_URL" ]]; then
  ok "AI_CLOUDHUB_JOB_WEBHOOK_URL set"
  if [[ -z "$WH_SEC" ]]; then
    bad "AI_CLOUDHUB_JOB_WEBHOOK_URL set but AI_CLOUDHUB_JOB_WEBHOOK_SECRET empty (unsigned webhooks)"
  else
    ok "AI_CLOUDHUB_JOB_WEBHOOK_SECRET set"
  fi
else
  ok "job webhook URL unset (optional)"
fi

# --- Stage C / payments (optional but called out) ---
if [[ -n "${AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET:-}" ]]; then
  ok "AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET set"
  if [[ -n "${AI_CLOUDHUB_STRIPE_SECRET_KEY:-}${STRIPE_SECRET_KEY:-}" ]]; then
    ok "Stripe secret key set (live Checkout Session)"
  else
    warn "no STRIPE secret key — checkout_url is mock only"
  fi
else
  warn "AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET unset — paid marketplace webhooks disabled unless ALLOW_INSECURE"
fi

if [[ -n "${AI_CLOUDHUB_PDP_URL:-}" ]]; then
  ok "AI_CLOUDHUB_PDP_URL set (remote PDP)"
fi

if [[ -n "${AI_CLOUDHUB_POLICY_FILE:-}" ]]; then
  if [[ -f "${AI_CLOUDHUB_POLICY_FILE}" ]]; then
    ok "AI_CLOUDHUB_POLICY_FILE exists"
  else
    bad "AI_CLOUDHUB_POLICY_FILE path missing: $AI_CLOUDHUB_POLICY_FILE"
  fi
fi

if [[ -n "${AI_CLOUDHUB_OPA_POLICY_FILE:-}" ]]; then
  if [[ -f "${AI_CLOUDHUB_OPA_POLICY_FILE}" ]]; then
    ok "AI_CLOUDHUB_OPA_POLICY_FILE exists"
  else
    bad "AI_CLOUDHUB_OPA_POLICY_FILE path missing: $AI_CLOUDHUB_OPA_POLICY_FILE"
  fi
fi

# --- listen / bind hints ---
HTTP_ADDR="${HTTP_ADDR:-}"
if [[ -n "$HTTP_ADDR" ]]; then
  ok "HTTP_ADDR=$HTTP_ADDR"
  if [[ "$HTTP_ADDR" == ":8080" || "$HTTP_ADDR" == "0.0.0.0:8080" || "$HTTP_ADDR" == "[::]:8080" ]]; then
    warn "HTTP_ADDR binds all interfaces — prefer 127.0.0.1 behind TLS proxy"
  fi
fi

# --- live API checks (optional) ---
API="${AI_CLOUDHUB_API:-http://127.0.0.1:8080}"
if curl -sf --noproxy '*' --max-time 2 "$API/healthz" >/dev/null 2>&1; then
  ok "healthz reachable at $API"
  if curl -sf --noproxy '*' --max-time 2 "$API/readyz" >/dev/null 2>&1; then
    ok "readyz OK"
  else
    bad "readyz failed at $API"
  fi
  VER=$(curl -sS --noproxy '*' --max-time 2 "$API/healthz" 2>/dev/null | python3 -c 'import sys,json; print(json.load(sys.stdin).get("version",""))' 2>/dev/null || true)
  if [[ -n "$VER" ]]; then
    ok "api version=$VER"
  fi
  # metrics gate probe
  code=$(curl -sS -o /dev/null -w "%{http_code}" --noproxy '*' --max-time 2 "$API/metrics" 2>/dev/null || echo "000")
  if [[ -n "${AI_CLOUDHUB_METRICS_TOKEN:-}" ]]; then
    if [[ "$code" == "401" || "$code" == "403" ]]; then
      ok "/metrics rejects unauthenticated ($code)"
    elif [[ "$code" == "200" ]]; then
      warn "/metrics returned 200 without token — token may not be loaded on running process"
    fi
    code2=$(curl -sS -o /dev/null -w "%{http_code}" --noproxy '*' --max-time 2 \
      -H "Authorization: Bearer ${AI_CLOUDHUB_METRICS_TOKEN}" "$API/metrics" 2>/dev/null || echo "000")
    if [[ "$code2" == "200" ]]; then
      ok "/metrics accepts metrics token"
    else
      warn "/metrics with token got HTTP $code2"
    fi
  else
    if [[ "$code" == "200" ]]; then
      warn "/metrics open (HTTP 200) — set AI_CLOUDHUB_METRICS_TOKEN"
    fi
  fi
else
  warn "API not reachable at $API (start control plane to verify healthz/readyz)"
fi

echo ""
if [[ $FAIL -ne 0 ]]; then
  echo "RESULT: FAIL ($FAIL hard issues, $WARN warnings)"
  echo "See docs/PRODUCTION.md · docs/CUTOVER.md"
  exit 1
fi
if [[ $WARN -ne 0 ]]; then
  echo "RESULT: PASS with $WARN warning(s) — review before cutover"
  exit 0
fi
echo "RESULT: PASS"
exit 0
