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
- **Policy：** 内置 scope + drive 白名单 + path 前缀；可选外部 JSON（`AI_CLOUDHUB_POLICY_FILE`，见 [POLICY.md](./POLICY.md)）。**无** OPA/Rego。
- **Runtime jail：** runner 默认路径 jail + **env 白名单**（`AI_CLOUDHUB_JAIL`；`AI_CLOUDHUB_PASS_TOKEN=1` 才注入父 API token）。可选 **进程内 seccomp**（Linux）：`AI_CLOUDHUB_SECCOMP=1`，CGO-free；`PROFILE=default|strict|netdeny`；`SECCOMP_NET=deny` 时 **socket 仅 AF_UNIX**；`SECCOMP_STRICT=1` 加载失败中止。见 [SECCOMP.md](./SECCOMP.md)。
- **Snapshot / objects：** 元数据 + 清单（含可选 version_id）；`version-hint` / `restore-plan` / `presign-get` 辅助 BYOS；`restore-version` 仅对对象存储发 **CopyObject**（用用户凭证），控制面**不**代理对象 body。Live 硬断言见 `make smoke-minio`。
- **Network deny：** env 剥离；`runner-netns.sh` / `runner-bwrap.sh` / `runner-seccomp.sh`（Linux，可选外部包装）。进程内 seccomp 与外部包装可叠加使用。
- **STS：** MinIO/AWS S3-compat 或原生；**Aliyun RAM** / **Tencent CAM** 原生 STS（`aliyun_sts` / `tencent_sts`）；其余厂商 S3 兼容 AssumeRole。见 [STS.md](./STS.md)。
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
  | r2/b2/qiniu/oracle | S3 兼容 AssumeRole（可选） | `AI_CLOUDHUB_S3_STS=1` 或 `AI_CLOUDHUB_<VENDOR>_STS=1` | `s3_sts` |
  | 上述厂商且开关关 | 不探测 | — | embedded + Note |

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
