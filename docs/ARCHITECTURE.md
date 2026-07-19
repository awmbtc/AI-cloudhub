# AI-cloudhub 架构（定稿 · 最强解）

> **产品名：AI-cloudhub**  
> **定位：人和 Agent 的多云磁盘操作系统（Workspace OS）**  
> **技术：100% Go 自研控制面 + 本机/云端 Runtime；不是魔改 MinIO，不是网盘。**  
> **存储：BYOS**（用户自带 R2 / S3 / OSS / MinIO…）；字节不经控制面。

配套：[VENDORS.md](./VENDORS.md) · [BUDGET-WOOL.md](./BUDGET-WOOL.md) · [RISK-COST.md](./RISK-COST.md) · [DECISIONS.md](./DECISIONS.md) · **[ROADMAP-2.0.md](./ROADMAP-2.0.md)**（可落脚演进）

---

## 0. 口径

| 是 | 不是 |
|----|------|
| Go 自研：API、Drive、STS、Job、Runtime 契约 | MinIO fork / 二开 / 魔改发行版 |
| 多厂商 S3 兼容适配 + 自动挂载 | 自研对象存储纠删码内核 |
| Key → 逻辑盘 → 本机/云路径无感读写 | 以业务「上传 API」为主路径的网盘 |

```text
✅ Go 自研 Drive Map + Runtime；用户自带对象存储
❌ 基于 MinIO 魔改的网盘
```

---

## 1. 一句话架构

```text
用户 API Key
    → 控制面定义逻辑盘（Drive）
    → Runtime（本机 hubd / 云 Runner）自动挂载
    → Agent 只认 AI_CLOUDHUB_WORKSPACE
    → zip/cp/Write 直达用户对象存储（本地写缓存保证不卡）
```

---

## 2. 总图

```text
                    ┌──────────────────────────────────────┐
                    │  Control Plane · Go · 无状态可扩       │
                    │  IAM · Provider · Drive · Binding     │
                    │  STS · Policy · Job · Audit · Manifest│
                    │  PostgreSQL · Redis · 密钥信封加密      │
                    └──────────────────┬───────────────────┘
                                       │
              HTTPS：短时凭证 · Desired 状态 · Workspace Manifest
                                       │
          ┌────────────────────────────┼────────────────────────────┐
          ▼                            ▼                            ▼
┌──────────────────┐        ┌──────────────────┐        ┌──────────────────┐
│ Desktop Runtime  │        │ Cloud Runtime    │        │ IDE / CI / MCP   │
│ hubd（本机守护）   │        │ runner 镜像       │        │ 同一契约          │
│ 自动挂 G: / 路径  │        │ 自动挂 /workspace │        │                  │
│ 续期 · 重挂 · 健康 │        │ Job 生命周期 mount │        │                  │
│ 注入 Agent 环境   │        │ 结束 umount       │        │                  │
└────────┬─────────┘        └────────┬─────────┘        └────────┬─────────┘
         │ 写回缓存 / VFS              │                           │
         └────────────────────────────┼───────────────────────────┘
                                      │ S3 兼容（STS 派生，非长期 Key 裸奔）
                                      ▼
                           用户对象存储 BYOS
                           R2 · B2 · OSS · COS · MinIO · S3 …
```

### 三层职责（硬边界）

| 层 | 组件 | 职责 | 禁止 |
|----|------|------|------|
| **L1 控制面** | `cmd/api` | 身份、Key 保管、盘定义、策略、STS、任务编排、审计、Manifest | 中转文件 body、当下载服务器 |
| **L2 运行时** | `cmd/hubd` + `runner` **镜像** | **自动挂载、缓存、健康、续期、Agent 环境注入**；默认跑在 **用户算力（BYOC）** | 自己存用户文件；跨租户共享挂载点；**我们账号下无配额的大规模 Runner 池**（见 RISK-COST 黑名单） |
| **L3 存储** | 用户 BYOS | 字节持久化 | — |

> **L2 是一等产品。** 只返回「请复制 rclone 命令」不是完整架构。

---

## 3. 核心模型

```text
Tenant / User
  ├── Agent[]        智能体主体（不直接继承用户全权；见 ROADMAP-2.0）
  ├── Provider[]     云厂商凭证（加密静态存储）
  ├── Drive[]        逻辑盘 = provider + bucket + prefix + 策略
  ├── Binding[]      某 Device/Runner 上的挂载实例（desired / actual）
  ├── Device[]       本机 hubd 设备身份
  ├── Job[]          云端 Agent 任务（后续可挂 agent_id）
  └── Quota / Policy 限流、并发、路径策略、scope
```

| 概念 | 含义 |
|------|------|
| **Provider** | 一把（组）对象存储 API Key + endpoint |
| **Drive** | 逻辑盘（与「在哪台机器」无关） |
| **Binding** | Drive 在具体设备上的挂载：`mount_point`、`desired=mounted`、`actual`、错误与延迟 |
| **Agent** | 用户名下的智能体身份；用 **Capability Token**（`aid` + `scopes`）访问 API，而非裸用用户密码 token |
| **Runtime** | 执行 desired→actual 的守护进程/容器；**路径 jail** 约束工作区 |
| **Manifest** | 给 Agent 的机器可读工作区契约（含 allowed_paths） |

同一 **Drive ID** 可同时：本机 `G:` + 云端 `/workspace`，对象命名空间一致。

### 3.1 身份分层（1.x→2.0）

```text
Human User Token     → 全权限（兼容）；管理 Agent / Provider
Agent Token          → aid + scopes（最小权限）
STS Session          → 对象存储短时凭证（挂载用，不替代 API 身份）
```

演进施工图见 [ROADMAP-2.0.md](./ROADMAP-2.0.md)。**不**默认拆微服务；**不**默认建设平台大规模 Runner 池。

---

## 4. 双执行面（一等公民）

| | 本机 Desktop | 云端 Agent |
|--|--------------|------------|
| Runtime | **hubd** | **runner** 镜像 / Job |
| 默认挂载点 | `G:` 或 `~/AI-CloudHub/<drive>` | **`/workspace`**（固定约定） |
| 谁触发挂载 | hubd 根据 desired 自动 | Job 启动自动，结束 umount |
| Agent | Cursor / Claude Code / 本地脚本 | 云上 Agent 进程 |
| 凭证 | 设备登录 + STS 续期 | 任务 Token + STS |

```text
本机 Agent (G:) ──┐
                  ├── Runtime mount ──► 同一 Drive ──► 用户桶
云端 Agent (/workspace) ──┘
```

---

## 5. 自动挂载闭环（Key → 盘符）

```text
1. 用户绑定 Provider（API Key）
2. 创建 Drive（选 bucket/prefix）
3. 设置 Binding.desired = mounted，mount_point = G: 或 /workspace
4. Runtime（hubd / runner）：
     拉 STS → 写临时 conf → mount → 上报 actual=mounted
5. 凭证将过期：Runtime 静默续期 / 重挂
6. desired=unmounted 或 Job 结束：umount + 销毁临时凭证
```

| 状态 | 含义 |
|------|------|
| desired | 控制面意图 |
| actual | Runtime 回报 |
| degraded | 已挂载但延迟/错误超阈 |
| revoked | 控制面吊销，Runtime 必须断开 |

---

## 6. 无感与性能

### 6.1 模式

| mode | 行为 | 适用 |
|------|------|------|
| **`mount`（默认）** | FUSE/WinFsp + **写回缓存**；按需拉对象 | 日常 Agent、编辑、zip |
| **`sync_workspace`** | 会话级同步到本地 SSD 工作区，结束回写 | 重度编译、大目录 |
| **`direct`** | SDK/工具直打 S3（无 FUSE） | 无 FUSE 的流水线 |

主路径：**mount + 本地 SSD 写缓存**。禁止以业务上传 API 为默认。

### 6.2 性能杠杆

| 杠杆 | 做法 |
|------|------|
| 零中转 | 执行面 ↔ 对象存储直连 |
| 写缓存 | 产品化 VFS full / write-back；缓存盘配额 |
| 大文件 | multipart 并行 |
| 元数据 | dir cache、负缓存 |
| 调度 | 云 Runner **贴存储 region** |
| 后端推荐 | R2 等免出口；国内同云 VPC 内网 endpoint |

### 6.3 写完成屏障（必须）

```text
文件关闭 / fsync / Agent 声明 task.complete
  → Runtime 刷缓存
  → 可选上报 write_barrier=ok
  → 其它端保证可见
```

无 barrier 的「无感」不可靠。

---

## 7. 多用户与规模

| 机制 | 方案 |
|------|------|
| API | 无状态多副本 + LB |
| 元数据 | PostgreSQL + 连接池 |
| 会话/限流 | Redis |
| 密钥 | KMS/本地主密钥信封加密；**禁止**长期 Key 进镜像 |
| 隔离 | **每 Job 独立 mount 命名空间**；禁止多租户共用 `/workspace` |
| 凭证 | **STS 短时**（如 15m–1h），Runtime 续期 |
| 配额 | 并发 Job、API QPS、逻辑容量策略 |
| 审计 | mount/umount/STS 发放；不记文件内容 |

控制面可轻量扩展；容量与流量成本在用户对象存储侧。

---

## 8. Agent 识别契约

Agent **不得靠猜测路径**。Runtime 挂载成功后必须注入：

### 8.1 环境变量（强制）

```text
AI_CLOUDHUB_WORKSPACE=/workspace    # 或 G:\ 等
AI_CLOUDHUB_DRIVE_ID=<drive_id>
AI_CLOUDHUB_MODE=mount|sync_workspace
AI_CLOUDHUB_API=https://<control-plane>
```

### 8.2 Workspace Manifest（强制机器可读）

路径示例：`$AI_CLOUDHUB_WORKSPACE/.ai-cloudhub/manifest.json` 或 Runtime 旁路文件。

```json
{
  "version": 1,
  "product": "AI-cloudhub",
  "drive_id": "drv_xxx",
  "mount_point": "/workspace",
  "mode": "mount",
  "write_barrier": "fsync_on_close",
  "agent": {
    "allowed_paths": ["/workspace"],
    "deny_upload_tools": true,
    "instructions": "All artifacts MUST be written under AI_CLOUDHUB_WORKSPACE. Do not use cloud upload APIs."
  }
}
```

### 8.3 可选

| 组件 | 作用 |
|------|------|
| MCP Server | `ensure_mounted` / `workspace_root` / `list_drives` |
| IDE 规则片段 | 一键写入系统提示 |

**成功标准：** 任意主流 Agent 只配置工作区根目录 = `AI_CLOUDHUB_WORKSPACE` 即可正确落盘。

---

## 9. 控制面 API（目标面）

| 领域 | 能力 |
|------|------|
| Auth / IAM | 注册登录、设备授权、Token |
| Providers | 绑定/轮换/删除 Key；catalog 厂商 |
| Drives | CRUD 逻辑盘 |
| Bindings | desired mount 状态、列表 actual |
| STS | `POST .../credentials/ephemeral` |
| Mount session | Runtime 拉配置（短时） |
| Jobs | 创建/取消云端 Agent 任务、日志句柄 |
| Manifest | 按 drive + runtime 生成 |
| Admin | 配额、审计查询 |

**字节面：** 仅对象存储；控制面无文件代理主路径。

---

## 10. 仓库结构（目标）

```text
AI-cloudhub/
├── cmd/
│   ├── api/           # L1 控制面
│   ├── hubd/          # L2 本机自动挂载守护
│   └── runner/        # L2 云端入口
├── internal/
│   ├── auth/ · iam/
│   ├── provider/      # s3/r2/minio → b2/oss/cos → …
│   ├── drive/         # Drive + Binding 状态机
│   ├── sts/
│   ├── manifest/
│   ├── policy/
│   ├── job/
│   └── mountlib/      # 与 rclone/FUSE 集成的统一封装
├── protocols/
│   └── workspace-manifest.schema.json
├── deploy/
│   └── runner-image/
└── docs/
    ├── ARCHITECTURE.md    # 本文件（唯一架构定稿）
    ├── VENDORS.md
    └── BUDGET-WOOL.md
```

### 与当前代码映射

| 目标 | 现状 |
|------|------|
| `cmd/api` Provider/Drive/mount 下发 | ✅ 批次 A |
| `cmd/hubd` 自动挂载 + 会话临期续挂 | ✅ P0 |
| STS / Manifest / Binding | ✅ P0 |
| `internal/mountlib` | ✅ |
| `cmd/runner` 云端 BYOC | ✅ P0 |
| SQLite 持久化 users/providers/drives/bindings | ✅ P1（`AI_CLOUDHUB_DB`） |
| Provider Key 信封加密 | ✅ P1（`AI_CLOUDHUB_MASTER_KEY`） |
| 限流 / write barrier API | ✅ P1 基础 |
| 厂商 B：`b2`/`oss`/`cos` | ✅ |
| PG/Redis 生产级、真云 STS | ⏳ 后续 |
| 厂商 C 批 | ❌ |

实现以本文件为 **DoD**，分 P0→P3 落地，不另起一套架构叙事。

---

## 11. 厂商适配

S3 兼容统一 Driver；模板差异（endpoint / path-style / region）。

| 批次 | ID | 角色 |
|------|-----|------|
| **A** | `s3` `r2` `minio` | 已实现 Resolve + rclone 交付 |
| **B** | `b2` `oss` `cos` | 下一波 |
| **C** | `qiniu` `oracle` … | 按需 |

详见 [VENDORS.md](./VENDORS.md)。

---

## 12. 部署

```text
┌─ 控制面（轻量可多副本）─┐     ┌─ Runtime（算力 BYOC）────────────┐
│ api × N + PG + Redis   │────►│ hubd：用户本机                     │
└────────────────────────┘     │ runner 镜像：用户云主机/K8s/自有机   │
                               │ （禁止默认「我们账号下大规模 Runner 池」）│
                               └───────────┬────────────────────────┘
                                           ▼
                                    用户对象存储
```

- 控制面成本可羊毛级（见 [BUDGET-WOOL.md](./BUDGET-WOOL.md)）。  
- 对象容量与出口在用户侧。  
- Runner 与存储 **同区域** 为调度建议（用户侧选区），不是我们自建全球机房。  
- **决策与黑名单**（含「自建大规模 Runner 池」定义与例外条件）见 [RISK-COST.md §6](./RISK-COST.md)。

---

## 13. 安全基线

1. 静态 Key **信封加密**；API 响应永不回显 secret  
2. Runtime 仅持 **STS**；过期自动续或断开  
3. 吊销用户/Provider → 续期失败 → 会话结束  
4. 多租户禁止共享挂载目录  
5. 审计 mount/STS；内容不进日志  
6. 云镜像不含用户永久密钥  

---

## 14. 架构验收（DoD）

- [ ] 本机：Binding desired=mounted 后 **无需手敲命令** 出现盘符  
- [ ] 云端：Runner 启动后 Agent 仅写 `AI_CLOUDHUB_WORKSPACE` 即落用户桶  
- [ ] 同一 Drive 本机与云端对象命名空间一致  
- [ ] 控制面宕机不导致「设计上必须经 API 传文件」  
- [ ] 吊销后一个 STS 周期内会话不可续  
- [ ] 无默认 body 中转上传路径  
- [ ] 对外口径永不写成 MinIO 二开  
- [ ] write barrier 后跨端可见  

---

## 15. 落地优先级

| 阶段 | 内容 |
|------|------|
| **P0** | hubd 自动挂载 · STS · Manifest/环境变量 · runner 与 mountlib 统一 |
| **P1** | PG · 密钥加密 · Binding 状态机 · 限流配额 · 审计 |
| **P2** | 缓存档位 · write barrier · 区域调度 · 厂商 B 批 |
| **P3** | Job **编排 API**（调度到用户侧/计费 SKU，非免费大池）· MCP · 可观测 · 厂商 C 批 |
| **1.x→2.0** | **Agent Identity · Capability scopes · Runtime path jail**（见 [ROADMAP-2.0.md](./ROADMAP-2.0.md)） |

P0–P3 骨架已基本落地；当前主线是 **ROADMAP 阶段 A/B**，不是继续堆网盘功能。

---

## 16. 总纲

> **AI-cloudhub = Go 自研控制面 + 本机/云端统一 Runtime。**  
> 用户 API Key 定义逻辑盘；Runtime 自动挂载并注入 Agent 工作区契约；  
> 文件直打用户对象存储，写缓存保证体感接近本地；  
> 多用户靠 STS、会话隔离与无状态 API 水平扩展。  
> **Agent 使用降权 Token + 路径 jail，不默认继承用户全权。**  
> **不是网盘，不是魔改 MinIO——是给人和 Agent 用的多云磁盘操作系统。**
