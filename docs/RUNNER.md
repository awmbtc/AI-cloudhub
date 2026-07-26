# runner — BYOC 云端 / 本机 Job Runtime

> 跑在**用户机器**上（D-001：禁止平台 Runner 大池）。  
> **Runtime 总览：** [RUNTIME.md](./RUNTIME.md) · 对称探测：[HUBD.md](./HUBD.md) `check` / `dry-run`

## 模式

| 模式 | 触发 | 作用 |
|------|------|------|
| **check** | `runner check` | 本机 rclone/FUSE JSON；**无需 token** |
| **dry-run** | `runner dry-run` | 列出 **pending jobs**（不 claim）；若设 `DRIVE_ID`/`BINDING_ID` 则写 session conf |
| **worker** | `AI_CLOUDHUB_WORKER=1` 或 `runner worker` | 轮询 claim → 跑 → complete |
| **materialize** | `MATERIALIZE_ONLY=1` 或 `runner materialize` | 仅 connector 物化（git/pg/mysql env） |
| **one-shot** | `DRIVE_ID` / `BINDING_ID` + 可选 `-- cmd` | 挂/同步工作区后执行命令 |

Env 覆盖：`AI_CLOUDHUB_RUNNER_MODE=check|dry-run|worker|materialize|run`

---

## check

```bash
make build
.bin/runner check
# { "component":"runner", "rclone_ok":…, "byoc_note":"…", "jail_default":… }
# exit 1 if rclone missing（与 hubd check 一致）
```

---

## dry-run（不抢 job）

```bash
export AI_CLOUDHUB_API=http://127.0.0.1:8080
export AI_CLOUDHUB_TOKEN=…   # human or agent with job.run
.bin/runner dry-run
# pending_jobs[], pending_count — 不 claim

# 可选：预写 session conf（仍不执行 command）
export AI_CLOUDHUB_DRIVE_ID=…
export AI_CLOUDHUB_STATE=/tmp/runner-state
.bin/runner dry-run
# conf_path + session.source / workspace
```

**不会** `POST /jobs/next/claim`，避免污染队列。

---

## worker（真跑）

```bash
export AI_CLOUDHUB_WORKER=1
export AI_CLOUDHUB_API=… AI_CLOUDHUB_TOKEN=…
export AI_CLOUDHUB_RUNNER_ID=my-box   # 可选 claim 归因
export AI_CLOUDHUB_REGION=cn-hz      # 可选 region 过滤
.bin/runner
# 或: .bin/runner worker
```

---

## 自动化

```bash
make smoke-runner
# 1) runner check JSON
# 2) API + agent job pending
# 3) dry-run 看到 pending_count>=1 且未 claim
# 4) 可选 DRIVE_ID session conf
```

---

## 与 hubd 对照

| | hubd | runner |
|--|------|--------|
| 主责 | Binding 挂盘守护 | Job / one-shot 工作区 |
| check | 本机 FUSE 探测 | 同 runtimeenv + BYOC 标注 |
| dry-run | session conf，不 mount | pending jobs + 可选 conf，不 claim |
| 算力归属 | 用户本机 | 用户本机/云主机（BYOC） |

---

## 版本

`runner check|dry-run` 自 **0.2.58**。
