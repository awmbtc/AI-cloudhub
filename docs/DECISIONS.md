# AI-cloudhub 决策记录（Decision Log）

> 重要产品/架构决策的书面记录。修订时追加条目，勿静默改历史结论。  
> 详评见 [RISK-COST.md](./RISK-COST.md)；架构定稿见 [ARCHITECTURE.md](./ARCHITECTURE.md)。

---

## D-001 · 决策基线与「自建大规模 Runner 池」黑名单

| 字段 | 内容 |
|------|------|
| **日期** | 2026-07-19 |
| **状态** | **已采纳（Accepted）** |
| **范围** | 产品默认路径、成本模型、云端 Agent 部署形态 |
| **详细条文** | [RISK-COST.md §6](./RISK-COST.md) |

### 背景

架构定稿为：Go 自研控制面 + 本机/云端 Runtime、BYOS 对象存储、路径无感、Agent 契约。  
评估风险与成本后，需把「坚持什么 / 必须买什么保险 / 禁止什么」写成可执行基线，避免实现时滑向「我们出算力养大池」或「只做 conf 套壳」。

### 决策结论

#### 1. 必须坚持（绿灯）

- **BYOS**：用户自带对象存储，我们侧成本不随文件量爆炸  
- **控制面极瘦**：字节不中转  
- **本机 hubd + 云 runner 同一套 mount/契约**  
- **Go 自研**，不魔改 MinIO、不自研对象存储内核  
- 落地顺序 **P0 → P1 → P2 → P3**

#### 2. 必须买入的保险（不做 = 风险不可接受）

1. **STS + Key 信封加密**  
2. **hubd / runner 自动挂载**（L2 一等产品）  
3. **Manifest + `AI_CLOUDHUB_WORKSPACE` 等环境变量**  
4. **预期管理**（mount ≠ 本地 NVMe）  
5. **write barrier**（刷缓存后再宣告完成）

#### 3. 最小可行集合

```text
P0 + P1 安全子集
  · 约 4～7.5 人月
  · 控制面云资源早期 < 500 元/月
  · 不做：P3 大平台、自研同步引擎、厂商一次做完、自建大规模 Runner 池
```

#### 4. 黑名单（红灯 · 默认禁止）

- 魔改 / fork MinIO 当产品内核  
- 控制面默认中转文件 body  
- 平台替用户存全站对象数据  
- 过早自研完整同步协议  
- 多租户共享同一 `/workspace`  
- **自建大规模 Runner 池**（见下）

---

### 黑名单专条：禁止「自建大规模 Runner 池」

#### 定义

```text
「自建大规模 Runner 池」=
  我们自己买/租大量云主机或 K8s 节点，
  常驻或弹性拉起成百上千个 Agent 容器，
  替（几乎）所有用户跑云端 Agent，
  算力与带宽账单主要由我们承担。
```

典型错误 enticement（禁止当作默认架构）：

- 「有云端 Agent 需求，所以我们要自建 500 节点 Runner 集群」  
- 「对标云 IDE，算力我们包了」  
- 架构图把 runner 大池画成唯一云执行方式，且不写 BYOC  

#### 为何拉黑

| 维度 | 说明 |
|------|------|
| **成本归属** | CPU/磁盘/公网随任务线性涨且 **我们付**，毁掉 BYOS 带来的「我们侧很轻」 |
| **与 BYOS 不一致** | 存储用户自带，算力却我们全包 → 半吊子商业模式 |
| **与穷部署冲突** | 控制面可月费很低；大池可轻松到 **数万～数十万/月** |
| **运维与安全** | 多租混部、脏盘、逃逸面、噪音邻居 |
| **产品焦点** | 沦为卖云主机；偏离「盘映射 + 自动挂载 + Agent 契约」 |

#### 云端 Agent 仍然要做——允许形态（白名单）

```text
✅ 1. 官方 runner 镜像 / entry 脚本
      → 跑在用户自己的 ECS / 轻量 / K8s / CI / 自有机（BYOC）
✅ 2. 本机 hubd（用户自己的电脑算力）
✅ 3. 极小规模演示/内测 Runner（个位数节点，有预算帽）
✅ 4. 用用户云账号弹性起机（账单进用户云账号）

❌ 默认禁止：
  · 在我们云账号下维持大规模常驻/无上限弹性 Agent 农场
  · 「免费无限云端 Agent」且无算力计量转嫁
  · 未做 STS/隔离的多租户混部大池
```

#### 例外条件（商业上必须我们出算力时）

不得偷偷做。须 **同时** 满足：

1. **单独 SKU**（算力包），与 Drive Map 核心订阅拆开定价  
2. **硬配额 + 计量计费**（超时杀、并发帽、磁盘帽）  
3. **预算熔断**（账户日/月上限）  
4. **成本模型过会**（更新 RISK-COST 与定价表）  
5. 架构仍优先 **用户云账号执行**  

未满足以上 5 条 → **维持黑名单**。

#### 文案对齐

| 说法 | 正确理解 |
|------|----------|
| Cloud Runtime / runner 镜像 | **软件交付物**；跑在哪由用户或计费 SKU 决定 |
| Runner 与存储同区域 | **调度建议**，不是我们自建全球机房 |
| 可扩缩 runner 池 | 指 **用户侧或计费 SKU 池**，默认不是我们免费大池 |

### 后果（违反本决策）

- 视为偏离架构定稿与成本模型  
- 须先修订本文件与 RISK-COST §6，再实现  

### 相关链接

- [RISK-COST.md §6 决策建议](./RISK-COST.md)  
- [ARCHITECTURE.md §12 部署](./ARCHITECTURE.md)  

---

---

## D-002 · 2.0 演进：Agent IAM 优先，拒绝过早微服务与万能连接器

| 字段 | 内容 |
|------|------|
| **日期** | 2026-07-20 |
| **状态** | **已采纳（Accepted）** |
| **范围** | 产品演进主线、架构边界、与外部 2.0/3.0 愿景的对齐方式 |
| **施工图** | [ROADMAP-2.0.md](./ROADMAP-2.0.md) |

### 背景

外部审计与《2.0/3.0 架构演进》报告将产品定位从「AI 文件系统」拉到「Agent Workspace Infrastructure / Data Plane」。方向正确，但清单过大（微服务拆分、Marketplace、Memory Kernel、Git/DB/SaaS 一等存储）。需书面裁剪，避免实现漂移。

### 决策结论

#### 1. 采纳的北极星

- 产品是 **Agent 访问用户自有数据的安全数据平面**，不是 C 端网盘 UI  
- **Human Identity ≠ Agent Identity**；Agent 用 Capability Token（scopes + 可选资源范围）  
- Runtime 必须 **Sandbox / path jail**，否则 BYOS 叙事在执行端断链  

#### 2. 演进接到现有模型（禁止平行宇宙）

- Workspace ≈ Drive + Binding + Manifest（增强字段，不另起一套）  
- Policy v0 = scope 校验 + 归属 + 后续 JSON 规则；**不**一上来 OPA 全家桶  
- Audit Graph v0 = 扩展 `audit_events` 关联字段，**不**先上图数据库  

#### 3. 默认禁止（本阶段）

| 禁止 | 原因 |
|------|------|
| 未成规模前拆 identity/policy/memory 等微服务 | 拖慢真正缺口；monorepo 模块边界足够 |
| 2.0 初版做 Marketplace / 完整 Memory Kernel | 无客户验证的范围膨胀 |
| 对象存储未做深即把 Git/DB/SaaS 列为一等存储 | 稀释 Drive Map + STS + Mount 独特性 |
| Agent 默认使用用户登录全权 token | 扩大泄露与越权面 |

#### 4. 阶段顺序（摘要）

```text
阶段 A（当前）  Agent CRUD + agent token(scopes) + path jail
阶段 B（2.0）   Policy v0 · Sandbox v1 · Manifest permissions · audit agent_id · snapshot v0
阶段 C（3.0）   规模化后再 Kernel 叙事/可选服务拆分/生态/连接器扩展
```

### 相关链接

- [ROADMAP-2.0.md](./ROADMAP-2.0.md)  
- [ARCHITECTURE.md §3.1](./ARCHITECTURE.md)  
- D-001 大规模 Runner 池黑名单  

---

## 记录索引

| ID | 标题 | 状态 |
|----|------|------|
| D-001 | 决策基线与自建大规模 Runner 池黑名单 | Accepted |
| D-002 | 2.0 演进：Agent IAM 优先，拒绝过早微服务 | Accepted |
