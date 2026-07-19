# Windows 本机 Runtime 指南

`hubd` 在 Windows 上通过 **rclone + WinFsp** 实现逻辑盘挂载（类似 Linux FUSE）。

| 组件 | 是否必需 | 说明 |
|------|----------|------|
| **rclone** | **硬依赖** | 缺失时 `hubd` 直接退出 |
| **WinFsp** | mount 模式需要 | 缺失时警告，不强制退出；可用 `mode=sync_workspace` |

## 一键安装依赖

在仓库根目录（或任意路径，指向脚本即可）：

```bat
scripts\windows\install-deps.bat
```

或 PowerShell（**建议管理员**，WinFsp 驱动需要）：

```powershell
powershell -ExecutionPolicy Bypass -File scripts\windows\install-deps.ps1
```

脚本行为：

1. 检测是否已安装 WinFsp / rclone  
2. **优先 winget**：`WinFsp.WinFsp`、`Rclone.Rclone`  
3. 无 winget 时：从 GitHub / rclone.org 下载安装包  
4. 中英双语成功/失败提示  

安装后请**重新打开终端**，再运行 `hubd`。

### 手动安装

- WinFsp：https://winfsp.dev/rel/  
- rclone：https://rclone.org/downloads/ （将 `rclone.exe` 加入 PATH）

## 运行 hubd

```powershell
# 编译（在 Go 环境中）
$env:CGO_ENABLED = "0"
go build -o .bin\hubd.exe .\cmd\hubd
go build -o .bin\api.exe .\cmd\api

# 先启动 API（另一终端）
.\.bin\api.exe

# Runtime
$env:AI_CLOUDHUB_API = "http://127.0.0.1:8080"
$env:AI_CLOUDHUB_TOKEN = "<bearer>"
$env:AI_CLOUDHUB_DEVICE_ID = "laptop-win-1"
.\.bin\hubd.exe
```

### 缺组件时的行为

| 情况 | hubd 行为 |
|------|-----------|
| 无 rclone | **Fatal**，打印 `scripts\windows\install-deps.ps1` 说明 |
| 有 rclone、无 WinFsp | **警告**后继续；`rclone mount` 可能失败；`sync_workspace` 仍可用 |
| 均已安装 | 正常 mount |

API 也可查询本机检查结果：

```text
GET /v1/runtime/check
```

字段含 `rclone_ok`、`winfsp_ok`、`install_hint`、`warnings`。

## 常见问题

**Q: winget 找不到包？**  
A: 升级 App Installer，或直接跑脚本的下载分支；或手动装 WinFsp MSI。

**Q: 安装了 WinFsp 仍提示未检测到？**  
A: 重启终端/系统；确认 `C:\Program Files (x86)\WinFsp\bin\` 下存在 `winfsp-x64.dll` 或 `launcher-x64.exe`。

**Q: 不想装 WinFsp？**  
A: Binding / session 使用 `mode=sync_workspace`（rclone sync 而非 mount），不依赖 FUSE。

**Q: 杀软拦截挂载？**  
A: 将工作目录与 rclone 加入白名单；见 `docs/RISK-COST.md` FUSE/WinFsp 风险说明。

## 相关

- 安装脚本：`scripts/windows/install-deps.ps1`  
- 运行时检查：`internal/runtimeenv/check.go`  
- 架构：`docs/ARCHITECTURE.md`
