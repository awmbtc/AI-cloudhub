# STS / short-lived mount credentials

AI-cloudhub issues **short-lived mount sessions** for rclone/FUSE. Native cloud STS is **best-effort** and never blocks Issue on failure.

## Sources (`session.source`)

| source | When | Env |
|--------|------|-----|
| `embedded` | Default: short-lived conf with provider keys (encrypted at rest if master key set) | — |
| `refresh` | Session refresh path | — |
| `minio_sts` | MinIO AssumeRole succeeded | `AI_CLOUDHUB_MINIO_STS=1` |
| `aws_sts` | AWS AssumeRole succeeded | `AI_CLOUDHUB_AWS_STS=1` + role ARN env |

Vendors without wired STS (R2, B2, OSS, COS, Qiniu, Oracle) always use embedded/refresh and set `session.note` explaining why.

## Metrics

`/metrics` exposes:

```text
aicloudhub_sessions_issued_total
aicloudhub_sts_source_total{source="embedded|refresh|minio_sts|aws_sts"}
```

## Production guidance

1. Prefer native STS where available (`minio_sts` / `aws_sts`).  
2. Always set `AI_CLOUDHUB_MASTER_KEY` so static keys are not plaintext at rest.  
3. Runtime must refresh before `expires_at` and destroy conf on unmount.  
