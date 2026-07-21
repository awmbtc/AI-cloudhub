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
- **Policy v0：** scope + drive 白名单 + path 前缀结构校验；尚无外部 JSON 策略文件 / OPA。
- **Runtime jail：** runner 默认路径 jail + **env 白名单**（`AI_CLOUDHUB_JAIL`；`AI_CLOUDHUB_PASS_TOKEN=1` 才注入父 API token）；非完整 seccomp/namespace。
- **Snapshot v0：** 仅 drive/manifest **元数据**快照与 restore 提示；**不**做对象存储字节级版本/回滚。
- **MCP：** v0.2 工具级 scope + 路径 jail；非完整 MCP SDK / resources。
- **Admin IP：** `AI_CLOUDHUB_ADMIN_CIDRS` 可选；空=不限制。
- **用户创建：** 公开注册可关；关后用 admin `POST /v1/admin/users` 建号。
- **Provider 密钥：** 生产请设置 `AI_CLOUDHUB_MASTER_KEY`（信封加密）；未设置时明文落库（仅开发）。
- **STS 会话：** 默认短时 conf 内嵌密钥（`source=embedded`/`refresh`）。原生 STS 为 best-effort：

  | type | 原生 STS | 开关 | Source |
  |------|----------|------|--------|
  | minio | AssumeRole | `AI_CLOUDHUB_MINIO_STS=1` | `minio_sts` |
  | s3（AWS） | AssumeRole | `AI_CLOUDHUB_AWS_STS=1` + Role ARN | `aws_sts` |
  | r2 | 不做经典 STS | — | embedded + Note |
  | b2/oss/cos/qiniu/oracle | 无统一 STS | — | embedded + Note |

  失败永不阻断 Issue，一律回退 embedded 短时会话。

## Runtime

- 依赖 **rclone**；挂盘还要 FUSE / **WinFsp** / macFUSE。
- Windows：运行 `scripts\windows\install-deps.ps1`（或 `.bat`）安装 WinFsp + rclone；详见 [WINDOWS.md](./WINDOWS.md)。
- soft refresh 只更新 conf，已打开的 FUSE 句柄仍可能需 remount。
- `mode=sync_workspace` 可在无 FUSE 时兜底。

## 控制面

- 默认 SQLite 单写；多副本用 `AI_CLOUDHUB_DB=postgres://...`。
- 限流默认进程内；多实例共享用 `AI_CLOUDHUB_REDIS=redis://...`。
- 基础 RBAC（admin|user）；无细粒度 per-resource ACL。
- 资源配额（默认）：binding 10 / drive 20 / provider 20 每用户；非字节级存储配额。
- Provider health 为 ListBuckets 探测，部分厂商权限不足时可能 502 但凭证仍可用于指定 bucket。
- 生产请设 `AI_CLOUDHUB_STRICT=1` + 强 `JWT_SECRET` + `AI_CLOUDHUB_MASTER_KEY` + `AI_CLOUDHUB_ALLOW_REGISTER=0`。
- `/metrics` 默认可匿名；生产设 `AI_CLOUDHUB_METRICS_TOKEN`。
- Job 为 BYOC 队列，**禁止**平台大规模 Runner 池（D-001）。

## 产品

- 非完整网盘 UI。
- 控制面不捆绑 MinIO 服务（非魔改 MinIO）。
