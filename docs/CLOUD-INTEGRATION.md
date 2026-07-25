# Multi-cloud integration runbooks

Copy-paste ops guides for **Aliyun OSS**, **Tencent COS**, **Qiniu Kodo**, and **Oracle OCI**.

Full STS matrix / sources: [STS.md](./STS.md) · Policy: [POLICY.md](./POLICY.md) · Prod secrets: [PRODUCTION.md](./PRODUCTION.md) · Vendor catalog: [VENDORS.md](./VENDORS.md) · Agent path: [QUICKSTART-AGENT.md](./QUICKSTART-AGENT.md).

**Design contract (do not fight it):**

| Rule | Meaning |
|------|---------|
| Best-effort STS | Native / S3-compat STS never blocks `POST …/session`. Failure → `source=embedded` (or `refresh`) + `session.note` |
| Short-lived only | Runtime must refresh before `expires_at` and destroy rclone conf on unmount |
| BYOS | Control plane never proxies object bytes; client ↔ your store |
| Secrets at rest | Prefer `AI_CLOUDHUB_MASTER_KEY` so provider AK/SK are envelope-encrypted |
| RoleArn is **env**, not provider JSON | Native / S3 STS RoleArn comes from `AI_CLOUDHUB_*_STS_ROLE_ARN` (see below) — not a field on `POST /v1/providers` |

Truthy env values: `1` / `true` / `yes`.

Assume API at `http://127.0.0.1:8080` and `$TOKEN` from `POST /v1/auth/login`.

---

## Which path should I use?

```text
Need mount via rclone/hubd?
  ├─ Aliyun OSS  → type=oss  + AI_CLOUDHUB_OSS_NATIVE_STS=1 + acs:ram:: RoleArn
  ├─ Tencent COS → type=cos  + AI_CLOUDHUB_COS_NATIVE_STS=1 + qcs::cam:: RoleArn
  ├─ Qiniu Kodo  → type=qiniu + (optional) AI_CLOUDHUB_QINIU_STS=1  [S3-compat]
  └─ Oracle OCI  → type=oracle + Customer Secret Keys on provider
                   optional AI_CLOUDHUB_ORACLE_STS=1
                   optional AI_CLOUDHUB_ORACLE_NATIVE_IAM=1 (identity proof only)

Need single-object private download for agents?
  └─ Qiniu → POST /v1/drives/{id}/objects/presign-get  (method=qiniu_download, no extra env)

Only need to prove OCI API key works?
  └─ AI_CLOUDHUB_ORACLE_NATIVE_IAM=1 + tenancy/user/fingerprint/PEM  → source=oci_iam

Stage C (optional, best-effort, requires IAM rights):
  ├─ AI_CLOUDHUB_OCI_CREATE_SECRET=1  → mint Customer Secret when provider AK empty (oci_secret)
  └─ AI_CLOUDHUB_OCI_PAR=1 + NAMESPACE + PAR_BUCKET → ObjectRead PAR sample in session.note
```

### Endpoint cheat sheet

| Vendor | Example data-plane endpoint | Notes |
|--------|----------------------------|--------|
| OSS | `oss-cn-hangzhou.aliyuncs.com` | Virtual-hosted default |
| COS | `cos.ap-guangzhou.myqcloud.com` | Bucket often `name-appid` |
| Qiniu S3 | `s3-cn-east-1.qiniucs.com` | Path-style; mount |
| Qiniu CDN | `xxx.bkt.clouddn.com` / custom | Better for private GET URL signing |
| OCI S3 | `<ns>.compat.objectstorage.<region>.oraclecloud.com` | Path-style; Customer Secret Key |

### Offline / CI verification (no real cloud)

| Check | Command / behavior |
|-------|-------------------|
| Qiniu HMAC presign | `make smoke-objects` → `method=qiniu_download` |
| MCP Qiniu + jobs | `make smoke-mcp` |
| Full agent path | `make smoke-quickstart-agent` (this pack) or `make smoke-agent` |
| STS unit tests | `go test ./internal/sts/ -count=1` |

---

## Shared bootstrap

```bash
export API=http://127.0.0.1:8080
export TOKEN='…'   # Bearer from login / agent token with drive scopes

# Encrypt provider secrets at rest (strongly recommended in prod)
export AI_CLOUDHUB_MASTER_KEY="$(openssl rand -base64 32)"

# Catalog (fields per type)
curl -sS "$API/v1/providers/catalog" -H "Authorization: Bearer $TOKEN" | python3 -m json.tool
```

Session response shape (all vendors):

```json
{
  "session": {
    "id": "…",
    "source": "aliyun_sts|tencent_sts|qiniu_sts|qiniu_download|oracle_sts|oci_iam|s3_sts|embedded|…",
    "note": "optional human hint when STS skipped/failed",
    "expires_at": "…",
    "token": "…",
    "spec": { "rclone_conf": "…", "remote_path": "…", "mount_point": "…" },
    "manifest": { }
  }
}
```

Refresh: `POST /v1/sessions/refresh` with `{ "session_token", "drive_id" }` → new `source` may be `refresh` (fallback) or a successful STS label again.

---

## 1. Aliyun OSS (native RAM STS)

### Provider fields

| Field | Required | Notes |
|-------|----------|--------|
| `type` | yes | `oss` |
| `credentials.access_key` | yes | RAM user AK with `sts:AssumeRole` |
| `credentials.secret_key` | yes | |
| `credentials.endpoint` | yes | e.g. `oss-cn-hangzhou.aliyuncs.com` |
| `credentials.region` | optional | default falls back to `us-east-1` for S3 clients; prefer real region (`cn-hangzhou`) |
| `force_path_style` | optional | default **false** (virtual-hosted) |

Drive: `bucket` = OSS bucket name; optional `prefix`, `mount_point`.

### Env flags → `session.source`

| Env | Purpose | Success `source` |
|-----|---------|------------------|
| `AI_CLOUDHUB_OSS_NATIVE_STS=1` (or `AI_CLOUDHUB_ALIYUN_STS=1`) | Prefer Aliyun RAM STS | **`aliyun_sts`** |
| `AI_CLOUDHUB_OSS_STS_ROLE_ARN` / `AI_CLOUDHUB_ALIYUN_STS_ROLE_ARN` / `AI_CLOUDHUB_S3_STS_ROLE_ARN` | RoleArn **`acs:ram::<uid>:role/<name>`** | required for native |
| `AI_CLOUDHUB_ALIYUN_STS_ENDPOINT` | Override (default `https://sts.aliyuncs.com`) | tests / regional |
| `AI_CLOUDHUB_OSS_STS=1` or `AI_CLOUDHUB_S3_STS=1` | Fallback S3-compat AssumeRole on data endpoint | `s3_sts` |

Native is preferred when the NATIVE flag is on **or** RoleArn looks like `acs:ram::…`.

### Example curl

```bash
# API process (separate shell)
export AI_CLOUDHUB_OSS_NATIVE_STS=1
export AI_CLOUDHUB_OSS_STS_ROLE_ARN='acs:ram::1234567890123456:role/ai-cloudhub-oss'
# optional fallback:
# export AI_CLOUDHUB_OSS_STS=1
./.bin/api

# Create provider
PID=$(curl -sS -X POST "$API/v1/providers" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{
    "name": "oss-hz",
    "type": "oss",
    "credentials": {
      "access_key": "'"$OSS_AK"'",
      "secret_key": "'"$OSS_SK"'",
      "endpoint": "oss-cn-hangzhou.aliyuncs.com",
      "region": "cn-hangzhou"
    }
  }' | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

# Create drive
DID=$(curl -sS -X POST "$API/v1/drives" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"oss-ws\",\"provider_id\":\"$PID\",\"bucket\":\"my-bucket\",\"prefix\":\"agent/\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

# Issue session
curl -sS -X POST "$API/v1/drives/$DID/session" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"mount_point":"/workspace","device_id":"ops-1","mode":"mount"}' \
  | python3 -c 'import sys,json; s=json.load(sys.stdin)["session"]; print(s["source"], s.get("note",""), s["expires_at"])'
# expect: aliyun_sts
```

### Common failures → `session.note`

| Situation | Typical `source` | `session.note` (substring) |
|-----------|------------------|----------------------------|
| No native flag and no S3-compat flag | `embedded` | `Aliyun OSS: set AI_CLOUDHUB_OSS_NATIVE_STS=1 + RoleArn (acs:ram::…)…` |
| Native on, RoleArn missing/wrong, no S3 fallback | `embedded` | `Aliyun STS AssumeRole failed; using embedded credentials…` |
| Native fail + `OSS_STS`/`S3_STS` also fail | `embedded` | `Aliyun native + S3-compatible STS failed…` |
| Native fail + S3-compat succeeds | `s3_sts` | empty (or prior context cleared) |

RAM console checklist: trusted entity can be assumed by the AK principal; policy allows OSS on the bucket; max session duration ≥ requested TTL (clamped 900–3600s for Aliyun).

### Security notes

- Store long-lived RAM AK only behind `AI_CLOUDHUB_MASTER_KEY`.
- Prefer a **role** with least privilege on one bucket/prefix; never ship root account keys to agents.
- Session embeds temporary AK/SK/token into rclone conf — treat conf as secret, delete on unmount.
- Metrics: `aicloudhub_sts_source_total{source="aliyun_sts"}`.

---

## 2. Tencent COS (native CAM STS)

### Provider fields

| Field | Required | Notes |
|-------|----------|--------|
| `type` | yes | `cos` |
| `credentials.access_key` | yes | CAM SecretId |
| `credentials.secret_key` | yes | SecretKey |
| `credentials.endpoint` | yes | e.g. `cos.ap-guangzhou.myqcloud.com` |
| `credentials.region` | optional | also used for TC3 STS region; default STS region `ap-guangzhou` if empty |
| `force_path_style` | optional | default **false** |

### Env flags → `session.source`

| Env | Purpose | Success `source` |
|-----|---------|------------------|
| `AI_CLOUDHUB_COS_NATIVE_STS=1` (or `AI_CLOUDHUB_TENCENT_STS=1`) | Prefer CAM STS | **`tencent_sts`** |
| `AI_CLOUDHUB_COS_STS_ROLE_ARN` / `AI_CLOUDHUB_TENCENT_STS_ROLE_ARN` / `AI_CLOUDHUB_S3_STS_ROLE_ARN` | RoleArn **`qcs::cam::uin/<uin>:roleName/<name>`** | required for native |
| `AI_CLOUDHUB_TENCENT_STS_ENDPOINT` | Override host (default `sts.tencentcloudapi.com`) | |
| `AI_CLOUDHUB_TENCENT_STS_REGION` | STS API region if provider region empty | default `ap-guangzhou` |
| `AI_CLOUDHUB_COS_STS=1` or `AI_CLOUDHUB_S3_STS=1` | S3-compat AssumeRole fallback | `s3_sts` |

Native when NATIVE flag is on **or** RoleArn looks like `qcs::cam::…` / contains `:roleName/`.

### Example curl

```bash
export AI_CLOUDHUB_COS_NATIVE_STS=1
export AI_CLOUDHUB_COS_STS_ROLE_ARN='qcs::cam::uin/100000000001:roleName/ai-cloudhub-cos'
./.bin/api

PID=$(curl -sS -X POST "$API/v1/providers" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{
    "name": "cos-gz",
    "type": "cos",
    "credentials": {
      "access_key": "'"$COS_SID"'",
      "secret_key": "'"$COS_SKEY"'",
      "endpoint": "cos.ap-guangzhou.myqcloud.com",
      "region": "ap-guangzhou"
    }
  }' | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

DID=$(curl -sS -X POST "$API/v1/drives" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"cos-ws\",\"provider_id\":\"$PID\",\"bucket\":\"mybucket-1250000000\",\"prefix\":\"ws/\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

curl -sS -X POST "$API/v1/drives/$DID/session" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"mount_point":"/workspace","device_id":"ops-1"}' \
  | python3 -c 'import sys,json; s=json.load(sys.stdin)["session"]; print(s["source"], s.get("note",""))'
# expect: tencent_sts
```

### Common failures → `session.note`

| Situation | Typical `source` | `session.note` |
|-----------|------------------|----------------|
| Flags off | `embedded` | `Tencent COS: set AI_CLOUDHUB_COS_NATIVE_STS=1 + RoleArn (qcs::cam::…)…` |
| AssumeRole error, no S3 fallback | `embedded` | `Tencent STS AssumeRole failed; using embedded credentials…` |
| Native + S3 both fail | `embedded` | `Tencent native + S3-compatible STS failed…` |

Duration clamped (typically ≤7200s). Bucket name usually includes appid (`bucket-appid`).

### Security notes

- CAM role trust policy must allow the SecretId principal to call `sts:AssumeRole`.
- Temporary SecretId/Key/Token land in session rclone conf — short TTL only.
- Use `AI_CLOUDHUB_MASTER_KEY` for the long-lived CAM keys at rest.

---

## 3. Qiniu Kodo (S3 STS + download token + presign-get)

Qiniu has **two** useful paths:

1. **Mount / rclone**: S3-compatible credentials (embedded AK/SK or S3 AssumeRole → `qiniu_sts` / `s3_sts`).
2. **Single-object private GET**: native HMAC download URL via `POST …/objects/presign-get` → `method=qiniu_download` (**always** for `type=qiniu` without `version_id`; no env required).

### Provider fields

| Field | Required | Notes |
|-------|----------|--------|
| `type` | yes | `qiniu` |
| `credentials.access_key` | yes | Kodo AccessKey |
| `credentials.secret_key` | yes | SecretKey |
| `credentials.endpoint` | yes | S3 API: `s3-cn-east-1.qiniucs.com` (path-style); **or** CDN/custom download domain for native GET |
| `credentials.region` | optional | |
| `force_path_style` | optional | default **true** |

### Env flags → `session.source`

| Env | Purpose | Success / assist `source` |
|-----|---------|---------------------------|
| `AI_CLOUDHUB_QINIU_STS=1` | S3-compat AssumeRole | **`qiniu_sts`** (if only generic S3 flag → `s3_sts`) |
| `AI_CLOUDHUB_S3_STS=1` | Same, all S3-compat vendors | `s3_sts` unless per-vendor sets `qiniu_sts` |
| `AI_CLOUDHUB_QINIU_STS_ROLE_ARN` / `AI_CLOUDHUB_S3_STS_ROLE_ARN` | RoleArn if gateway requires it | |
| `AI_CLOUDHUB_QINIU_STS_ENDPOINT` / `AI_CLOUDHUB_S3_STS_ENDPOINT` | STS host override | |
| `AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN=1` | Session **note assist** + may set `source=qiniu_download` when S3 STS not used/successful | **`qiniu_download`** |

Object-level `presign-get` does **not** depend on `AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN`.

### Example curl

```bash
# Optional for mount STS + session note assist
export AI_CLOUDHUB_QINIU_STS=1
export AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN=1
./.bin/api

# Provider: S3 endpoint for mount; use CDN host if you mainly care about private download URLs
PID=$(curl -sS -X POST "$API/v1/providers" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{
    "name": "kodo-east",
    "type": "qiniu",
    "credentials": {
      "access_key": "'"$QINIU_AK"'",
      "secret_key": "'"$QINIU_SK"'",
      "endpoint": "s3-cn-east-1.qiniucs.com",
      "region": "cn-east-1"
    }
  }' | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

DID=$(curl -sS -X POST "$API/v1/drives" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"kodo-ws\",\"provider_id\":\"$PID\",\"bucket\":\"my-kodo-bucket\",\"prefix\":\"ws/\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

# Session (mount)
curl -sS -X POST "$API/v1/drives/$DID/session" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"mount_point":"/workspace"}' \
  | python3 -c 'import sys,json; s=json.load(sys.stdin)["session"]; print("source=",s["source"]); print("note=",s.get("note",""))'

# Object private download (native HMAC) — preferred for agents
curl -sS -X POST "$API/v1/drives/$DID/objects/presign-get" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"key":"pic.png","ttl_min":10}' \
  | python3 -m json.tool
```

Expected `presign-get` (no `version_id`):

```json
{
  "method": "qiniu_download",
  "url": "https://…/ws/pic.png?e=…&token=AK:…",
  "deadline": 1710000000,
  "expires_in": 600,
  "key": "ws/pic.png",
  "bucket": "my-kodo-bucket"
}
```

URL shape:

- Host contains `qiniucs` / `s3.` → path-style `https://host/bucket/key`
- CDN / custom domain → `https://host/key` (bucket DNS-bound)

With `version_id` set → **`method=s3_presign`** instead (S3-compatible).

Offline smoke (no live Kodo): `make smoke-objects` asserts `method=qiniu_download`.

### Common failures → `session.note`

| Situation | Typical `source` | `session.note` |
|-----------|------------------|----------------|
| No QINIU/S3 STS and no download flag | `embedded` | `Qiniu Kodo: set AI_CLOUDHUB_QINIU_STS=1 … and/or AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN=1…` |
| S3 AssumeRole fails | `embedded` (or `qiniu_download` if download flag on) | `Qiniu S3-compatible STS AssumeRole failed…` (+ optional download sample) |
| Download assist on | `qiniu_download` (when no STS success) | `Qiniu private download token (source=qiniu_download): sample_signed_url_prefix=…` |

### Security notes

- Private download tokens are **URL-bound + deadline**; still treat as capability secrets.
- Do not enable download-token assist if session notes are logged widely (sample URL is truncated but still sensitive).
- Prefer short `ttl_min` on `presign-get` (service clamps; default ~15 min).
- MASTER_KEY for stored AK/SK; never log full `token=` query strings in production.

---

## 4. Oracle OCI (S3-compat + ORACLE_NATIVE_IAM)

OCI Object Storage is used in two layers:

1. **Mount**: S3 compatibility endpoint + customer secret keys (AK/SK) on the provider; optional S3 AssumeRole → `oracle_sts` / `s3_sts`.
2. **Identity proof**: RSA API-key env → validate user via Identity API → `source=oci_iam` (does **not** mint S3 secrets or PARs).

### Provider fields

| Field | Required | Notes |
|-------|----------|--------|
| `type` | yes | `oracle` |
| `credentials.access_key` | yes* | OCI **Customer Secret Key** access key (*required for rclone mount) |
| `credentials.secret_key` | yes* | Matching secret |
| `credentials.endpoint` | yes | `<namespace>.compat.objectstorage.<region>.oraclecloud.com` |
| `credentials.region` | optional | default **`us-ashburn-1`** |
| `force_path_style` | optional | default **true** |

### Env flags → `session.source`

| Env | Purpose | Success `source` |
|-----|---------|------------------|
| `AI_CLOUDHUB_ORACLE_STS=1` | S3-compat AssumeRole | **`oracle_sts`** |
| `AI_CLOUDHUB_S3_STS=1` | Generic S3-compat | `s3_sts` (unless per-vendor) |
| `AI_CLOUDHUB_ORACLE_STS_ROLE_ARN` / `AI_CLOUDHUB_S3_STS_ROLE_ARN` | RoleArn if needed | |
| `AI_CLOUDHUB_ORACLE_STS_ENDPOINT` / `AI_CLOUDHUB_S3_STS_ENDPOINT` | STS host override | |
| `AI_CLOUDHUB_ORACLE_NATIVE_IAM=1` (or `AI_CLOUDHUB_OCI_IAM=1`) | RSA API-key validation | **`oci_iam`** when no S3 STS success; if S3 STS wins, source stays `oracle_sts`/`s3_sts` and note appends validation |
| `AI_CLOUDHUB_OCI_TENANCY_OCID` | Tenancy OCID | required for native IAM |
| `AI_CLOUDHUB_OCI_USER_OCID` | User OCID | |
| `AI_CLOUDHUB_OCI_FINGERPRINT` | API key fingerprint | |
| `AI_CLOUDHUB_OCI_PRIVATE_KEY_PEM` or `AI_CLOUDHUB_OCI_PRIVATE_KEY_FILE` | RSA private key | |
| `AI_CLOUDHUB_OCI_REGION` | Identity region | default `us-ashburn-1` |
| `AI_CLOUDHUB_OCI_IDENTITY_ENDPOINT` | Override Identity base URL | tests |
| `AI_CLOUDHUB_OCI_IAM_CACHE_SEC` | Cache successful validate | default `300`; `0` = off |

### Example curl

```bash
# S3 mount path
export AI_CLOUDHUB_ORACLE_STS=1   # optional

# Optional native IAM proof (API process env — not stored on provider JSON)
export AI_CLOUDHUB_ORACLE_NATIVE_IAM=1
export AI_CLOUDHUB_OCI_TENANCY_OCID='ocid1.tenancy.oc1..…'
export AI_CLOUDHUB_OCI_USER_OCID='ocid1.user.oc1..…'
export AI_CLOUDHUB_OCI_FINGERPRINT='aa:bb:cc:…'
export AI_CLOUDHUB_OCI_PRIVATE_KEY_FILE=/secure/oci_api_key.pem
export AI_CLOUDHUB_OCI_REGION=us-ashburn-1
./.bin/api

PID=$(curl -sS -X POST "$API/v1/providers" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{
    "name": "oci-ash",
    "type": "oracle",
    "credentials": {
      "access_key": "'"$OCI_S3_AK"'",
      "secret_key": "'"$OCI_S3_SK"'",
      "endpoint": "mynamespace.compat.objectstorage.us-ashburn-1.oraclecloud.com",
      "region": "us-ashburn-1"
    }
  }' | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

DID=$(curl -sS -X POST "$API/v1/drives" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"oci-ws\",\"provider_id\":\"$PID\",\"bucket\":\"my-bucket\",\"prefix\":\"ws/\",\"mount_point\":\"/workspace\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')

curl -sS -X POST "$API/v1/drives/$DID/session" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"mount_point":"/workspace"}' \
  | python3 -c 'import sys,json; s=json.load(sys.stdin)["session"]; print(s["source"]); print(s.get("note",""))'
# S3 STS only: oracle_sts
# IAM only (no ORACLE_STS): oci_iam + note about validated user / mount still uses S3 AK
# Both: oracle_sts + note includes "OCI IAM API-key validated…"
```

### Common failures → `session.note`

| Situation | Typical `source` | `session.note` |
|-----------|------------------|----------------|
| Flags off | `embedded` | `Oracle OCI: set AI_CLOUDHUB_ORACLE_STS=1 … and/or AI_CLOUDHUB_ORACLE_NATIVE_IAM=1…` |
| Native IAM on, env incomplete | `embedded` or S3 result | `OCI native IAM enabled but key env incomplete: …` |
| Identity HTTP/sign error | fallback + note | `OCI IAM API-key validation failed: …; using embedded/S3-compat path` |
| IAM OK, no provider AK | `oci_iam` | `…; no S3 access_key on provider — rclone mount may need customer secret keys` |
| S3 AssumeRole fail | `embedded` | `Oracle S3-compatible STS AssumeRole failed…` |

Honest limits (see [KNOWN_LIMITATIONS.md](./KNOWN_LIMITATIONS.md)): **no** auto PAR minting, **no** automatic S3 customer secret creation — `oci_iam` is validation only.

### Security notes

- API private key stays in **process env / file**, not in provider JSON (current build). Protect the host.
- Customer secret keys + MASTER_KEY for mount material.
- Prefer short sessions; hubd/runner must refresh and wipe conf.
- Cache reduces Identity chatter; do not rely on it as authz.

---

## Ops quick reference

| Vendor | Type | Preferred native flag | RoleArn **env** keys | Happy `source` |
|--------|------|----------------------|----------------------|----------------|
| Aliyun OSS | `oss` | `AI_CLOUDHUB_OSS_NATIVE_STS=1` | `AI_CLOUDHUB_OSS_STS_ROLE_ARN` / `AI_CLOUDHUB_ALIYUN_STS_ROLE_ARN` (`acs:ram::…`) | `aliyun_sts` |
| Tencent COS | `cos` | `AI_CLOUDHUB_COS_NATIVE_STS=1` | `AI_CLOUDHUB_COS_STS_ROLE_ARN` / `AI_CLOUDHUB_TENCENT_STS_ROLE_ARN` (`qcs::cam::…`) | `tencent_sts` |
| Qiniu Kodo | `qiniu` | `AI_CLOUDHUB_QINIU_STS=1` + optional `QINIU_DOWNLOAD_TOKEN` | `AI_CLOUDHUB_QINIU_STS_ROLE_ARN` (if S3 STS gateway needs it) | `qiniu_sts` / `qiniu_download` |
| Oracle OCI | `oracle` | `AI_CLOUDHUB_ORACLE_STS=1` + optional `ORACLE_NATIVE_IAM` | `AI_CLOUDHUB_ORACLE_STS_ROLE_ARN` (S3) | `oracle_sts` / `oci_iam` |

### How to read a session (debug loop)

```bash
curl -sS -X POST "$API/v1/drives/$DID/session" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"mount_point":"/workspace","device_id":"ops-1"}' \
  | python3 -c 'import sys,json; s=json.load(sys.stdin)["session"];
print("source =", s.get("source"));
print("note   =", (s.get("note") or "")[:300]);
print("exp    =", s.get("expires_at"))'
```

| Observation | Meaning |
|-------------|---------|
| `source=aliyun_sts` / `tencent_sts` / `qiniu_sts` / `oracle_sts` and empty note | Native or S3 STS happy path |
| `source=embedded` + long note | Flags off or AssumeRole failed — **mount still works** with long-lived AK/SK |
| `source=qiniu_download` | Download-token assist (or primary when no S3 STS); not a full S3 session mint |
| `source=oci_iam` | API-key validated; mount still needs Customer Secret Key on provider |
| `presign-get` → `method=qiniu_download` | Object-level private URL (Qiniu only); independent of session STS |

Metrics: `aicloudhub_sts_source_total{source="…"}` — see [METRICS.md](./METRICS.md).

### Production checklist (all four)

1. `AI_CLOUDHUB_MASTER_KEY` set before first provider write ([PRODUCTION.md](./PRODUCTION.md)).
2. Prefer native STS where RoleArn env exists; keep long-lived keys off agent hosts.
3. Confirm `session.source` after Issue; empty success note + expected source = good.
4. hubd/runner refresh before `expires_at`; never log full `rclone_conf`.
5. Optional policy gate: [POLICY.md](./POLICY.md) (`drive.session`, `provider.write`).
6. Prometheus: `aicloudhub_sts_source_total{source="…"}`.
7. Agent consumers: put drive id in `allowed_drive_ids` ([QUICKSTART-AGENT.md](./QUICKSTART-AGENT.md)).

### Related code

- `internal/sts/aliyun_sts.go` · `tencent_sts.go` · `qiniu_token.go` · `oci_iam.go` · `s3_assume.go` · `optional_sts.go`
- Object Qiniu path: drive `ObjectPresignGet` → `method=qiniu_download`
