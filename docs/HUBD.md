# hubd — 本机 Runtime 挂盘

> 用户机器上的自动挂载守护进程（**BYOC / D-001**：不在平台跑）。  
> **Runtime 总览：** [RUNTIME.md](./RUNTIME.md) · 黄金路径：[GOLDEN-PATH.md](./GOLDEN-PATH.md) · Windows：[WINDOWS.md](./WINDOWS.md)

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

## 无 FUSE 也可用：sync_workspace 同步到本机目录

扩展找不到、或 macOS 不允许系统扩展时，**不要卡在 mount**。产品支持：

```bash
make demo-local
# → 真 MinIO 对象拉取到 ~/aihub-demo-workspace
# open ~/aihub-demo-workspace
```

`mode=sync_workspace`：`rclone sync` 拉/推，**不需要** FUSE/`rclone mount`。

### macOS 真 FUSE 专项（`mode=mount`）

```bash
# 1) 必须用官方 rclone（Homebrew 版会拒绝 mount）
#    脚本会自动拉到 .bin/rclone-official
# 2) macFUSE 系统扩展必须已加载，否则：
#    mount_macfuse: the file system is not available (1)

AI_CLOUDHUB_SMOKE_FUSE_REQUIRE=1 make smoke-hubd-fuse
# 软跳过（无 FUSE 时 exit 0）：
make smoke-hubd-fuse
```

**本机实测（2026-07）：**  
- brew rclone → `mount is not supported … Homebrew` ❌  
- 官方 rclone + hubd 报 `mounted`，但内核报 `file system is not available` → 挂载点仍空 ❌（扩展未真正启用）  
- `make demo-local` / sync ✅  

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

## 与 runner

Job 侧对称探测见 [RUNNER.md](./RUNNER.md)（`runner check|dry-run`，不 claim）。  
总对照表：[RUNTIME.md](./RUNTIME.md)。

## 版本

`hubd check|dry-run|once` 自 **0.2.57**。
