# STS / short-lived mount credentials

AI-cloudhub issues **short-lived mount sessions** for rclone/FUSE. Native / S3-compatible cloud STS is **best-effort** and never blocks Issue on failure.

## Sources (`session.source`)

| source | When | Env |
|--------|------|-----|
| `embedded` | Default: short-lived conf with provider keys (encrypted at rest if master key set) | — |
| `refresh` | Session refresh path | — |
| `minio_sts` | MinIO AssumeRole succeeded | `AI_CLOUDHUB_MINIO_STS=1` **or** `AI_CLOUDHUB_S3_STS=1` |
| `aws_sts` | AWS AssumeRole succeeded | `AI_CLOUDHUB_AWS_STS=1` + `AI_CLOUDHUB_AWS_STS_ROLE_ARN` (AWS-looking `type=s3` only) |
| `s3_sts` | S3-compatible AssumeRole succeeded (non-MinIO / non-AWS) | `AI_CLOUDHUB_S3_STS=1` and/or per-vendor flags |
| `aliyun_sts` | Aliyun RAM STS AssumeRole succeeded | `AI_CLOUDHUB_OSS_NATIVE_STS=1` (or `AI_CLOUDHUB_ALIYUN_STS=1`) + RoleArn `acs:ram::…` |
| `tencent_sts` | Tencent CAM STS AssumeRole succeeded | `AI_CLOUDHUB_COS_NATIVE_STS=1` (or `AI_CLOUDHUB_TENCENT_STS=1`) + RoleArn `qcs::cam::…` |

## Env matrix

| Flag | Effect |
|------|--------|
| `AI_CLOUDHUB_S3_STS=1` | Try S3-compat AssumeRole (minio-go `STSAssumeRole` against provider endpoint) for: `minio`, `b2`, `oss`, `cos`, `qiniu`, `oracle`, `r2`, and **non-AWS** `type=s3` custom endpoints |
| `AI_CLOUDHUB_MINIO_STS=1` | Same for `type=minio` only → `source=minio_sts` |
| `AI_CLOUDHUB_AWS_STS=1` | AWS STS for AWS-looking `type=s3` → `source=aws_sts` (requires role ARN) |
| `AI_CLOUDHUB_OSS_NATIVE_STS=1` / `AI_CLOUDHUB_ALIYUN_STS=1` | Aliyun **RAM** STS (not S3 endpoint) → `source=aliyun_sts` |
| `AI_CLOUDHUB_COS_NATIVE_STS=1` / `AI_CLOUDHUB_TENCENT_STS=1` | Tencent **CAM** STS → `source=tencent_sts` |
| `AI_CLOUDHUB_B2_STS` / `OSS` / `COS` / `QINIU` / `ORACLE` / `R2` `_STS=1` | Per-vendor S3-compat enable (truthy: `1` / `true` / `yes`) |

Truthy values: `1`, `true`, `yes` (case-insensitive).

### Role ARN

| Env | Used for |
|-----|----------|
| `AI_CLOUDHUB_AWS_STS_ROLE_ARN` | AWS AssumeRole (**required**) |
| `AI_CLOUDHUB_ALIYUN_STS_ROLE_ARN` / `AI_CLOUDHUB_OSS_STS_ROLE_ARN` | Aliyun RAM (`acs:ram::…`, **required** for native) |
| `AI_CLOUDHUB_TENCENT_STS_ROLE_ARN` / `AI_CLOUDHUB_COS_STS_ROLE_ARN` | Tencent CAM (`qcs::cam::…`, **required** for native) |
| `AI_CLOUDHUB_MINIO_STS_ROLE_ARN` | MinIO (optional; preferred over generic) |
| `AI_CLOUDHUB_S3_STS_ROLE_ARN` | Generic fallback for S3-compat / native ARN fallback |
| `AI_CLOUDHUB_B2_STS_ROLE_ARN` (and `QINIU`/`ORACLE`/`R2`) | Vendor-specific RoleArn for S3-compat |

Optional endpoints (tests / regional):

- `AI_CLOUDHUB_AWS_STS_ENDPOINT`, `AI_CLOUDHUB_AWS_STS_EXTERNAL_ID`
- `AI_CLOUDHUB_ALIYUN_STS_ENDPOINT` (default `https://sts.aliyuncs.com`)
- `AI_CLOUDHUB_TENCENT_STS_ENDPOINT`, `AI_CLOUDHUB_TENCENT_STS_REGION`

## Behavior notes

1. **Never blocks Issue/Refresh** — any STS error falls back to embedded/refresh credentials and sets `session.note`.
2. **AWS endpoints** (`looksLikeAWS`) never use the generic S3-compat path; only AWS STS when `AI_CLOUDHUB_AWS_STS=1`.
3. **OSS/COS**: native cloud STS is preferred when NATIVE flag is on or RoleArn looks vendor-native; on failure, falls back to S3-compat if that flag is also on.
4. **Flags off** for R2/B2/OSS/COS/Qiniu/Oracle: no STS probe; `session.note` explains how to enable.
5. Implementation: minio-go for S3-compat; pure Go HMAC for Aliyun RPC + Tencent TC3; AWS STS via minio-go against STS host.

## Metrics

`/metrics` exposes:

```text
aicloudhub_sessions_issued_total
aicloudhub_sts_source_total{source="embedded|refresh|minio_sts|aws_sts|s3_sts|aliyun_sts|tencent_sts"}
```

## Production guidance

1. Prefer native STS where available (`aliyun_sts` / `tencent_sts` / `aws_sts` / `minio_sts` / `s3_sts`).  
2. Always set `AI_CLOUDHUB_MASTER_KEY` so static keys are not plaintext at rest.  
3. Runtime must refresh before `expires_at` and destroy conf on unmount.  
