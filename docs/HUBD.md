# hubd — 本机 Runtime 挂盘

> 用户机器上的自动挂载守护进程（**BYOC / D-001**：不在平台跑）。  
> 黄金路径总览：[GOLDEN-PATH.md](./GOLDEN-PATH.md) · Windows：[WINDOWS.md](./WINDOWS.md)

## 做什么

```text
API: Binding desired=mounted
  → hubd 轮询
  → Issue Session（STS + rclone conf + Manifest）
  → rclone mount 或 sync_workspace
  → report actual=mounted|error|unmounted
```

控制面**不**中转文件 body。

---

## 前置

| 需要 | 说明 |
|------|------|
| **rclone** | 硬依赖；`hubd check` 会验 |
| FUSE / macFUSE / WinFsp | **mount** 模式；`sync_workspace` 可无 FUSE 兜底 |
| API + 人/设备 token | `AI_CLOUDHUB_API` + `AI_CLOUDHUB_TOKEN` |
| Device id | 与 Binding.`device_id` 一致 |

```bash
make build   # → .bin/hubd
.bin/hubd check
# JSON: rclone_ok, fuse_hint, winfsp_ok, warnings…
```

---

## 子命令

| 命令 | 作用 | Token |
|------|------|-------|
| `hubd` / `hubd daemon` | 常驻轮询挂盘 | 需要 |
| **`hubd check`** | 只做本机 runtime 探测（JSON stdout） | **不需要** |
| **`hubd dry-run`** | 拉 binding → Issue session → **写 conf**，**不**起 rclone | 需要 |
| **`hubd once`** | 一轮 reconcile（可能真挂盘），然后拆掉退出 | 需要 |

Env 等价：`AI_CLOUDHUB_HUBD_MODE=check|dry-run|once|daemon`

```bash
export AI_CLOUDHUB_API=http://127.0.0.1:8080
export AI_CLOUDHUB_TOKEN=…
export AI_CLOUDHUB_DEVICE_ID=laptop-1

.bin/hubd check
.bin/hubd dry-run          # 看 bindings[].conf_path / session_source
.bin/hubd                  # 真挂盘守护
```

`dry-run` 输出示例字段：`ok_count`、`bindings[].conf_path`、`session_source`、`workspace`。  
conf 写在 `$AI_CLOUDHUB_STATE/dry-run/<binding_id>/rclone.conf`。

---

## 真挂盘剧本（人工）

1. 控制面：provider → drive → binding(`device_id=laptop-1`,`desired=mounted`)  
2. `hubd check` 通过  
3. `hubd dry-run` 看到 conf + workspace  
4. `hubd` 常驻；`actual` 变为 mounted  
5. Agent 只认 `AI_CLOUDHUB_WORKSPACE` / mount_point  

可选 env：

| Env | 含义 |
|-----|------|
| `AI_CLOUDHUB_POLL` | 轮询间隔（默认 15s） |
| `AI_CLOUDHUB_STATE` | 状态目录 |
| `AI_CLOUDHUB_FORCE_REMOUNT_ON_REFRESH=1` | 刷新时整挂 remount（打开句柄更诚实） |

---

## 自动化

```bash
make smoke-hubd
# 1) hubd check
# 2) API + binding
# 3) hubd dry-run → conf 非空
```

不强制本机 FUSE 真挂（CI 往往无 FUSE）；真挂由人工 / 有 FUSE 的机器验证。

---

## 已知限制

见 [KNOWN_LIMITATIONS.md](./KNOWN_LIMITATIONS.md) Runtime 节：

- soft refresh 后**已打开** FUSE 句柄可能仍持旧凭证  
- 路径可达性探测不能覆盖所有 FUSE 假死  
- Windows mount 必须 WinFsp；否则用 `mode=sync_workspace`  

---

## 版本

`hubd check|dry-run|once` 自 **0.2.57**。
