# 黄金路径（Golden Path）

> **主线收口后的唯一默认演示剧本。**  
> 决策：[D-003](./DECISIONS.md) · 进度：[PROGRESS.md](./PROGRESS.md)  
> 自动化：`make smoke-golden` → `scripts/smoke-golden.sh`（无活桶）  
> 活桶扩展：`make smoke-golden-minio` → `scripts/smoke-golden-minio.sh`

## 产品一句话

```text
用户带自己的对象存储（BYOS）
  → 控制面登记 Key / Drive / Binding
  → Runtime（本机 hubd 或用户侧 runner）拿 STS + Manifest
  → Agent 只认工作区路径写文件
  → 字节进用户自己的桶（控制面不中转 body）
```

**不是：** 网盘 UI、平台代存对象、平台大规模 Runner 池（D-001）。

---

## 路径图

```text
① 注册/登录（人）
② Provider（云凭证，加密可选）
③ Drive（逻辑盘：bucket + mount_point）
④ Binding desired=mounted + Session（STS spec + Manifest）
⑤ 分支 A：Agent token（scopes + drive 白名单）→ MCP / 作业
   分支 B：hubd 本机挂载（需 rclone；本 smoke 只验 session 契约）
⑥ BYOC Job：create → claim → complete（跑在用户机器语义上）
⑦ healthz / readyz 健康
```

本仓库 **默认黄金路径不要求** 起真 MinIO 写对象；P0 契约在 **无活桶** 时仍可验 session/manifest。  
需要「真用户路径」时用下方 **Live MinIO path**；细粒度清单/snapshot 另见 `make smoke-minio` / [CLOUD-INTEGRATION.md](./CLOUD-INTEGRATION.md)。

---

## 一键回归

```bash
export CGO_ENABLED=0
make build
make smoke-golden
# 期望末行：OK golden-path …
```

等价：

```bash
./scripts/smoke-golden.sh
```

| 相关 smoke | 覆盖面 |
|------------|--------|
| `make smoke` / `smoke-p0` | Provider → Drive → Binding → Session |
| `make smoke-quickstart-agent` | Agent + MCP + Job |
| `make smoke-job` | Job 全量 ops（**已 freeze，不必当主线**） |
| `make smoke-minio` | 活 MinIO 对象清单 + snapshot（可选） |
| `make smoke-golden-minio` | 活 MinIO 黄金路径：session + inventory + BYOC job（可选） |

---

## Live MinIO path

扩展默认黄金路径：在 **真实 MinIO 桶** 上走完 register → provider → drive → binding/session → **GET objects（硬断言）** → agent → BYOC job create/claim/complete。

**活契约边界（诚实说明）：**

| 本 smoke 验什么 | 不验什么 |
|-----------------|----------|
| 控制面登记 + STS session/manifest | hubd 真 FUSE 挂载 |
| `GET /v1/drives/{id}/objects` 看到 seed 对象 | rclone 进程 / 本地 mount 点 |
| BYOC job 队列契约（create → claim → complete） | 平台 runner 池（D-001 禁止） |

`hubd` **没有** dry/check 模式可在无 FUSE 时代替挂载；启动即检查 rclone，mount 需本机 FUSE/WinFsp。真挂载人工步骤：

```bash
# 1) 先跑 smoke 或手建 provider/drive/binding，记下 API + human token
export AI_CLOUDHUB_API=http://127.0.0.1:<port>
export AI_CLOUDHUB_TOKEN=<human-jwt>
export AI_CLOUDHUB_DEVICE_ID=laptop-1
# 2) 本机已装 rclone（+ macFUSE / WinFsp / Linux FUSE）
./.bin/hubd
```

自动化（推荐）：

```bash
export CGO_ENABLED=0
make smoke-golden-minio
# 期望末行：OK golden-minio …
# 若本机无法拉起 MinIO：SKIP: … 且 exit 0
# 强制失败：AI_CLOUDHUB_SMOKE_MINIO_REQUIRE=1 make smoke-golden-minio
```

等价：

```bash
./scripts/smoke-golden-minio.sh
# 或接已有 MinIO：
MINIO_ENDPOINT=http://127.0.0.1:9000 ./scripts/smoke-golden-minio.sh
```

| 行为 | 说明 |
|------|------|
| MinIO 探测 | 优先 `MINIO_ENDPOINT` / `127.0.0.1:9000`，否则下载官方 binary 到 `.bin/minio-server` 并临时起服 |
| 写对象 | `go run ./scripts/minio-seed`（EnsureBucket + Put） |
| soft-skip | 起不了 MinIO → `SKIP: …` exit **0**（不拖垮 `smoke-all`） |
| hard require | `AI_CLOUDHUB_SMOKE_MINIO_REQUIRE=1` 时起不了则 exit **1** |

**不在 `make smoke-all` 里硬依赖**（与 `smoke-minio` 同策略：可选 live 目标）。

---

## 人工 10 分钟剧本（对照脚本）

1. `make build` 后起 API（见 [QUICKSTART-AGENT.md](./QUICKSTART-AGENT.md) §2）  
2. 注册 → Provider(minio 即可) → Drive → Binding `desired=mounted`  
3. `POST /v1/bindings/{id}/session` → 看到 `manifest.env.AI_CLOUDHUB_WORKSPACE` 与 rclone spec  
4. （可选）`POST /v1/agents` + token → `create_job` / claim / complete  
5. （可选真挂载）本机 `hubd` + rclone  

成功标准：

- Session 返回非空 workspace / remote_path  
- Job 在 **同一用户 token** 下 claim/complete（非平台池）  
- `/healthz` 含 `version`；`/readyz` 可连 store  

---

## 停机与冻结（给自己看）

| 已够用 | 不要再当「下一刀」 |
|--------|-------------------|
| P0–P3、阶段 A/B、C-v0 | Job list 第 N 个 filter |
| BYOC Job + webhook outbox | admin webhook 第 N 个按钮 |
| 本黄金路径 | 默认微服务 / 托管 embedding / 真 PCI |

再开工请先问：**是事故、安全，还是有客户场景？** 否则默认不做。

---

## 版本

Verified against binary **0.2.54** (`internal/version`). Offline path: `make smoke-golden`. Live MinIO: `make smoke-golden-minio`.
