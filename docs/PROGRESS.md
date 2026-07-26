# 实现进度（对照架构定稿）

## 总览：v0.2.13 已发布；labels / region claim / webhook event_id

| 阶段 | 状态 |
|------|------|
| P0 无感闭环 | ✅ |
| P1 持久化/加密/限流 | ✅ |
| 厂商 A/B/C | ✅ s3 r2 minio b2 oss cos qiniu oracle |
| P2 | ✅ 大部（真云 STS 仅 MinIO 可选） |
| P3 | ✅ 骨架（jobs 持久化+worker、mcp、metrics） |
| D-001 大池黑名单 | ✅ |

## 二进制

```text
.bin/api .bin/hubd .bin/runner .bin/mcp
```

## 验证

```bash
export CGO_ENABLED=0
go test ./...
go build -o .bin/api ./cmd/api
go build -o .bin/hubd ./cmd/hubd
go build -o .bin/runner ./cmd/runner
go build -o .bin/mcp ./cmd/mcp
./scripts/smoke-p0.sh
./scripts/smoke-p1.sh
./scripts/smoke-job.sh
curl -s localhost:8080/metrics
curl -s localhost:8080/v1/runtime/check
```

## 已知限制

见 [KNOWN_LIMITATIONS.md](./KNOWN_LIMITATIONS.md)

## 三项补强（已完成）

- [x] 密码 bcrypt + 旧明文登录升级
- [x] 多厂商 STS best-effort（MinIO/AWS 开关；R2/国内等 embedded+Note）
- [x] Windows 安装：`scripts/windows/install-deps.ps1` + [WINDOWS.md](./WINDOWS.md)

## 本轮增强

- [x] PostgreSQL store（`AI_CLOUDHUB_DB=postgres://...`）
- [x] Redis 共享限流（`AI_CLOUDHUB_REDIS=...`）
- [x] `deploy/docker-compose.prod.yml`（api+pg+redis，无 runner 大池）
- [x] 基础 RBAC：`role=admin|user`，首用户 admin，`GET /v1/me`，`POST /v1/admin/users/{id}/role`
- [x] `GET /readyz` store Ping
- [x] PG 集成测试（`-tags=integration` + `AI_CLOUDHUB_TEST_PG`）
- [x] `GET /v1/admin/users` 列表
- [x] `POST /v1/me/password` 改密
- [x] 审计日志 `GET /v1/admin/audit`（login/register/role/password/provider/drive/binding）
- [x] OpenAPI `docs/openapi.yaml`
- [x] 优雅关闭 + CORS + X-Request-ID
- [x] Makefile + Dockerfile.all
- [x] Job ClaimNext 并发安全 + region 过滤

## 加固项（第一波）

- [x] Provider 健康探测：`GET|POST /v1/providers/{id}/health`（ListBuckets，超时 8s）
- [x] Drive 配额：默认每用户 20 个 drive map（超限 409）
- [x] Provider 配额：默认每用户 20 个 provider（超限 409）
- [x] Binding 配额：默认每用户 10（已有）
- [x] 审计过滤：`GET /v1/admin/audit?user_id=&limit=`
- [x] 配置校验：`JWT_SECRET` 最短 16 / 禁默认值（`AI_CLOUDHUB_STRICT=1` 硬失败）+ master key 提示

## 加固项（第二波）

- [x] 密码策略：≥8 字符；用户名 3–64 `[a-zA-Z0-9._-]`
- [x] Token TTL：`AI_CLOUDHUB_TOKEN_TTL_HOURS`（默认 24）
- [x] 注册开关：`AI_CLOUDHUB_ALLOW_REGISTER`（关后仅允许首用户 bootstrap）
- [x] 登录防爆破：IP 速率限制 + 连续失败锁定 + `auth.login_fail` 审计
- [x] 末位 admin 不可降级
- [x] 安全响应头 + body 大小限制
- [x] `/metrics` 可选 token：`AI_CLOUDHUB_METRICS_TOKEN`

## 加固项（第三波）

- [x] Token `jti` + `token_version`；`POST /v1/auth/logout` 吊销当前会话
- [x] 改密自动 bump version（全部会话失效）
- [x] Admin：`POST /v1/admin/users/{id}/revoke-sessions`
- [x] 审计过滤：`GET /v1/admin/audit?user_id=&action=&limit=`
- [x] store：`revoked_jtis` 表 + users.token_version（sqlite/pg/memory）

## 加固项（第四波）

- [x] Refresh 双令牌：login 返回 `token` + `refresh_token`；`POST /v1/auth/refresh` 轮换
- [x] refresh 仅存 SHA-256；改密 / revoke-sessions 吊销全部 refresh
- [x] Admin 建用户：`POST /v1/admin/users`（username/password/role）
- [x] 可选 HSTS：`AI_CLOUDHUB_HSTS=1`
- [x] JSON `Content-Type` 校验（非 json 的 POST → 415）

## 阶段 A · ROADMAP-2.0

施工图：[ROADMAP-2.0.md](./ROADMAP-2.0.md) · 决策 D-002

- [x] 正式路线图 + ARCHITECTURE/DECISIONS 对齐
- [x] Agent CRUD：`/v1/agents` + store（memory/sqlite/pg）
- [x] Agent Token：`POST /v1/agents/{id}/token`（`aid` + `scopes`）
- [x] Scope 校验：agent token 写 drive/provider/job 需对应 scope；人 token 不受限
- [x] Admin API 拒绝 agent token
- [x] `internal/sandbox` 路径 jail + runner 默认启用（`AI_CLOUDHUB_JAIL=0` 关闭）

## 阶段 B · 2.0 最小企业可用

- [x] B1：`allowed_drive_ids`；PUT agent；list/session 按白名单过滤
- [x] B2：Policy Engine v0（`internal/policy` Evaluate）
- [x] B4：Manifest 2.0 `permissions.read/write` + `agent_id` env
- [x] B5：`audit_events.agent_id` + admin 查询 `?agent_id=`
- [x] B3：Sandbox v1 env 白名单（runner 默认过滤；`AI_CLOUDHUB_PASS_TOKEN=1` 才传父 token）
- [x] B6：Snapshot v0（元数据快照 CRUD + restore 返回 payload）

## 本波（MCP + Admin IP）

- [x] MCP v0.2：工具 `required_scopes_any` + whoami/resolve_path/snapshots
- [x] MCP 路径 jail（mount_point / resolve_path）
- [x] Admin IP allowlist：`AI_CLOUDHUB_ADMIN_CIDRS`

## 本波续（restore apply / network / smoke）

- [x] Snapshot restore `apply=true` 回写 name/prefix/mount_point/region
- [x] 快照配额：每 drive 最多 50
- [x] Runner `AI_CLOUDHUB_NETWORK=deny` 剥离 proxy env
- [x] `scripts/smoke-agent.sh`

## 本波（可选增强）

- [x] Snapshot 对象清单：`include_objects=true` ListObjects 元数据
- [x] Snapshot diff：`GET .../snapshots/diff?a=&b=`
- [x] STS metrics：`aicloudhub_sts_source_total`
- [x] Linux netns 可选：`scripts/runner-netns.sh` + `AI_CLOUDHUB_NETWORK=deny`
- [x] docs/STS.md

## 本波（可选续）

- [x] 实时对象清单：`GET /v1/drives/{id}/objects?versions=1`
- [x] version-hint
- [x] 429 Retry-After
- [x] runner-bwrap.sh + MCP list_objects

## 本波（可选续）

- [x] 实时对象清单：`GET /v1/drives/{id}/objects?versions=1`
- [x] version-hint：`.../objects/version-hint`
- [x] 429 `Retry-After`
- [x] `scripts/runner-bwrap.sh` + MCP `list_objects`

## 本波（可选 · version restore + seccomp 骨架）

- [x] `POST .../objects/presign-get`（可选 versionId；client↔store 直下）
- [x] `POST .../objects/restore-plan`（CLI + presign + api_restore 路径）
- [x] `POST .../objects/restore-version`（S3 CopyObject，drive.write；无 body 代理）
- [x] MCP：`object_presign_get` / `object_restore_plan` / `object_restore_version`
- [x] `scripts/seccomp/runner-default.json` + `scripts/runner-seccomp.sh`

## 本波（OpenAPI + smoke objects）

- [x] OpenAPI：`/v1/drives/{id}/objects*`（list / version-hint / presign-get / restore-plan / restore-version）
- [x] `scripts/smoke-objects.sh` + `make smoke-objects`（可选 `AI_CLOUDHUB_SMOKE_MINIO=1`）

## 本波（Snapshot OpenAPI）

- [x] OpenAPI：`/v1/drives/{id}/snapshots` list/create、`diff`、`{sid}` get/delete、`{sid}/restore`
- [x] smoke-agent：list/get/preview/apply/diff/delete 覆盖

## 本波（seccomp 内嵌 + 多厂商 STS + live MinIO）

- [x] CGO-free 进程内 seccomp：`internal/sandbox` + `AI_CLOUDHUB_SECCOMP=1`（elastic/go-seccomp-bpf；非 Linux no-op）
- [x] 多厂商 S3 兼容 AssumeRole：`AI_CLOUDHUB_S3_STS` + per-vendor；`source=s3_sts`
- [x] Live MinIO 硬断言 inventory：`make smoke-minio`（auto-start MinIO；include_objects + diff）

## 本波（原生 STS + seccomp profile）

- [x] Aliyun RAM STS：`source=aliyun_sts`（`AI_CLOUDHUB_OSS_NATIVE_STS` + RoleArn）
- [x] Tencent CAM STS：`source=tencent_sts`（`AI_CLOUDHUB_COS_NATIVE_STS` + RoleArn）
- [x] seccomp `default` / `strict` 档 + `docs/SECCOMP.md`

## 本波（netdeny + Qiniu/Oracle STS 端点）

- [x] seccomp `netdeny` / `AI_CLOUDHUB_SECCOMP_NET=deny`：socket 仅 AF_UNIX
- [x] Qiniu/Oracle：`qiniu_sts` / `oracle_sts` + 独立 `*_STS_ENDPOINT` 覆盖
- [x] 通用 `AI_CLOUDHUB_S3_STS_ENDPOINT` 分离 STS 与数据端点

## 本波（Policy 外部 JSON）

- [x] `AI_CLOUDHUB_POLICY_FILE` + reload；`protocols/policy.example.json`
- [x] 规则：deny/allow、path_deny、drive/agent、observe 模式
- [x] 接入 `allowAgentDrive` + `GET /v1/admin/policy`；docs/POLICY.md

## 本波（Policy jobs + smoke + OpenAPI）

- [x] Job create/claim/complete/cancel：scope `job.run` + `CheckAccess(ActionJobRun)`
- [x] `scripts/smoke-policy.sh` / `make smoke-policy`
- [x] OpenAPI `GET /v1/admin/policy`

## 本波（Claim release）

- [x] `ReleaseToPending` + `ClaimNextFiltered`：policy/drive 拒绝后回 pending
- [x] claim by id 拒绝时同样释放

## 本波（Job agent_id）

- [x] `agent_id`（创建者）+ `claimed_by_agent_id`（领取者）；sqlite/pg soft migrate
- [x] claim 路径写入 claimer；release 清空 claimer
- [x] OpenAPI Job schema；ROADMAP M-B 勾选同步

## 本波（Job 审计 + smoke-job agent 追溯）

- [x] `job.claim` / `job.complete` / `job.cancel` 审计（带 agent_id）
- [x] `make smoke-job`：restart 耐久 + creator/claimer agent 字段
- [x] runner jobDTO 含 agent 字段

## 本波（Provider policy + job list filter）

- [x] Provider GET/health 强制 `provider.read`；写/删走 policy；`provider.write` ⇒ read
- [x] `GET /v1/jobs?agent_id=&claimed_by_agent_id=`
- [x] smoke-agent provider 403；smoke-job list filter

## 本波（MCP jobs tools）

- [x] MCP：`list_jobs` / `create_job` / `claim_next_job` / `complete_job`（scope job.run）
- [x] docs/MCP.md 同步

## 本波（MCP cancel + providers + smoke）

- [x] MCP `cancel_job` / `list_providers`
- [x] `scripts/smoke-mcp-jobs.sh` + `make smoke-mcp`

## 本波（Bindings/Devices agent 门禁）

- [x] Bindings：scope + `allowAgentDrive`；list 过滤禁止 drive
- [x] Devices：agent token 一律 403（hubd/人侧）
- [x] smoke-agent 覆盖；README MCP 工具表更新

## Close-out（主线收口 · 全部 major waves 完成）

| 波次 | 状态 |
|------|------|
| P0 无感闭环 · P1 持久化/加密/限流 · P2 厂商/STS 骨架 · P3 jobs/mcp/metrics | ✅ |
| 加固 1–4 波（配额/审计/会话吊销/refresh） | ✅ |
| 阶段 A：Agent CRUD/token/scopes + path jail | ✅ |
| 阶段 B：drive allowlist · Policy Engine · env 白名单 · Manifest 2.0 · audit.agent_id · Snapshot v0 | ✅ |
| STS：MinIO/AWS/S3-compat · Aliyun RAM · Tencent CAM · Qiniu/Oracle 标签 | ✅ best-effort |
| Objects / Snapshots / OpenAPI / smoke-* | ✅ |
| Seccomp（default/strict/netdeny）· bwrap/netns 包装 | ✅ |
| Policy 外部 JSON + job/provider CheckAccess | ✅ |
| Job `agent_id` / `claimed_by_agent_id` + 审计 + list filter | ✅ |
| MCP jobs 工具 + `make smoke-mcp` | ✅ |
| Bindings agent 门禁 · Devices 拒 agent token | ✅ |
| Qiniu 私有下载 token（`qiniu_download`） | ✅ best-effort / note assist |
| OCI API-key 私钥 IAM 校验（`oci_iam`） | ✅ best-effort validate |
| 可选 OPA/Rego（`AI_CLOUDHUB_OPA_POLICY_FILE`） | ✅ |

**结论：** 1.x 收尾 + ROADMAP 2.0 最小企业可用主线 + 可选三件套（Qiniu 下载 token / OCI IAM / OPA）已落地；文档与代码口径对齐（见 [ROADMAP-2.0.md](./ROADMAP-2.0.md)、[STS.md](./STS.md)、[POLICY.md](./POLICY.md)、[KNOWN_LIMITATIONS.md](./KNOWN_LIMITATIONS.md)）。

### Stage C 切片（已启动）

- [x] **远程 PDP**：`AI_CLOUDHUB_PDP_URL`（POST allow/reason；fail-open / observe / strict）
- [x] **OCI Customer Secret 铸造**：`AI_CLOUDHUB_OCI_CREATE_SECRET=1` → `source=oci_secret`（best-effort）
- [x] **OCI ObjectRead PAR sample**：`AI_CLOUDHUB_OCI_PAR=1` + NAMESPACE + PAR_BUCKET → note / `oci_par`
- [x] **Memory Kernel v0**：`/v1/memory*` · [MEMORY.md](./MEMORY.md)
- [x] **Marketplace v0**：`/v1/marketplace*` · [MARKETPLACE.md](./MARKETPLACE.md)
- [x] **模块注册表**：`GET /v1/modules` · [MODULES.md](./MODULES.md)（逻辑边界，非强拆进程）
- [x] **Data Lineage v0** `/v1/lineage` · **Identity Graph** `/v1/graph`
- [x] **Connectors catalog** Git/DB/SaaS · **vector memory search** · **marketplace checkout stub**
- [x] **modular compose** 双 api 副本（非 runner 池）· [STAGE-C.md](./STAGE-C.md)
- **仍禁止：** 平台大规模 Runner 池（D-001）；默认不强制领域微服务

### 剩余诚实边界

- **Qiniu versioned 原生下载**：无 `version_id` 时 `presign-get` → `qiniu_download`；带 version 仍 S3 presign
- OCI mint/PAR 依赖租户 IAM 权限；失败回退 embedded，不阻断 Issue

### 本波补强（三件套之后）

- [x] Qiniu：`ObjectPresignGet` 原生按 key 签名（`method=qiniu_download`）
- [x] OCI：Identity 校验短缓存（`AI_CLOUDHUB_OCI_IAM_CACHE_SEC`）
- [x] smoke-policy 覆盖 OPA deny `provider.write`
- [x] smoke-objects / smoke-mcp：Qiniu offline HMAC presign
- [x] docs/PRODUCTION.md + docker-compose.prod STRICT / 强制密钥
- [x] CI `make smoke-all`；nginx/Caddy TLS 反代示例
- [x] Docker 瘦身（`-s -w` / distroless API）+ CI `smoke-minio` + image build
- [x] 多架构 Release（`v*` tag）+ compose healthcheck（api / postgres / redis）
- [x] 多云接入 runbook：`docs/CLOUD-INTEGRATION.md`（OSS/COS/Qiniu/OCI 字段 · env · curl · session.note）

### Stage C 本波（install 副作用 + job clone note）

- [x] 付费/免费 **agent_template install** 成功后自动写：episodic memory、idgraph edges、lineage
- [x] install 响应 `memory_id`；审计 `marketplace.install` 保留
- [x] Job `Complete` note **追加**（不再整段覆盖），cap 2000
- [x] BYOC runner：`maybeGitConnector` 返回 dest；complete note `cloned to <path>`；env `AI_CLOUDHUB_CLONE_PATH`
- [x] `smoke-stage-c` 覆盖 install memory/graph/lineage + complete append
- [x] 文档：MARKETPLACE / CONNECTORS / STAGE-C / CHANGELOG

### Stage C 本波（skill install + clone strict + v0.2.2）

- [x] `InstallSkill` / `InstallManifest`：同一 `POST …/install`，HasPaidAccess，不创建 agent
- [x] install 副作用对 skill：memory + `user--installed-->item`（无 from_item）
- [x] Runner：`clone failed: …` 始终进 complete note；`AI_CLOUDHUB_CLONE_STRICT=1` 则 job fail
- [x] `smoke-stage-c`：free/paid skill、manifest、soft/strict clone-fail notes
- [x] 版本钉 **0.2.2**（version.go / mcp / openapi / compose）+ tag `v0.2.2`

### 本波（P0 文档 + P1 MCP Stage C）

- [x] KNOWN_LIMITATIONS：远程 PDP 已落地（纠偏「无 PDP」）
- [x] ROADMAP C3b：skill/manifest install 口径
- [x] MCP Stage C 工具 11 个 + `cmd/mcp/stage_c.go`
- [x] skill/manifest install 允许 agent token；agent_template 仍 human-only
- [x] `smoke-mcp-jobs` 覆盖 Stage C；docs/MCP.md + CHANGELOG Unreleased

### 本波（v0.2.3 · checkout_url + postgres BYOC）

- [x] Checkout 返回 `checkout_url` / `session_id`（无 secret → mock；有 `AI_CLOUDHUB_STRIPE_SECRET_KEY` → live Session）
- [x] Connector `config` JSON 对象编码（RawMessage，非 base64）
- [x] Postgres connector：create strip + host/db 校验；runner `AI_CLOUDHUB_PG_*` + `PassLibpq`
- [x] `AI_CLOUDHUB_PG_STRICT` / `AI_CLOUDHUB_PASS_PG=0`
- [x] smoke-stage-c：checkout_url + postgres strip；版本钉 **0.2.3** + tag

### 本波（mysql BYOC + OpenAPI Stage C）

- [x] MySQL connector：catalog/create strip + runner `AI_CLOUDHUB_MYSQL_*` + `PassMysql` (`MYSQL_PWD`)
- [x] `AI_CLOUDHUB_MYSQL_STRICT` / `AI_CLOUDHUB_PASS_MYSQL=0`
- [x] OpenAPI：`/v1/memory*` `/v1/marketplace*` `/v1/connectors*` `/v1/lineage` `/v1/graph` `/v1/modules` purchases/webhooks
- [x] Job schema `connector_id`；smoke-stage-c mysql strip

### 本波（v0.2.4 · MCP connector CRUD + checkout）

- [x] MCP：`create_connector` / `get_connector` / `delete_connector` / `marketplace_checkout`
- [x] smoke-mcp 覆盖 human connector CRUD + checkout_url
- [x] 版本钉 **0.2.4** + tag

### 本波（BYOC 联调 + 生产 runbook）

- [x] runner `AI_CLOUDHUB_MATERIALIZE_ONLY=1`（无 rclone 物化 git/pg/mysql）
- [x] `scripts/smoke-byoc-connectors.sh` + `make smoke-byoc`（本地 bare git + DB env）
- [x] `scripts/prod-preflight.sh` + `make prod-preflight`
- [x] PRODUCTION.md：Stage C/Stripe/PDP、BYOC 表、cutover checklist；纠偏「无远程 PDP」
- [x] 版本钉 **0.2.5** + tag；AGENTS.md smoke 表同步

### 本波（STS 联调 + Windows hubd + 可观测性）

- [x] `make smoke-sts`：offline STS path + unit + qiniu_download metrics
- [x] hubd Windows：WinFsp 预检拒绝 mount、盘符不 MkdirAll、rclone 路径探测、即死检测
- [x] `install-deps.ps1 -CheckOnly` + `smoke-windows.ps1` + WINDOWS.md checklist
- [x] Stage C metrics（install/checkout/paid/connector/memory/jobs+connector）+ Grafana 行 + METRICS.md
- [x] 版本钉 **0.2.6** + tag

### 本波（hubd mode + 挂载存活）

- [x] Mode 优先级：binding → session.mode → manifest env → mount
- [x] rclone 进程退出 → actual=error + remount；binding.mode 变更 → remount
- [x] `cmd/hubd` 单测；WINDOWS / KNOWN_LIMITATIONS 同步
- [x] 版本钉 **0.2.7** + tag

### 本波（job result + get_job + STS live）

- [x] Job `exit_code` / `duration_ms` store+API+runner+OpenAPI
- [x] MCP `get_job` + complete 结构化字段；smoke-job / smoke-mcp
- [x] smoke-sts Phase L：live MinIO STS（opt-in）

### 本波（job lease + heartbeat）

- [x] `heartbeat_at` soft migrate；claim 置位；complete/cancel/release 清空
- [x] `POST /v1/jobs/{id}/heartbeat`；claim 路径 `ReclaimStale`（`AI_CLOUDHUB_JOB_LEASE_SEC`，默认 300）
- [x] runner worker 心跳；MCP `heartbeat_job`；unit + smoke
- [x] 版本钉 **0.2.8** + tag

### 本波（job output + list status + CI STS live）

- [x] complete `stdout`/`stderr` soft migrate + tail cap（`AI_CLOUDHUB_JOB_OUTPUT_MAX`）
- [x] runner MultiWriter 捕获 agent 输出；MCP complete 字段
- [x] `GET /v1/jobs?status=` exact filter（pending 仍为 claimable 集）
- [x] CI `smoke-sts-live`（MinIO + LIVE/REQUIRE）
- [x] 版本钉 **0.2.9** + tag

### 本波（timeout + trunc flags + hubd probe）

- [x] `timeout_sec` / `claimed_at`；API fail timeout exit 124；runner CommandContext
- [x] `stdout_truncated` / `stderr_truncated` store+API+runner+MCP
- [x] hubd：mount path ReadDir 探测；`FORCE_REMOUNT_ON_REFRESH`；Windows Kill 停挂
- [x] 版本钉 **0.2.10** + tag

### 本波（attempts + job ops metrics + webhook）

- [x] `attempt_count` / `max_attempts`；lease 过期达上限 → fail
- [x] metrics：timeout / lease_reclaim / max_attempts / heartbeat / webhook_ok
- [x] `AI_CLOUDHUB_JOB_WEBHOOK_URL` terminal 异步 POST
- [x] 版本钉 **0.2.11** + tag

### 本波（priority + runner_id + webhook HMAC）

- [x] `priority` claim 排序；`claimed_by_runner_id` header/body + runner env
- [x] webhook `AI_CLOUDHUB_JOB_WEBHOOK_SECRET` HMAC-SHA256（timestamp.body）
- [x] MCP/OpenAPI/smoke；版本钉 **0.2.12** + tag

### 本波（labels + region claim + webhook envelope）

- [x] job `labels` store/API/list `label=k:v`；MCP create/list
- [x] claim region filter（header/body/`AI_CLOUDHUB_REGION`）
- [x] webhook envelope `event_id` + Event headers
- [x] 版本钉 **0.2.13** + tag
