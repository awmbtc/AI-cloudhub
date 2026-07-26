# AI-cloudhub 已知限制（持续更新）

## 安全

- **密码：** **bcrypt** 存储；注册/改密最短 8 字符；旧明文密码登录成功后自动升级哈希。
- **注册：** 默认开放；生产 bootstrap 后设 `AI_CLOUDHUB_ALLOW_REGISTER=0`（仍允许零用户时创建首位 admin）。
- **登录防爆破：** 按 IP 限速 + 用户名+IP 失败锁定（可配 `AI_CLOUDHUB_AUTH_*`）；失败写入审计 `auth.login_fail`。
- **会话：** Access = 对称 HMAC（`jti` + `token_version`），默认 TTL 24h；Refresh = 不透明随机串（库内仅 SHA-256），默认 7d。
  - 登录返回 `token` + `refresh_token`；`POST /v1/auth/refresh` 轮换 refresh。
  - 单会话吊销：`POST /v1/auth/logout`（可选 body `refresh_token`）
  - 全会话吊销：改密 / `POST /v1/admin/users/{id}/revoke-sessions`
- **Agent 身份：** CRUD + token scopes；`allowed_drive_ids` 白名单（空=全部）；PUT 更新；Manifest 2.0 前缀。
  - **Devices：** agent token **一律 403**（`agent token cannot manage devices`；仅 hubd/人）。
  - **Bindings：** create/mutate 走 `allowAgentDrive`（scope + drive allowlist + policy）；**list 过滤**掉 agent 不可见的 drive。
- **Policy：** 内置 scope + drive 白名单 + path 前缀；可选外部 JSON（`AI_CLOUDHUB_POLICY_FILE`）与可选 **OPA/Rego**（`AI_CLOUDHUB_OPA_POLICY_FILE`，见 [POLICY.md](./POLICY.md)）。可选 **远程 PDP**（`AI_CLOUDHUB_PDP_URL` HTTP allow/reason；默认 fail-open；observe/strict；**不**替客户托管 PDP）。
- **Runtime jail：** runner 默认路径 jail + **env 白名单**（`AI_CLOUDHUB_JAIL`；`AI_CLOUDHUB_PASS_TOKEN=1` 才注入父 API token）。可选 **进程内 seccomp**（Linux）：`AI_CLOUDHUB_SECCOMP=1`，CGO-free；`PROFILE=default|strict|netdeny`；`SECCOMP_NET=deny` 时 **socket 仅 AF_UNIX**；`SECCOMP_STRICT=1` 加载失败中止。见 [SECCOMP.md](./SECCOMP.md)。
- **Snapshot / objects：** 元数据 + 清单（含可选 version_id）；`version-hint` / `restore-plan` / `presign-get` 辅助 BYOS；`restore-version` 仅对对象存储发 **CopyObject**（用用户凭证），控制面**不**代理对象 body。Live 硬断言见 `make smoke-minio`。
- **Network deny：** env 剥离；`runner-netns.sh` / `runner-bwrap.sh` / `runner-seccomp.sh`（Linux，可选外部包装）。进程内 seccomp 与外部包装可叠加使用。
- **STS：** MinIO/AWS S3-compat 或原生；**Aliyun RAM** / **Tencent CAM**；**Qiniu 私有下载 token**（`qiniu_download`）；**OCI API-key IAM 校验**（`oci_iam`，不自动铸造 S3 密钥）。见 [STS.md](./STS.md)。
- **429：** 带 `Retry-After: 1`（固定秒，非自适应）。
- **MCP：** v0.2 工具级 scope + 路径 jail；非完整 MCP SDK / resources。
- **Admin IP：** `AI_CLOUDHUB_ADMIN_CIDRS` 可选；空=不限制。
- **用户创建：** 公开注册可关；关后用 admin `POST /v1/admin/users` 建号。
- **Provider 密钥：** 生产请设置 `AI_CLOUDHUB_MASTER_KEY`（信封加密）；未设置时明文落库（仅开发）。
- **STS 会话：** 默认短时 conf 内嵌密钥（`source=embedded`/`refresh`）。原生 / S3 兼容 STS 为 best-effort：

  | type | 原生 STS | 开关 | Source |
  |------|----------|------|--------|
  | minio | AssumeRole（provider 端点） | `AI_CLOUDHUB_MINIO_STS=1` 或 `AI_CLOUDHUB_S3_STS=1` | `minio_sts` |
  | s3（AWS 端点） | AWS AssumeRole | `AI_CLOUDHUB_AWS_STS=1` + Role ARN | `aws_sts` |
  | s3（自定义端点） | S3 兼容 AssumeRole | `AI_CLOUDHUB_S3_STS=1` | `s3_sts` |
  | oss | **Aliyun RAM** AssumeRole | `AI_CLOUDHUB_OSS_NATIVE_STS=1` + `acs:ram::` RoleArn | `aliyun_sts` |
  | cos | **Tencent CAM** AssumeRole | `AI_CLOUDHUB_COS_NATIVE_STS=1` + `qcs::cam::` RoleArn | `tencent_sts` |
  | oss/cos 另可选 | S3 兼容 AssumeRole（原生失败可回退） | `AI_CLOUDHUB_OSS_STS` / `COS_STS` / `S3_STS` | `s3_sts` |
  | r2/b2 | S3 兼容 AssumeRole（可选） | `AI_CLOUDHUB_S3_STS=1` 或 per-vendor | `s3_sts` |
  | qiniu | S3 兼容 AssumeRole **和/或** 原生私有下载 token | `AI_CLOUDHUB_QINIU_STS=1` / `S3_STS`；`AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN=1` | `qiniu_sts` / `s3_sts` / `qiniu_download` |
  | oracle | S3 兼容 AssumeRole **和/或** OCI API-key 校验 | `AI_CLOUDHUB_ORACLE_STS=1` / `S3_STS`；`AI_CLOUDHUB_ORACLE_NATIVE_IAM=1` + OCI key env | `oracle_sts` / `s3_sts` / `oci_iam` |
  | 上述厂商且开关关 | 不探测 | — | embedded + Note |

  失败永不阻断 Issue，一律回退 embedded 短时会话。

  **诚实边界（已落地但仍有限）：**
  - **Qiniu：** `POST …/objects/presign-get` 对 type=qiniu 返回原生 HMAC 私有下载 URL（`method=qiniu_download`）；session 侧可另开 note assist。**不**替代挂盘 S3 session；versioned GET 仍走 S3 presign。
  - **OCI：** `oci_iam` 校验 API-key；Stage C 可选 `AI_CLOUDHUB_OCI_CREATE_SECRET=1` 铸造 Customer Secret、`AI_CLOUDHUB_OCI_PAR=1` 生成 ObjectRead PAR sample（需 namespace/bucket 与 IAM 权限）。失败不阻断 Issue。
  - **远程 PDP：** `AI_CLOUDHUB_PDP_URL` 可选；默认 fail-open；**不**替客户托管 PDP。
  - **D-001：** 禁止平台大规模 Runner 池（算力 BYOC）。
  - 见 [STS.md](./STS.md) · [POLICY.md](./POLICY.md) · [DECISIONS.md](./DECISIONS.md)。

## Runtime

- 依赖 **rclone**；挂盘还要 FUSE / **WinFsp** / macFUSE。
- Windows：运行 `scripts\windows\install-deps.ps1`（或 `.bat`）安装 WinFsp + rclone；详见 [WINDOWS.md](./WINDOWS.md)。
- soft refresh 只更新 conf，已打开的 FUSE 句柄仍可能持旧凭证、需 remount（hubd 不会对“打开文件”做强制踢出）。
- hubd 会检测 **rclone 进程退出** 并 `actual=error` + 自动 remount。
- hubd 另做 **mount path 可达性探测**（`ReadDir` 超时则 remount）；仍无法保证所有 FUSE 假死场景。
- soft refresh 默认只重写 conf；`AI_CLOUDHUB_FORCE_REMOUNT_ON_REFRESH=1` 时改为整挂 remount（诚实处理打开句柄）。
- Windows stop：优先 `Kill` 进程；Linux stop 额外 fusermount/umount best-effort。
- Mode 优先级：binding.mode → session.mode → manifest env → `mount`（hubd v0.2.7+）。
- `mode=sync_workspace` 可在无 FUSE / 无 WinFsp 时兜底。

## 控制面

- 默认 SQLite 单写；多副本用 `AI_CLOUDHUB_DB=postgres://...`。
- 限流默认进程内；多实例共享用 `AI_CLOUDHUB_REDIS=redis://...`。
- 基础 RBAC（admin|user）；无细粒度 per-resource ACL。
- 资源配额（默认）：binding 10 / drive 20 / provider 20 每用户；非字节级存储配额。
- Provider health 为 ListBuckets 探测，部分厂商权限不足时可能 502 但凭证仍可用于指定 bucket。
- 生产请设 `AI_CLOUDHUB_STRICT=1` + 强 `JWT_SECRET` + `AI_CLOUDHUB_MASTER_KEY` + `AI_CLOUDHUB_ALLOW_REGISTER=0`。
- `/metrics` 默认可匿名；生产设 `AI_CLOUDHUB_METRICS_TOKEN`。
- Job 为 BYOC 队列，**禁止**平台大规模 Runner 池（D-001）。
- Admin 跨用户 job 列表：`GET /v1/admin/jobs*`（人 admin only）；keyset `cursor`/`next_cursor`（created_at DESC, id DESC）；stats 为全库 `COUNT GROUP BY status`（无 500 行上限）。
- User job 列表：`GET /v1/jobs` 同样 keyset（default limit 100）；`status=pending` 为 claimable 全集（无 cursor）。过滤后分页（非 store 层 label/agent 下推）。
- Job lease：默认 `AI_CLOUDHUB_JOB_LEASE_SEC=300`；runner 周期性 `POST …/heartbeat`；过期 running 在下次 claim 时回 pending（note `released: lease expired`）。`0` 关闭回收。无法检测「进程存活但卡死且仍心跳」。
- Job hard timeout：`timeout_sec`（创建）或全局 `AI_CLOUDHUB_JOB_TIMEOUT_SEC`；从 `claimed_at` 起算，超时在 claim 路径 fail（exit 124 + note）。runner 侧 `CommandContext` 杀进程。
- Job attempts：`attempt_count` 每次 claim +1；`max_attempts>0` 时 lease 过期且次数耗尽 → fail（不再 re-queue）。
- Job priority：`priority` 更高先 claim（同优先级 FIFO）；默认 0，夹紧 ±1000。
- Job labels：创建 `labels` 字符串 map；list 用重复 query `label=key:value`（全匹配）。
- Job region claim：`X-AI-Cloudhub-Region` / body `region` / runner `AI_CLOUDHUB_REGION` 只 claim 匹配 `region_hint` 的 job。
- Job idempotency：创建 `idempotency_key`（每用户唯一）；同 key+同 payload → 200 重放；同 key+不同 payload → **409**；见 [JOBS.md](./JOBS.md)。
- Job cancel → runner：worker 轮询 GET job（`AI_CLOUDHUB_CANCEL_POLL` 默认 5s）；取消后 CommandContext kill；complete 对已 terminal 为 no-op。
- Job runner 身份：claim 可带 `X-AI-Cloudhub-Runner-Id` / body `runner_id`；runner 默认 `AI_CLOUDHUB_RUNNER_ID` 或 hostname。
- Job webhook（可选）：durable outbox `job_webhook_outbox` + worker；envelope `{event_id,event,occurred_at,job}`；HMAC optional；at-least-once（按 event_id 去重）；失败退避后 `dead`；admin list/get/retry/purge；delivered/dead 默认保留 7 天（`AI_CLOUDHUB_JOB_WEBHOOK_RETAIN_SEC`，`0` 关闭自动 purge）；pending 不自动删。
- Job 输出：complete 可带 `stdout`/`stderr`（默认各最多 8KiB 尾部，`AI_CLOUDHUB_JOB_OUTPUT_MAX`）+ `stdout_truncated`/`stderr_truncated`；非流式、非对象存储日志。

## 产品

- 非完整网盘 UI。
- 控制面不捆绑 MinIO 服务（非魔改 MinIO）。
- 2.0 主线已收口；Stage C 扩展：Lineage / Graph / Connectors / 向量记忆搜索 / 支付骨架 / 可选多副本 compose。默认仍 monorepo `api`。详见 [STAGE-C.md](./STAGE-C.md)。**仍禁止**平台大规模 Runner 池（D-001）。
