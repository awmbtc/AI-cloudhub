# STS 联调 Runbook（厂商 Session 来源）

> 生产纪律 / 真路径一类工作（D-003 P1）。  
> API 参考：[STS.md](./STS.md) · 厂商字段：[CLOUD-INTEGRATION.md](./CLOUD-INTEGRATION.md)  
> 自动化：`make smoke-sts`（离线）· `make smoke-sts-live`（真 MinIO，可软跳过）

## 原则（先读）

1. **Session 永不因 STS 失败而阻断** → 回退 `source=embedded` + `session.note`  
2. **控制面不中转对象 body**；STS 只换短时挂盘凭证  
3. **真云 STS 多为 best-effort**；没配 Role / 服务端无 AssumeRole 时，挂盘仍可用长效 AK/SK  

---

## 期望 `session.source` 矩阵

| Provider `type` | 打开开关 | 成功 source | 失败时 |
|-----------------|----------|-------------|--------|
| minio | `AI_CLOUDHUB_MINIO_STS=1` 或 `S3_STS=1` | `minio_sts` | `embedded` + note |
| s3 (AWS 端点) | `AI_CLOUDHUB_AWS_STS=1` + RoleArn | `aws_sts` | `embedded` |
| s3 (自定义端点) | `AI_CLOUDHUB_S3_STS=1` | `s3_sts` | `embedded` |
| oss | `OSS_NATIVE_STS=1` + `acs:ram::` RoleArn | `aliyun_sts` | 可回落 S3-compat → 再 embedded |
| cos | `COS_NATIVE_STS=1` + `qcs::cam::` RoleArn | `tencent_sts` | 同上 |
| qiniu | `QINIU_STS=1` / `QINIU_DOWNLOAD_TOKEN=1` | `qiniu_sts` / `qiniu_download` | `embedded` |
| oracle | `ORACLE_STS=1` / `ORACLE_NATIVE_IAM=1` | `oracle_sts` / `oci_iam` | `embedded` |
| r2 / b2 | `R2_STS` / `B2_STS` / `S3_STS` | `s3_sts` | `embedded` + 诚实 note |

Role ARN 等字段见 CLOUD-INTEGRATION 各厂商节。

---

## 离线回归（CI / 本机必跑）

```bash
export CGO_ENABLED=0
make smoke-sts
```

覆盖：

| Phase | 断言 |
|-------|------|
| U | `go test ./internal/sts` |
| O | 各厂商 flags **关** → `source=embedded` |
| Q | `QINIU_DOWNLOAD_TOKEN=1` → `qiniu_download` + metrics |
| F | MinIO STS **开** + 坏端点 → **fail-open** `embedded` + note 含 fail |
| L | 仅当 `SMOKE_STS_LIVE=1`：真 MinIO session（source 可为 minio_sts 或 embedded） |

---

## 真 MinIO 联调（本机）

```bash
# 自动下载/启动 MinIO binary（无需 docker）
AI_CLOUDHUB_SMOKE_STS_LIVE=1 make smoke-sts-live

# 或指向已有 MinIO
MINIO_ENDPOINT=http://127.0.0.1:9000 \
MINIO_ACCESS_KEY=minioadmin MINIO_SECRET_KEY=minioadmin \
AI_CLOUDHUB_SMOKE_STS_LIVE=1 make smoke-sts-live

# CI 硬失败：
AI_CLOUDHUB_SMOKE_STS_LIVE=1 AI_CLOUDHUB_SMOKE_STS_REQUIRE=1 make smoke-sts-live
```

**诚实预期：** 原生日建 MinIO **常常没有** 配置好的 AssumeRole，因此 `source=embedded` **算通过**。  
只有服务端 STS / Role 配好时才会看到 `minio_sts`。挂盘 conf 仍会下发，可用 rclone 测。

### 人工 curl 最小闭环

```bash
export AI_CLOUDHUB_MINIO_STS=1
# 登录后：
curl -sS -X POST "$API/v1/drives/$DID/session" -H "Authorization: Bearer $TOK" \
  -H 'Content-Type: application/json' -d '{"device_id":"dev1","mode":"mount"}' | jq '.session.source,.session.note'
```

看 metrics：

```bash
curl -sS "$API/metrics" | grep aicloudhub_sts_source_total
```

---

## 真云厂商（有客户再做）

| 厂商 | 最小前置 | 成功标志 |
|------|----------|----------|
| Aliyun OSS | RAM 用户可 `sts:AssumeRole` + RoleArn | `source=aliyun_sts` |
| Tencent COS | CAM 可 AssumeRole + RoleArn | `source=tencent_sts` |
| AWS S3 | RoleArn + 可 AssumeRole 的 AK | `source=aws_sts` |
| Qiniu | 下载：DOWNLOAD_TOKEN；挂盘：S3 STS 视网关 | note / source 见 STS.md |
| OCI | API-key env + 可选 CREATE_SECRET/PAR | `oci_iam` / note |

**不要**在没有客户凭证时硬编假成功。失败必须 fail-open。

---

## 与黄金路径的关系

| 脚本 | 测什么 |
|------|--------|
| `smoke-golden` | 控制面契约（可无活桶） |
| `smoke-golden-minio` | 活桶 **objects inventory** + job |
| `smoke-sts` / `smoke-sts-live` | **session.source 选择与 fail-open** |

上线清单见 [CUTOVER.md](./CUTOVER.md)。

---

## 版本

对齐 **0.2.56+**（Phase F fail-open + live binary auto-start）。
