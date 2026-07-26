# Runtime 总览（hubd + runner）

> 控制面之外、跑在**用户机器**上的两件 Runtime。  
> **D-001：** 不是平台算力池。  
> 主线演示：[GOLDEN-PATH.md](./GOLDEN-PATH.md) · 上线：[CUTOVER.md](./CUTOVER.md)

## 一张图

```text
                    ┌─────────────────────┐
                    │  Control plane API  │
                    │  auth / drives /    │
                    │  bindings / jobs /  │
                    │  STS session        │
                    └──────────┬──────────┘
           desired=mounted     │     BYOC jobs
               ┌───────────────┴───────────────┐
               ▼                               ▼
        ┌─────────────┐                 ┌─────────────┐
        │    hubd     │                 │   runner    │
        │  本机挂盘    │                 │ Job / 一次性 │
        │  守护进程    │                 │  工作区执行  │
        └──────┬──────┘                 └──────┬──────┘
               │ rclone mount/sync             │ session + cmd
               ▼                               ▼
        用户本机路径                      用户本机/云主机
        AI_CLOUDHUB_WORKSPACE            (同一 Manifest 契约)
```

两者共享：**Session + rclone conf + Manifest + path jail**。  
控制面**不**中转对象 body。

---

## 怎么选

| 场景 | 用谁 |
|------|------|
| 笔记本长期挂逻辑盘给 IDE/Agent | **hubd** |
| 队列里跑 `command[]`（CI/云主机 BYOC） | **runner worker** |
| 只物化 git/DB 连接器 | **runner materialize** |
| 只想确认本机能不能挂 | **`hubd check` / `runner check`** |
| 只想看 session conf / 队列，不真挂不真跑 | **`dry-run`** |

详文：

- [HUBD.md](./HUBD.md) — 子命令、真挂盘、soft refresh  
- [RUNNER.md](./RUNNER.md) — worker / materialize / 不 claim 的 dry-run  
- [STS.md](./STS.md) · [STS-RUNBOOK.md](./STS-RUNBOOK.md) — session.source  
- [WINDOWS.md](./WINDOWS.md) — WinFsp / rclone  
- [SECCOMP.md](./SECCOMP.md) — Linux 可选沙箱  

---

## 对称命令（0.2.57+）

| | hubd | runner |
|--|------|--------|
| **check** | 本机 rclone/FUSE，无 token | 同 + BYOC 标注 |
| **dry-run** | Issue session，写 conf，**不 mount** | 列 pending jobs，**不 claim**；可选 conf |
| **真跑** | 守护轮询 binding | worker claim→run→complete 或 one-shot |

```bash
make build
.bin/hubd check
.bin/runner check

# 有 API + token 时：
export AI_CLOUDHUB_API=… AI_CLOUDHUB_TOKEN=…
.bin/hubd dry-run
.bin/runner dry-run
```

---

## 共用环境变量（摘要）

| Env | hubd | runner | 含义 |
|-----|------|--------|------|
| `AI_CLOUDHUB_API` | ✅ | ✅ | 控制面 |
| `AI_CLOUDHUB_TOKEN` | ✅（check 除外） | ✅（check 除外） | Bearer |
| `AI_CLOUDHUB_DEVICE_ID` | ✅ binding 匹配 | session 可选 | 设备 id |
| `AI_CLOUDHUB_STATE` | conf/state 目录 | dry-run conf | 本地状态 |
| `AI_CLOUDHUB_MOUNT` | — | 默认 `/workspace` | 工作区路径 |
| `AI_CLOUDHUB_DRIVE_ID` / `BINDING_ID` | 经 binding | one-shot / dry-run conf | 盘/绑定 |
| `AI_CLOUDHUB_WORKER` | — | worker 模式 | 领 job |
| `AI_CLOUDHUB_RUNNER_ID` / `REGION` | — | claim 归因/过滤 | 多 runner |
| `AI_CLOUDHUB_FORCE_REMOUNT_ON_REFRESH` | soft→整挂 | — | 刷新策略 |
| `AI_CLOUDHUB_JAIL` | — | 路径 jail 默认开 | sandbox |

完整列表见各专文与 `.env.example`（若有）。

---

## 推荐验收顺序

```bash
export CGO_ENABLED=0
make test
make smoke-golden          # 控制面契约（无活桶）
make smoke-hubd            # hubd check + dry-run
make smoke-runner          # runner check + dry-run（不 claim）
make smoke-runtime         # = smoke-hubd + smoke-runner
# 可选：
make smoke-golden-minio    # 活桶 objects
make smoke-sts / smoke-sts-live
make prod-preflight
```

人工真挂盘：装 rclone + FUSE → `hubd` 常驻（见 HUBD）。  
人工真 job：`AI_CLOUDHUB_WORKER=1 .bin/runner`（见 RUNNER）。

---

## 明确不做

| 禁止 | 原因 |
|------|------|
| 平台大规模 Runner 池 | D-001 |
| 控制面代理上传下载 body | BYOS 成本与安全 |
| 把 hubd/runner 画成「我们托管的云 IDE 算力」 | 产品是数据平面，不是算力云 |

---

## 版本

本文汇总自 **0.2.59**（hubd 0.2.57 · runner 0.2.58 能力之上）。
