# STS / short-lived mount credentials

AI-cloudhub issues **short-lived mount sessions** for rclone/FUSE. Native / S3-compatible cloud STS is **best-effort** and never blocks Issue on failure.

## Sources (`session.source`)

| source | When | Env |
|--------|------|-----|
| `embedded` | Default: short-lived conf with provider keys | — |
| `refresh` | Session refresh path | — |
| `minio_sts` | MinIO AssumeRole | `AI_CLOUDHUB_MINIO_STS=1` or `AI_CLOUDHUB_S3_STS=1` |
| `aws_sts` | AWS AssumeRole | `AI_CLOUDHUB_AWS_STS=1` + RoleArn |
| `s3_sts` | S3-compatible AssumeRole | `AI_CLOUDHUB_S3_STS=1` / per-vendor |
| `aliyun_sts` | Aliyun RAM | `AI_CLOUDHUB_OSS_NATIVE_STS=1` + `acs:ram::` RoleArn |
| `tencent_sts` | Tencent CAM | `AI_CLOUDHUB_COS_NATIVE_STS=1` + `qcs::cam::` RoleArn |
| `qiniu_sts` | Qiniu S3-compat AssumeRole | `AI_CLOUDHUB_QINIU_STS=1` |
| `qiniu_download` | **Native Qiniu private download token** (HMAC URL, not S3 session) | `AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN=1` |
| `oracle_sts` | Oracle S3-compat AssumeRole | `AI_CLOUDHUB_ORACLE_STS=1` |
| `oci_iam` | **OCI API-key (RSA private key) identity validation** | `AI_CLOUDHUB_ORACLE_NATIVE_IAM=1` + OCI key env |

## Qiniu private download token

```bash
export AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN=1   # session.note sample assist
```

### Object-level signed GET (preferred)

`POST /v1/drives/{id}/objects/presign-get` for **type=qiniu** (no `version_id`) returns a native HMAC private download URL:

```json
{
  "method": "qiniu_download",
  "url": "https://cdn…/key?e=…&token=AK:…",
  "deadline": 1710000000,
  "expires_in": 900
}
```

- Uses provider AK/SK + endpoint (S3-compat host → `host/bucket/key`; CDN host → `host/key`).
- With `version_id` set → S3-compatible presign (`method=s3_presign`) instead.
- Control plane never proxies object bytes.

### Session assist

When `AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN=1`, session Issue may set `source=qiniu_download` and put a truncated sample URL in `session.note`.  
Mount/rclone still uses S3-compat credentials when available (`AI_CLOUDHUB_QINIU_STS=1`).

Helpers: `QiniuDownloadToken`, `QiniuObjectSignedGet`, `QiniuUploadToken` in `internal/sts/qiniu_token.go`.

## OCI API-key IAM (private key)

```bash
export AI_CLOUDHUB_ORACLE_NATIVE_IAM=1
export AI_CLOUDHUB_OCI_TENANCY_OCID=ocid1.tenancy...
export AI_CLOUDHUB_OCI_USER_OCID=ocid1.user...
export AI_CLOUDHUB_OCI_FINGERPRINT=aa:bb:...
export AI_CLOUDHUB_OCI_PRIVATE_KEY_PEM="-----BEGIN RSA PRIVATE KEY-----..."
# or AI_CLOUDHUB_OCI_PRIVATE_KEY_FILE=/path/to/oci_api_key.pem
export AI_CLOUDHUB_OCI_REGION=us-ashburn-1
```

Best-effort: signs `GET /20160918/users/{userId}` to **validate** API-key material (`source=oci_iam`).  
Does **not** auto-mint S3 customer secret keys or PARs in this build. Mount still prefers provider S3 AK/SK / S3-compat STS when present.

Successful Identity lookups are **cached** briefly (`AI_CLOUDHUB_OCI_IAM_CACHE_SEC`, default `300`; `0` disables) so session Issue does not call OCI on every request.

## Env matrix (summary)

| Flag | Effect |
|------|--------|
| `AI_CLOUDHUB_S3_STS=1` | Generic S3-compat AssumeRole for many types |
| `AI_CLOUDHUB_*_STS=1` | Per-vendor S3-compat |
| `AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN=1` | Native Qiniu download token note |
| `AI_CLOUDHUB_ORACLE_NATIVE_IAM=1` | OCI RSA API-key validation |
| Aliyun/Tencent NATIVE flags | Cloud RAM/CAM AssumeRole |

Truthy: `1` / `true` / `yes`.

## Behavior notes

1. **Never blocks Issue/Refresh** — failures fall back to embedded/refresh + `session.note`.
2. AWS-looking endpoints never use generic S3-compat STS.
3. Implementation: minio-go STS; pure Go Aliyun HMAC / Tencent TC3 / Qiniu HMAC / OCI RSA-SHA256.

## Metrics

```text
aicloudhub_sts_source_total{source="…|qiniu_download|oci_iam|…"}
```

## Production guidance

1. Prefer native STS where available.  
2. Set `AI_CLOUDHUB_MASTER_KEY`.  
3. Runtime must refresh before `expires_at` and destroy conf on unmount.  
