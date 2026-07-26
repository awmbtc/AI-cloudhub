# 生产 Cutover 清单

> 控制面上线前的一次性检查。决策：D-001（无平台 Runner 池）· D-003（主线已收口，本页是 **生产纪律**，不是新功能清单）。  
> 详环境说明：[PRODUCTION.md](./PRODUCTION.md) · 自动化：`make prod-preflight` · 自测：`make smoke-prod-preflight`

## 0. 一分钟决策

| 问题 | 若「否」 |
|------|----------|
| 对象存储是用户自己的（BYOS）？ | 停：本产品不代存 body |
| Job/Agent 跑在用户机器（BYOC）？ | 停：禁止平台大池 |
| 已准备 Postgres（多副本）或接受单机 SQLite？ | 写进运维记录 |
| TLS 在边缘终止、API 只听 loopback？ | 补 nginx/Caddy |

---

## 1. 密钥与开关（硬门槛）

```bash
export JWT_SECRET="$(openssl rand -hex 32)"
export AI_CLOUDHUB_MASTER_KEY="$(openssl rand -base64 32)"
export AI_CLOUDHUB_STRICT=1
export AI_CLOUDHUB_ALLOW_REGISTER=0   # 首位 admin 建完再关
export AI_CLOUDHUB_METRICS_TOKEN="$(openssl rand -hex 16)"
export AI_CLOUDHUB_ADMIN_CIDRS="10.0.0.0/8,127.0.0.1"  # 按你的管理网改
export AI_CLOUDHUB_DB='postgres://…'
export AI_CLOUDHUB_REDIS='redis://…'
# 可选
# export AI_CLOUDHUB_HSTS=1
# export AI_CLOUDHUB_JOB_WEBHOOK_URL=… AI_CLOUDHUB_JOB_WEBHOOK_SECRET=…

make prod-preflight
```

| 项 | 必须 | 说明 |
|----|------|------|
| `JWT_SECRET` | ✅ | ≥16，生产建议 32+ hex |
| `AI_CLOUDHUB_MASTER_KEY` | ✅ | Provider 信封加密 |
| `AI_CLOUDHUB_STRICT=1` | ✅ | 弱密钥启动失败 |
| `ALLOW_REGISTER=0` | 上线后 ✅ | bootstrap 后关闭 |
| `METRICS_TOKEN` | 强烈建议 | 否则 `/metrics` 公开 |
| `ADMIN_CIDRS` | 建议 | 限制 admin API 来源 IP |
| Job webhook URL+SECRET | 成对 | 有 URL 无 SECRET → preflight **FAIL** |

---

## 2. 部署形态

| 路径 | 命令 / 文档 |
|------|-------------|
| Compose（api+pg+redis） | `deploy/docker-compose.prod.yml` |
| 裸机 | `make build` → `.bin/api` |
| 边缘 TLS | `deploy/nginx.conf.example` / `Caddyfile.example` |
| 镜像 | `make docker-api`（distroless） |

**不要**在平台账号下拉大规模 runner 集群（D-001）。

---

## 3. 功能验收（canary）

```bash
export CGO_ENABLED=0
make test
make smoke-golden              # 主线契约（无活桶）
make smoke-sts                 # 多云 session.source + fail-open
make smoke-all                 # 全量离线 smoke
# 有 MinIO 时：
make smoke-golden-minio        # 真桶 inventory + job
make smoke-sts-live            # 真 MinIO STS 路径（source 可为 embedded）
# 生产 env：
make prod-preflight
make smoke-prod-preflight      # preflight 脚本自身回归
```

产品故事见 [GOLDEN-PATH.md](./GOLDEN-PATH.md)。

---

## 4. 人机 checklist（复制打勾）

1. [ ] `make prod-preflight` → FAIL=0（WARN 已审）  
2. [ ] Postgres + Redis（多副本 API）  
3. [ ] TLS 边缘；API `127.0.0.1`；metrics token 生效  
4. [ ] 首位 admin 已建；`ALLOW_REGISTER=0`  
5. [ ]（可选）`POLICY_FILE` / OPA / PDP  
6. [ ]（可选）Stripe webhook；Job webhook URL+HMAC  
7. [ ] 用户侧 rclone / hubd / runner 安装说明已发（**非**平台池）  
8. [ ] canary：`smoke-golden`（+ 可选 `smoke-golden-minio`）  
9. [ ] 备份：Postgres 备份策略；`MASTER_KEY` / `JWT_SECRET` 进密钥库  
10. [ ] 回滚：上一版镜像/tag 已知；compose 可一键回退  

---

## 5. 明确不做（cutover 范围外）

- 托管 embedding / OpenLineage 仓 / PCI 真卡数据  
- 默认微服务拆分  
- Job admin 运维第 N 刀（D-003 freeze）  
- 控制面中转对象 body  

---

## 6. 版本

Preflight 与本清单对齐二进制 **0.2.55+**。
