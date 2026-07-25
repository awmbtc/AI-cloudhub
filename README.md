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
| [docs/QUICKSTART-AGENT.md](docs/QUICKSTART-AGENT.md) | **Agent 30 分钟**：token + MCP + hubd 挂载提示 |
| [docs/STS.md](docs/STS.md) | 多厂商 STS / Qiniu 下载 token / OCI IAM |
| [docs/CLOUD-INTEGRATION.md](docs/CLOUD-INTEGRATION.md) | **多云接入 runbook**（OSS/COS/Qiniu/OCI：字段、env、curl、session.note） |
| [docs/POLICY.md](docs/POLICY.md) | Policy JSON + 可选 OPA/Rego |
| [docs/PRODUCTION.md](docs/PRODUCTION.md) | **生产 checklist**（STRICT / 密钥 / Compose / TLS） |
| [docs/METRICS.md](docs/METRICS.md) | Prometheus `/metrics` 说明 · scrape · Grafana 查询 |
| [docs/MEMORY.md](docs/MEMORY.md) · [MARKETPLACE.md](docs/MARKETPLACE.md) · [MODULES.md](docs/MODULES.md) · [STAGE-C.md](docs/STAGE-C.md) | Stage C：Memory / 市场 / 模块 / Lineage·Graph·Connectors |
| [docs/PAYMENTS.md](docs/PAYMENTS.md) · [CONNECTORS.md](docs/CONNECTORS.md) | Stripe webhook 骨架 · Git/DB/SaaS 连接器 + runner |
| [deploy/grafana/](deploy/grafana/) | 示例 dashboard + `prometheus.yml.example` |
| [deploy/nginx.conf.example](deploy/nginx.conf.example) · [Caddyfile.example](deploy/Caddyfile.example) | 边缘 TLS 反代示例 |
| Release | `git tag vX.Y.Z && git push --tags` → 多架构二进制；本地 `make release-binaries` |
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
| **限制** | [docs/KNOWN_LIMITATIONS.md](docs/KNOWN_LIMITATIONS.md) | v0.2 |
| **发布** | `v0.2.7` 多架构二进制；hubd mode + mount liveness | ✅ |

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
# 可选生产栈（需先 export JWT_SECRET / AI_CLOUDHUB_MASTER_KEY，见 docs/PRODUCTION.md）：
# docker compose -f deploy/docker-compose.prod.yml up -d --build
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

`cmd/mcp` 是 **MCP-compatible-ish** 的 stdio 工具服务（stdlib only），供 Agent 主机接入。Agent token 按 tool 声明的 scope 门禁。

| Tool | Scope | 作用 |
|------|--------|------|
| `whoami` / `list_drives` / `list_providers` | me / drive / provider | 身份与资源列表 |
| `list_objects` / `object_presign_get` / snapshot | drive.* | 清单；Qiniu 原生 / S3 预签名 GET |
| `list_jobs` / `create_job` / `claim_next_job` / `complete_job` / `cancel_job` | job.run | BYOC 任务（用户 runner，D-001） |
| `ensure_mounted_hint` / `workspace_env` / `resolve_path` | drive / local | 挂载提示与路径 jail |

详情：[docs/MCP.md](docs/MCP.md) · 现场：`make smoke-mcp`

## 主要 API

完整 OpenAPI 契约：[docs/openapi.yaml](docs/openapi.yaml)（BYOC / 无平台 Runner 池已写在 description）。

| Method | Path | 说明 |
|--------|------|------|
| GET | `/healthz` | 健康 + 能力清单 |
| GET | `/readyz` | 存储就绪 |
| GET | `/metrics` | Prometheus 指标 |
| GET | `/v1/runtime/check` | 本机 rclone/FUSE 预检 |
| GET | `/v1/providers/catalog` | 厂商目录 |
| POST | `/v1/auth/register` `login` | 账号（首用户 admin） |
| GET | `/v1/me` | 当前用户与角色 |
| POST | `/v1/me/password` | 改密 `{old_password,new_password}` |
| GET | `/v1/admin/users` | 用户列表（admin） |
| POST | `/v1/admin/users/{id}/role` | 设角色（admin） |
| GET | `/v1/admin/audit` | 审计（admin） |
| GET | `/v1/admin/policy` | 外部策略文件状态（admin；`docs/POLICY.md`） |
| CRUD | `/v1/providers` | 绑定 Key（A+B+C） |
| CRUD | `/v1/drives` | 逻辑盘 |
| POST | `/v1/drives/{id}/session` | STS + Manifest |
| POST | `/v1/sessions/refresh` | 续期 STS（token 轮换） |
| POST | `/v1/drives/{id}/barrier` | write barrier |
| GET | `/v1/drives/{id}/objects` | 对象清单（元数据 only；不代下） |
| POST | `…/objects/presign-get` `restore-plan` `restore-version` | 预签名 / 恢复指引 / 版本恢复 |
| GET/POST | `/v1/drives/{id}/snapshots` | 元数据快照（≤50/盘；可选 inventory） |
| GET | `…/snapshots/diff` · POST `…/{sid}/restore` | 快照 diff / 恢复 drive 元数据 |
| CRUD | `/v1/bindings` | desired 挂载 |
| POST | `/v1/bindings/{id}/session` | hubd 拉会话 |
| POST | `/v1/bindings/{id}/report` | actual 上报 |
| CRUD | `/v1/agents` | Agent 身份、scope、path jail |
| GET/POST | `/v1/jobs` | BYOC 任务队列（用户 runner，D-001） |
| POST | `/v1/jobs/next/claim` `…/{id}/complete` `cancel` | 领取 / 完成 / 取消 |

Agent 侧亦可通过 **MCP helper**（`cmd/mcp`，stdio）调用同等能力（list/create job、objects、snapshots 等），见上文 MCP 节与 [docs/MCP.md](docs/MCP.md)。

现场回归（需本地 `api`）：

```bash
make smoke-agent   # Agent + snapshot
make smoke-job     # Job 持久化 / claim / complete
make smoke-mcp     # MCP tools ↔ jobs
make smoke-policy  # 外部 policy + admin/policy
make smoke-minio   # 真 MinIO inventory + include_objects
# make smoke-objects  make smoke-all
```

可选原生 / S3 兼容 STS（best-effort，失败一律回退 embedded 短时会话）：

- `AI_CLOUDHUB_MINIO_STS=1` 或 `AI_CLOUDHUB_S3_STS=1`：`type=minio` → AssumeRole（`source=minio_sts`）
- `AI_CLOUDHUB_AWS_STS=1`：`type=s3` 且 endpoint 像 AWS 时 → AWS AssumeRole（需 RoleArn；`source=aws_sts`）
- `AI_CLOUDHUB_OSS_NATIVE_STS=1`：阿里云 RAM STS（`source=aliyun_sts`，RoleArn `acs:ram::…`）
- `AI_CLOUDHUB_COS_NATIVE_STS=1`：腾讯云 CAM STS（`source=tencent_sts`，RoleArn `qcs::cam::…`）
- `AI_CLOUDHUB_S3_STS=1` 或 per-vendor：S3 兼容 AssumeRole（`source=s3_sts`）
- `AI_CLOUDHUB_QINIU_DOWNLOAD_TOKEN=1`：session note assist；对象级：`presign-get` 对 type=qiniu → `qiniu_download`
- `AI_CLOUDHUB_ORACLE_NATIVE_IAM=1` + OCI 私钥 env：API-key 校验（`oci_iam`）
- `AI_CLOUDHUB_OPA_POLICY_FILE`：可选 Rego（[POLICY.md](docs/POLICY.md)）
- 详见 [docs/STS.md](docs/STS.md) · [docs/PRODUCTION.md](docs/PRODUCTION.md) · seccomp [docs/SECCOMP.md](docs/SECCOMP.md)

## 目录

```text
cmd/api|hubd|runner|mcp
internal/{auth,provider,drive,sts,manifest,mountlib,store,crypto,policy}
protocols/workspace-manifest.schema.json
docs/  (含 MCP.md)
```

## License

Apache-2.0。不发行 MinIO 修改版；用户对象存储各自条款。
