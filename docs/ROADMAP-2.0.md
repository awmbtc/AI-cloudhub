# AI-cloudhub 可落地演进路线（1.x 收尾 → 2.0 → 3.0）

> **地位**：施工图（优先于外部愿景文档）。  
> 外部《2.0/3.0 架构演进》仅作叙事参考，见 [ROADMAP-2.0-3.0-外部架构演进报告.md](./ROADMAP-2.0-3.0-外部架构演进报告.md)。  
> 架构定稿：[ARCHITECTURE.md](./ARCHITECTURE.md) · 决策：[DECISIONS.md](./DECISIONS.md) D-001/D-002。

---

## 0. 北极星（不变）

```text
不是 AI 网盘 UI。
是：人和 Agent 访问「用户自有数据」的安全数据平面（BYOS + BYOC）。
```

| 坚持 | 禁止（默认） |
|------|----------------|
| BYOS 对象存储，控制面不中转文件 | 平台代存全站对象数据 |
| hubd / runner 同一契约 | 自建大规模 Runner 池（D-001） |
| Go monorepo 控制面先做深 | 未成规模前拆 10 个微服务 |
| Agent 身份与最小权限 | Agent 默认继承用户全权 |

---

## 1. 与现有模型的映射（不要平行宇宙）

| 2.0 概念 | 接到现有实体 | 本阶段最小实现 |
|----------|--------------|----------------|
| Human Identity | `users` + role | 已有 |
| Agent Identity | 新表 `agents`（owner = user） | **本波骨架** |
| Workspace | Drive + Binding + Manifest 视图 | Manifest 增加 agent/scope 字段 |
| Capability Token | access token 扩展字段 | `agent_id` + `scopes[]` |
| Policy Engine | 先 JSON/硬编码规则 | scope 校验 + 归属；引擎后置 |
| Sandbox | runner/hubd + jail 包 | **路径 jail v0** |
| Audit Graph | `audit_events` 扩展字段 | action 已有；逐步加 agent_id |
| Snapshot / Memory / Marketplace | — | **Not in 1.x / 2.0 初版** |

```text
User
 ├── Agent[]          ← 新增：智能体主体
 ├── Provider[]
 ├── Drive[]
 ├── Binding[]
 ├── Device[]
 ├── Job[]            ← 后续可挂 agent_id
 └── Token (access)  ← 可带 agent_id + scopes
```

---

## 2. 版本切分（可执行）

### 阶段 A · 1.x 收尾（当前波次，可演示安全加固）

**目标**：Agent 有名字、Token 可降权、Runtime 默认不越界读写路径。

| # | 落脚点 | 验收 |
|---|--------|------|
| A1 | `agents` CRUD（用户自建，admin 可看） | API + store 三实现 |
| A2 | 签发 Agent Token：`POST /v1/agents/{id}/token` | token 含 `aid` + `scopes` |
| A3 | Parse 暴露 agent_id/scopes；敏感 admin 仍要求人身份 | 无 aid 的用户 token 行为不变 |
| A4 | `internal/sandbox` 路径 jail | 拒绝 `..` 与 workspace 外路径 |
| A5 | runner 执行命令前校验 cwd/args 路径 | 单元测试 + 文档 |

### 阶段 B · 2.0 最小企业可用

| # | 落脚点 | 状态 | 说明 |
|---|--------|------|------|
| B1 | Capability 绑定 Drive 列表 | **done** | `allowed_drive_ids`；session/list 过滤 |
| B2 | Policy v0 | **done** | `internal/policy` Engine（scope+drive+path 前缀） |
| B3 | Sandbox v1：env 白名单 | pending | 完整 seccomp 可后置；path jail 已在 A |
| B4 | Manifest 2.0：permissions.read/write | **done** | version=2 + prefixes |
| B5 | Audit 关联 `agent_id` | **done** | 字段 + `?agent_id=` 过滤 |
| B6 | Snapshot v0 | pending | 非全量 Git FS |

### 阶段 C · 3.0 蓝图（有客户与规模后再做）

- Kernel 化命名与**模块边界**清晰后再谈服务拆分  
- Data Lineage / EDA  
- Workspace / Agent Marketplace  
- Git / DB / SaaS Connector（对象存储做深之后）  
- 完整 Memory Kernel、Identity Graph 产品化  

---

## 3. 明确 Not in Scope（写进评审）

**2.0 初版不做：**

- 微服务拆分（identity-service / policy-service …）  
- MCP Tool Marketplace  
- 企业级 Memory 三层产品  
- 默认支持 Git/DB/SaaS 作为一等存储（对象存储优先）  
- 完整 OPA/Rego 引擎  
- 自建大规模 Runner 池  

---

## 4. 阶段 A 技术规格（本仓库正在落地）

### 4.1 Agent

```text
Agent {
  id, owner_user_id, name, description,
  status: active|disabled,
  default_scopes: string[],   // e.g. drive.read, drive.write, job.run
  created_at
}
```

API（用户鉴权）：

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/agents` | 创建 |
| GET | `/v1/agents` | 列表（己方） |
| GET | `/v1/agents/{id}` | 详情 |
| DELETE | `/v1/agents/{id}` | 删除 |
| POST | `/v1/agents/{id}/token` | 签发短时 Agent access token |

### 4.2 Token 字段扩展

```json
{
  "uid": "user-uuid",
  "un": "alice",
  "role": "user",
  "exp": 123,
  "jti": "...",
  "tv": 0,
  "aid": "agent-uuid",
  "scopes": ["drive.read", "drive.write"]
}
```

- 人登录 token：`aid` 空，scopes 视为全权限（兼容 1.x）  
- Agent token：必须带 `aid`；API 层对写操作检查 scopes  
- 吊销：仍用 `jti` / `token_version`（与第三波一致）

### 4.3 Scope 词汇表（v0）

| scope | 含义 |
|-------|------|
| `drive.read` | 读 drive/binding/session（只读） |
| `drive.write` | 创建/改 drive、binding desired |
| `job.run` | 创建/claim job |
| `provider.read` | 列 provider（无 secret） |
| `provider.write` | 创建/删除 provider |

未列出的 scope → 默认拒绝（Agent token）；人 token 不受限（admin 另算）。

### 4.4 Path Jail v0

```text
Allow:  mount_point 及其子路径（清理后的 abs path）
Deny:   .. 穿越、符号链接逃逸（尽力）、空路径
```

包：`internal/sandbox`  
调用方：`cmd/runner`（命令 cwd / 显式路径参数）。

---

## 5. 里程碑验收清单

### M-A（本波结束）

- [x] 文档：本 ROADMAP + ARCHITECTURE 演进节 + D-002  
- [x] `go test ./...` 绿  
- [x] 创建 agent → 签发 token → 用 agent token 调 API  
- [x] agent token 无 `drive.write` 时创建 drive 失败  
- [x] jail 拒绝 `/etc/passwd` 与 `../../../etc`（unit + runner 默认开启）

### M-B（2.0）

- [ ] Policy 文件或表驱动  
- [ ] Manifest permissions  
- [ ] Audit 带 agent_id  

---

## 6. 与外部 2.0/3.0 报告的关系

| 外部报告说法 | 本仓库处理 |
|--------------|------------|
| Agent IAM | **采纳**，阶段 A/B 落地 |
| Policy Engine / Sandbox | **采纳**，B 做深，A 先 jail+scope |
| Memory / Snapshot / Marketplace | **推迟**到 C 或 B 末 |
| 微服务拆分 | **拒绝**作为 2.0 默认 |
| 数据平面扩到 Git/DB/SaaS | **对象存储做深后再开** |

---

*修订时更新本文件与 PROGRESS.md，勿只改外部拷贝。*
