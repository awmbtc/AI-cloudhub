# 实现进度（对照架构定稿）

## 总览：接近 v0.1 可演示收尾

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

## 仍可后续

- MCP 工具级权限 + 路径 jail
- Snapshot 对象级版本 / 真回滚
- 更多厂商原生 STS
- Admin IP allowlist
