# 产品叙事：作为其他 Agent 软件的「盘插件」

> **状态：** 产品方向记录（2026-09-06）— 先写清逻辑，不等于本迭代已全部落地。  
> **对齐：** [ARCHITECTURE.md](./ARCHITECTURE.md) · [PROVIDERS.md](./PROVIDERS.md) · [MCP.md](./MCP.md) · [QUICKSTART-AGENT.md](./QUICKSTART-AGENT.md) · [DECISIONS.md](./DECISIONS.md)（D-001 / D-003）

---

## 1. 一句话

用户在 AI-cloudhub **控制面**配好多云桶密钥，并把桶映射成稳定「盘符」（如 A=R2、B=腾讯云 COS）。  
其他 Agent 软件通过 **登录 / Agent Token** 安装本产品的插件面后，人对 Agent 说「文件放到 AI-cloudhub 的 A 盘」——Agent **只认盘符 A**，不必知道 R2；字节经 Runtime（hubd / BYOC runner）写入用户自己的桶（BYOS）。

```text
云厂商控制台 AK/SK
  → Provider（密钥）
  → Drive（逻辑盘 = 盘符 A/B …）
  → 其他 Agent 客户端：登录 + 插件（MCP / 约定工具）
  → Agent：「写到 A 盘」
  → hubd/runner 或 session/presign
  → 用户自己的 R2 / COS / …
```

**不是：** 网盘网页上传 UI、平台代存对象、平台大规模 Runner 池（D-001）。

---

## 2. 用户心智 ↔ 现有模型

| 用户说法 | 系统对象 | 说明 |
|----------|----------|------|
| 「后台设好几个桶的 API Key」 | **Provider** | 密钥只在控制面；生产用 `AI_CLOUDHUB_MASTER_KEY` 信封加密 |
| 「R2 定义成 A 盘，腾讯定义成 B 盘」 | **Drive** | `provider_id` + `bucket` + 可选 `prefix`；人类可读名 / `mount_point` 充当盘符 |
| 「在其他 Agent 软件里添加此插件并登录」 | **认证 + Agent Token** | 人账号登录（或 OAuth 未来项）；再发带 `scopes` + `allowed_drive_ids` 的 Agent Token |
| 「跟 Agent 说放到 AI-cloudhub 的 A 盘」 | **盘符约定 + 工具/挂载** | Agent 看到的是 Drive 别名或挂载路径，不是厂商 SDK |
| 「文件其实进了 R2」 | **L2 Runtime / STS** | hubd 本机挂载或 runner BYOC；控制面 **不**中转对象 body |

核心边界：

| 谁 | 知道什么 | 不知道 / 不该碰 |
|----|----------|-----------------|
| 人（管理员） | 哪家云、哪把钥匙、哪块盘叫 A/B | 不必把 SK 交给每个 Agent 客户端 |
| Agent | A 盘 / B 盘、工作区路径、MCP 工具 | 长期 AK/SK、厂商细节（默认） |
| 插件宿主（Cursor / Claude / 其他） | 如何挂 MCP、如何带 Token | 不应持久化用户云密钥 |
| AI-cloudhub 控制面 | IAM、Drive Map、STS、审计 | 不存用户文件字节 |

---

## 3. 端到端流程（目标体验）

### 3.1 人：只做一次（或很少改）

1. 在云厂商控制台创建最小权限 AK/SK，建好桶。  
2. 登录 AI-cloudhub API / 控制面（现网或自建）。  
3. `POST /v1/providers` 登记密钥（见 [PROVIDERS.md](./PROVIDERS.md)）。  
4. `POST /v1/drives` 建逻辑盘：例如 name=`A` → R2 某桶；name=`B` → COS 某桶。  
5. （可选）建 Agent，设 `allowed_drive_ids` 只含 A/B，scopes 最小（如 `drive.read` / `drive.write`）。

### 3.2 其他 Agent 软件：添加插件

1. 用户在宿主里「添加 AI-cloudhub 插件」。  
2. 完成登录（人 token 或未来 OAuth）→ 换取 **Agent Token**（短时、可吊销；禁用 Agent 须立即失效，见审计 P0 修复）。  
3. 宿主拉起 `cmd/mcp`（stdio）或未来正式 MCP/插件协议，注入：
   - `AI_CLOUDHUB_API`
   - `AI_CLOUDHUB_TOKEN`（Agent Token）
4. （推荐）本机另跑 **hubd**，按 Binding 把 A/B 挂到约定路径；或依赖 MCP + session/presign 路径（无 FUSE 时可用 `sync_workspace` / 工具面）。

### 3.3 Agent：只认盘符

人对 Agent：

> 把报告放到 AI-cloudhub 的 A 盘 `/reports/…`

Agent 应：

1. 用工具确认 A 盘存在且在 allowlist（如 `list_drives`）。  
2. 写入 **A 的工作区路径**（Manifest / `AI_CLOUDHUB_WORKSPACE` / mount_point），或经产品约定的「按 drive 写」工具。  
3. **不要**自行拼 R2 endpoint 或索要 SK。

结果：对象出现在用户 R2 桶中；叙事上仍是「写进了 A 盘」。

---

## 4. 插件面需要什么（能力清单）

| 能力 | 现状 | 缺口 / 备注 |
|------|------|-------------|
| Provider + Drive CRUD | ✅ API | 缺友好「盘符别名」一等字段时可先用 `name` |
| Agent Token + scopes + drive 白名单 | ✅ | 禁用/降权即时失效已加强 |
| MCP tools（list drives/objects、job…） | ✅ `cmd/mcp`（compatible-ish） | 各宿主「一键安装」体验未产品化 |
| 本机自动挂载 | ✅ hubd | 依赖 rclone + FUSE；无 FUSE 用 sync_workspace |
| 登录 / OAuth 装插件向导 | ⚠️ 现为 API login | **产品缺口**：宿主内登录页、深链、token 注入 |
| 「A 盘」口语 → Drive 解析 | ⚠️ 靠 name/约定 | 可补：稳定 alias、MCP `resolve_drive("A")` |
| 跨宿主官方插件包 | ❌ | Cursor / Claude Desktop 等需分别适配 |

---

## 5. 必须坚持的红线

摘自决策日志，插件叙事不得违背：

1. **BYOS**：文件进用户桶，控制面不代理 body（D-001 成本模型）。  
2. **算力 BYOC**：其他软件里的 Agent 跑在用户侧；禁止默认「我们养 Runner 大池」。  
3. **人身份 ≠ Agent 身份**：插件默认用 Capability Token，不用用户全权密码 token。  
4. **主线收口（D-003）**：插件体验优先走黄金路径（Key → Drive → Session/挂载 → Agent 写盘），不要用 Job 运维雕花冒充插件完成度。

---

## 6. 成功标准（验收口径）

逻辑「通」且可演示时，至少满足：

1. 用户只配置一次 Provider+Drive（A=R2，B=COS）。  
2. 在某一外部 Agent 宿主中，仅通过登录 + 插件配置（API + Agent Token + MCP）接入。  
3. 自然语言 / 工具调用「写入 A 盘」后，对象出现在对应 R2 桶；写入 B 盘则进 COS。  
4. Agent 日志与工具参数中 **不出现** 长期 SecretKey。  
5. 禁用该 Agent 后，旧 Token 在合理时间内不可再用。

自动化对齐：`make smoke-golden` / `smoke-golden-minio` / `smoke-mcp` / 可选 `smoke-hubd-fuse`；人工剧本见 [GOLDEN-PATH.md](./GOLDEN-PATH.md)、[QUICKSTART-AGENT.md](./QUICKSTART-AGENT.md)。

---

## 7. 建议的下一步（文档级，非本文件承诺排期）

| 优先级 | 项 |
|--------|-----|
| P0 | 盘符别名约定写进 OpenAPI / MCP（`name` 或 `alias=A`），并在 QUICKSTART 给「对 Agent 怎么说」例句 |
| P1 | 某一宿主（如 Cursor MCP）一页安装说明：命令、env、登录换 token |
| P2 | 控制面简易「连接向导」或落地页引导（仍可不做完整网盘 UI） |
| 冻结 | 不为插件叙事新开 Job admin / 平台 Runner 池 |

---

## 8. 修订记录

| 日期 | 说明 |
|------|------|
| 2026-09-06 | 初稿：确认「后台配桶 → 盘符 → 他端 Agent 插件登录 → 按盘符写入 BYOS」逻辑与现有架构同构，并记录缺口 |
