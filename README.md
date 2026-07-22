# AI-cloudhub

**人和 Agent 的多云磁盘操作系统（Go 100% 自研）。**  
不是魔改 MinIO，不是网盘。用户 BYOS；API Key → 逻辑盘 → Runtime 自动挂载 → 路径无感读写。

```text
用户 API Key → Drive + Binding(desired)
       → hubd（本机）/ runner（云端 BYOC）
       → STS 会话 + Manifest
       → rclone mount + 写缓存
       → 用户 R2 / S3 / OSS / COS / B2 / MinIO …
```

## 文档

| 文档 | 内容 |
|------|------|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | 架构定稿 |
| [docs/DECISIONS.md](docs/DECISIONS.md) | 决策记录（含 Runner 池黑名单） |
| [docs/openapi.yaml](docs/openapi.yaml) | **HTTP OpenAPI**（auth / providers / drives / bindings / sessions / jobs / admin / healthz / readyz / metrics / runtime） |
| [docs/RISK-COST.md](docs/RISK-COST.md) | 风险成本 |
| [docs/VENDORS.md](docs/VENDORS.md) | 厂商 A/B/C |
| [docs/BUDGET-WOOL.md](docs/BUDGET-WOOL.md) | 穷部署 |
| [docs/PROGRESS.md](docs/PROGRESS.md) | **实现进度对照表** |
| [docs/MCP.md](docs/MCP.md) | MCP-compatible-ish agent tool helper |
| [docs/WINDOWS.md](docs/WINDOWS.md) | Windows 安装 WinFsp/rclone 与 hubd |
| [protocols/workspace-manifest.schema.json](protocols/workspace-manifest.schema.json) | Agent Manifest schema |

## 进度（相对架构定稿）

| 阶段 | 内容 | 状态 |
|------|------|------|
| **P0** | STS、Manifest、Binding、hubd、runner、mountlib | ✅ |
| **P1** | SQLite 持久化、Key 信封加密、限流、write barrier | ✅ 基础完成 |
| **厂商 A** | s3 / r2 / minio | ✅ |
| **厂商 B** | b2 / oss / cos | ✅ |
| **P2** | region、sync_workspace、runtime check、session refresh、jobs BYOC、MinIO STS | ✅ |
| **P3** | Job 持久化+worker、MCP、metrics | ✅ |
| **厂商 C** | qiniu、oracle | ✅ |
| **黑名单** | 自建大规模 Runner 池 | 禁止（D-001） |
| **限制** | [docs/KNOWN_LIMITATIONS.md](docs/KNOWN_LIMITATIONS.md) | v0.1 |

## 快速开始

```bash
cd /Users/dushuaihang/AI-cloudhub
export CGO_ENABLED=0
go build -o .bin/api ./cmd/api
go build -o .bin/hubd ./cmd/hubd
go build -o .bin/runner ./cmd/runner
go build -o .bin/mcp ./cmd/mcp

# 可选：加密 Provider 密钥
export AI_CLOUDHUB_MASTER_KEY="$(openssl rand -base64 32)"
# 存储：默认 SQLite；内存 memory；多副本 Postgres：
# export AI_CLOUDHUB_DB='postgres://aihub:aihub@localhost:5432/aihub?sslmode=disable'
# 多实例限流：
# export AI_CLOUDHUB_REDIS='redis://localhost:6379/0'

./.bin/api
./scripts/smoke-p0.sh
# 可选生产栈：docker compose -f deploy/docker-compose.prod.yml up -d
```

### Runtime

```bash
# 本机自动挂载（需 rclone）
AI_CLOUDHUB_API=http://127.0.0.1:8080 \
AI_CLOUDHUB_TOKEN=<token> \
AI_CLOUDHUB_DEVICE_ID=laptop-1 \
./.bin/hubd

# 云端 = 用户自己的机器（BYOC，禁止平台大池）
AI_CLOUDHUB_API=... AI_CLOUDHUB_TOKEN=... AI_CLOUDHUB_DRIVE_ID=... \
./.bin/runner -- your-agent

# Worker：轮询领取 durable jobs（仍在用户机器上跑）
AI_CLOUDHUB_WORKER=1 AI_CLOUDHUB_API=... AI_CLOUDHUB_TOKEN=... \
./.bin/runner

# Agent MCP helper（stdio JSON-RPC；见 docs/MCP.md）
AI_CLOUDHUB_API=... AI_CLOUDHUB_TOKEN=... ./.bin/mcp
```

### Windows

Windows 挂载需要 **rclone**（硬依赖）+ **WinFsp**（mount 模式）。一键安装：

```bat
scripts\windows\install-deps.bat
```

或：

```powershell
powershell -ExecutionPolicy Bypass -File scripts\windows\install-deps.ps1
```

- 优先 `winget`（`WinFsp.WinFsp` / `Rclone.Rclone`），否则直接下载  
- 缺 rclone：`hubd` 退出并提示安装脚本  
- 缺 WinFsp：仅警告，仍可 `mode=sync_workspace`  

完整说明：[docs/WINDOWS.md](docs/WINDOWS.md)

### MCP helper（agents）

`cmd/mcp` 是 **MCP-compatible-ish** 的 stdio 工具服务（stdlib only），供 Agent 主机接入：

| Tool | 作用 |
|------|------|
| `list_drives` | `GET /v1/drives` |
| `ensure_mounted_hint` | 挂载说明；可选 `drive_id`/`binding_id` 探测 session |
| `workspace_env` | Manifest 约定的 `AI_CLOUDHUB_*` 环境变量名 |

详情：[docs/MCP.md](docs/MCP.md)。

## 主要 API

完整 OpenAPI 契约：[docs/openapi.yaml](docs/openapi.yaml)（BYOC / 无平台 Runner 池已写在 description）。

| Method | Path | 说明 |
|--------|------|------|
| GET | `/healthz` | 健康 + 能力清单 |
| GET | `/v1/providers/catalog` | 厂商目录 |
| POST | `/v1/auth/register` `login` | 账号（首用户 admin） |
| GET | `/v1/me` | 当前用户与角色 |
| POST | `/v1/me/password` | 改密 `{old_password,new_password}` |
| GET | `/v1/admin/users` | 用户列表（admin） |
| POST | `/v1/admin/users/{id}/role` | 设角色（admin） |
| GET | `/v1/admin/audit` | 审计（admin） |
| GET | `/readyz` | 存储就绪 |
| CRUD | `/v1/providers` | 绑定 Key（A+B 批） |
| CRUD | `/v1/drives` | 逻辑盘 |
| POST | `/v1/drives/{id}/session` | STS + Manifest |
| POST | `/v1/sessions/refresh` | 续期 STS（token 轮换） |
| POST | `/v1/drives/{id}/barrier` | write barrier |
| CRUD | `/v1/bindings` | desired 挂载 |
| POST | `/v1/bindings/{id}/session` | hubd 拉会话 |
| POST | `/v1/bindings/{id}/report` | actual 上报 |

可选原生 / S3 兼容 STS（best-effort，失败一律回退 embedded 短时会话）：

- `AI_CLOUDHUB_MINIO_STS=1` 或 `AI_CLOUDHUB_S3_STS=1`：`type=minio` → AssumeRole（`source=minio_sts`）
- `AI_CLOUDHUB_AWS_STS=1`：`type=s3` 且 endpoint 像 AWS 时 → AWS AssumeRole（需 RoleArn；`source=aws_sts`）
- `AI_CLOUDHUB_OSS_NATIVE_STS=1`：阿里云 RAM STS（`source=aliyun_sts`，RoleArn `acs:ram::…`）
- `AI_CLOUDHUB_COS_NATIVE_STS=1`：腾讯云 CAM STS（`source=tencent_sts`，RoleArn `qcs::cam::…`）
- `AI_CLOUDHUB_S3_STS=1` 或 per-vendor：S3 兼容 AssumeRole（`source=s3_sts`）
- 详见 [docs/STS.md](docs/STS.md) · seccomp 见 [docs/SECCOMP.md](docs/SECCOMP.md)

## 目录

```text
cmd/api|hubd|runner|mcp
internal/{auth,provider,drive,sts,manifest,mountlib,store,crypto,policy}
protocols/workspace-manifest.schema.json
docs/  (含 MCP.md)
```

## License

Apache-2.0。不发行 MinIO 修改版；用户对象存储各自条款。
