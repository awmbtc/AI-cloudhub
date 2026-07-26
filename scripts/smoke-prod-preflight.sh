#!/usr/bin/env bash
# Regression for scripts/prod-preflight.sh (no long-lived API required).
# 1) Weak env → must FAIL
# 2) Strong env → must PASS (warnings allowed)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PF="$ROOT/scripts/prod-preflight.sh"
chmod +x "$PF"

echo "== weak env must FAIL =="
set +e
(
  unset JWT_SECRET AI_CLOUDHUB_MASTER_KEY AI_CLOUDHUB_STRICT AI_CLOUDHUB_ALLOW_REGISTER
  unset AI_CLOUDHUB_METRICS_TOKEN AI_CLOUDHUB_ADMIN_CIDRS AI_CLOUDHUB_DB AI_CLOUDHUB_REDIS
  unset AI_CLOUDHUB_JOB_WEBHOOK_URL AI_CLOUDHUB_JOB_WEBHOOK_SECRET
  export AI_CLOUDHUB_API=http://127.0.0.1:1   # unreachable
  "$PF"
) >/tmp/aihub-preflight-weak.out 2>&1
rc=$?
set -e
if [[ $rc -eq 0 ]]; then
  echo "expected FAIL on weak env, got PASS" >&2
  cat /tmp/aihub-preflight-weak.out >&2
  exit 1
fi
grep -q "RESULT: FAIL" /tmp/aihub-preflight-weak.out
echo "weak env FAIL ok"

echo "== webhook URL without secret must FAIL =="
set +e
(
  export JWT_SECRET="$(openssl rand -hex 32 2>/dev/null || echo 'abcdefghijklmnopqrstuvwxyz012345')"
  export AI_CLOUDHUB_MASTER_KEY="$(openssl rand -base64 32 2>/dev/null || echo 'master-key-material-32bytes!!!!')"
  export AI_CLOUDHUB_STRICT=1
  export AI_CLOUDHUB_ALLOW_REGISTER=0
  export AI_CLOUDHUB_METRICS_TOKEN=tok
  export AI_CLOUDHUB_ADMIN_CIDRS=127.0.0.1
  export AI_CLOUDHUB_DB=postgres://u:p@localhost:5432/db
  export AI_CLOUDHUB_REDIS=redis://localhost:6379/0
  export AI_CLOUDHUB_JOB_WEBHOOK_URL=https://example.com/hook
  unset AI_CLOUDHUB_JOB_WEBHOOK_SECRET
  export AI_CLOUDHUB_API=http://127.0.0.1:1
  "$PF"
) >/tmp/aihub-preflight-wh.out 2>&1
rc=$?
set -e
if [[ $rc -eq 0 ]]; then
  echo "expected FAIL when webhook secret missing" >&2
  cat /tmp/aihub-preflight-wh.out >&2
  exit 1
fi
grep -q "JOB_WEBHOOK_SECRET" /tmp/aihub-preflight-wh.out
echo "webhook secret required ok"

echo "== strong env must PASS =="
set +e
(
  export JWT_SECRET="$(openssl rand -hex 32 2>/dev/null || echo 'abcdefghijklmnopqrstuvwxyz012345')"
  export AI_CLOUDHUB_MASTER_KEY="$(openssl rand -base64 32 2>/dev/null || echo 'master-key-material-32bytes-long!!')"
  export AI_CLOUDHUB_STRICT=1
  export AI_CLOUDHUB_ALLOW_REGISTER=0
  export AI_CLOUDHUB_METRICS_TOKEN="$(openssl rand -hex 8 2>/dev/null || echo 'metricstok')"
  export AI_CLOUDHUB_ADMIN_CIDRS=127.0.0.1
  export AI_CLOUDHUB_DB=postgres://produser:notdefault@db.internal:5432/aihub
  export AI_CLOUDHUB_REDIS=redis://redis.internal:6379/0
  export AI_CLOUDHUB_HSTS=1
  unset AI_CLOUDHUB_JOB_WEBHOOK_URL AI_CLOUDHUB_JOB_WEBHOOK_SECRET
  export AI_CLOUDHUB_STRIPE_WEBHOOK_SECRET=whsec_test
  export AI_CLOUDHUB_STRIPE_SECRET_KEY=sk_test_x
  export AI_CLOUDHUB_API=http://127.0.0.1:1
  "$PF"
) >/tmp/aihub-preflight-strong.out 2>&1
rc=$?
set -e
if [[ $rc -ne 0 ]]; then
  echo "expected PASS on strong env" >&2
  cat /tmp/aihub-preflight-strong.out >&2
  exit 1
fi
grep -qE "RESULT: PASS" /tmp/aihub-preflight-strong.out
echo "strong env PASS ok"

echo "OK smoke-prod-preflight"
