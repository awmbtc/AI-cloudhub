# AI-cloudhub Windows dependency installer
# Installs WinFsp (FUSE layer) and rclone for hubd mount mode.
# Prefer winget; fall back to direct download.
#
# Usage (PowerShell as Admin recommended for WinFsp):
#   powershell -ExecutionPolicy Bypass -File scripts\windows\install-deps.ps1
# Or double-click install-deps.bat

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

function Write-Info([string]$zh, [string]$en) {
    Write-Host "[INFO] $zh / $en" -ForegroundColor Cyan
}
function Write-Ok([string]$zh, [string]$en) {
    Write-Host "[OK]   $zh / $en" -ForegroundColor Green
}
function Write-Warn([string]$zh, [string]$en) {
    Write-Host "[WARN] $zh / $en" -ForegroundColor Yellow
}
function Write-Fail([string]$zh, [string]$en) {
    Write-Host "[FAIL] $zh / $en" -ForegroundColor Red
}

function Test-IsAdmin {
    try {
        $id = [Security.Principal.WindowsIdentity]::GetCurrent()
        $p = New-Object Security.Principal.WindowsPrincipal($id)
        return $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    } catch {
        return $false
    }
}

function Test-Winget {
    return $null -ne (Get-Command winget -ErrorAction SilentlyContinue)
}

function Test-WinFspInstalled {
    $candidates = @(
        "${env:ProgramFiles(x86)}\WinFsp\bin\winfsp-x64.dll",
        "${env:ProgramFiles}\WinFsp\bin\winfsp-x64.dll",
        "${env:ProgramFiles(x86)}\WinFsp\bin\launcher-x64.exe",
        "${env:ProgramFiles}\WinFsp\bin\launcher-x64.exe",
        "${env:ProgramFiles(x86)}\WinFsp\bin\winfsp-x86.dll",
        "${env:ProgramFiles}\WinFsp\bin\winfsp-x86.dll"
    )
    foreach ($c in $candidates) {
        if (Test-Path -LiteralPath $c) { return $true }
    }
    # Registry (optional)
    $regPaths = @(
        "HKLM:\SOFTWARE\WinFsp",
        "HKLM:\SOFTWARE\WOW6432Node\WinFsp",
        "HKLM:\SYSTEM\CurrentControlSet\Services\WinFsp.Launcher"
    )
    foreach ($rp in $regPaths) {
        if (Test-Path -LiteralPath $rp) { return $true }
    }
    return $false
}

function Test-RcloneInstalled {
    $cmd = Get-Command rclone -ErrorAction SilentlyContinue
    if ($cmd) { return $true }
    $candidates = @(
        "$env:LOCALAPPDATA\Microsoft\WinGet\Links\rclone.exe",
        "$env:ProgramFiles\rclone\rclone.exe",
        "$env:LOCALAPPDATA\rclone\rclone.exe"
    )
    foreach ($c in $candidates) {
        if (Test-Path -LiteralPath $c) { return $true }
    }
    return $false
}

function Install-WinFsp {
    Write-Info "正在安装 WinFsp..." "Installing WinFsp..."

    if (-not (Test-IsAdmin)) {
        Write-Warn "建议以管理员身份运行以安装 WinFsp 驱动" "Administrator rights recommended for WinFsp driver install"
    }

    if (Test-Winget) {
        Write-Info "使用 winget 安装 WinFsp" "Using winget to install WinFsp"
        try {
            & winget install --id WinFsp.WinFsp -e --accept-package-agreements --accept-source-agreements
            if ($LASTEXITCODE -eq 0 -or (Test-WinFspInstalled)) {
                Write-Ok "WinFsp 已通过 winget 安装" "WinFsp installed via winget"
                return $true
            }
            Write-Warn "winget 退出码 $LASTEXITCODE，尝试直接下载" "winget exit $LASTEXITCODE, trying direct download"
        } catch {
            Write-Warn "winget 安装 WinFsp 失败: $_" "winget WinFsp install failed: $_"
        }
    }

    # Direct download from a well-known recent GitHub release
    $msiUrl = "https://github.com/winfsp/winfsp/releases/download/v2.0/winfsp-2.0.23075.msi"
    $tmp = Join-Path $env:TEMP "winfsp-install.msi"
    try {
        Write-Info "从 GitHub 下载 WinFsp MSI" "Downloading WinFsp MSI from GitHub"
        Invoke-WebRequest -Uri $msiUrl -OutFile $tmp -UseBasicParsing
        Write-Info "运行 MSI 安装（静默）" "Running MSI installer (quiet)"
        $args = "/i `"$tmp`" /qn /norestart"
        $p = Start-Process -FilePath "msiexec.exe" -ArgumentList $args -Wait -PassThru
        if ($p.ExitCode -eq 0 -or $p.ExitCode -eq 3010) {
            Write-Ok "WinFsp MSI 安装完成 (exit=$($p.ExitCode))" "WinFsp MSI install finished (exit=$($p.ExitCode))"
            return $true
        }
        Write-Fail "WinFsp MSI 安装失败 exit=$($p.ExitCode)" "WinFsp MSI install failed exit=$($p.ExitCode)"
        Write-Info "请手动安装: https://winfsp.dev/rel/" "Manual install: https://winfsp.dev/rel/"
        return $false
    } catch {
        Write-Fail "下载/安装 WinFsp 失败: $_" "Download/install WinFsp failed: $_"
        Write-Info "请手动安装: https://winfsp.dev/rel/" "Manual install: https://winfsp.dev/rel/"
        return $false
    } finally {
        if (Test-Path -LiteralPath $tmp) { Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue }
    }
}

function Install-Rclone {
    Write-Info "正在安装 rclone..." "Installing rclone..."

    if (Test-Winget) {
        Write-Info "使用 winget 安装 rclone" "Using winget to install rclone"
        try {
            & winget install --id Rclone.Rclone -e --accept-package-agreements --accept-source-agreements
            if ($LASTEXITCODE -eq 0 -or (Test-RcloneInstalled)) {
                Write-Ok "rclone 已通过 winget 安装" "rclone installed via winget"
                return $true
            }
            Write-Warn "winget 退出码 $LASTEXITCODE，尝试直接下载" "winget exit $LASTEXITCODE, trying direct download"
        } catch {
            Write-Warn "winget 安装 rclone 失败: $_" "winget rclone install failed: $_"
        }
    }

    # Direct zip from rclone.org (stable)
    $zipUrl = "https://downloads.rclone.org/rclone-current-windows-amd64.zip"
    $tmpZip = Join-Path $env:TEMP "rclone-windows.zip"
    $destDir = Join-Path $env:LOCALAPPDATA "rclone"
    try {
        Write-Info "从 rclone.org 下载 zip" "Downloading rclone zip from rclone.org"
        Invoke-WebRequest -Uri $zipUrl -OutFile $tmpZip -UseBasicParsing
        if (-not (Test-Path -LiteralPath $destDir)) {
            New-Item -ItemType Directory -Path $destDir -Force | Out-Null
        }
        $extract = Join-Path $env:TEMP "rclone-extract"
        if (Test-Path -LiteralPath $extract) { Remove-Item -LiteralPath $extract -Recurse -Force }
        Expand-Archive -Path $tmpZip -DestinationPath $extract -Force
        $exe = Get-ChildItem -Path $extract -Filter "rclone.exe" -Recurse | Select-Object -First 1
        if (-not $exe) {
            Write-Fail "zip 中未找到 rclone.exe" "rclone.exe not found in zip"
            return $false
        }
        Copy-Item -LiteralPath $exe.FullName -Destination (Join-Path $destDir "rclone.exe") -Force
        # Ensure user PATH includes destDir for this session and permanently for user
        $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
        if ($userPath -notlike "*$destDir*") {
            [Environment]::SetEnvironmentVariable("Path", "$userPath;$destDir", "User")
            Write-Info "已将 $destDir 加入用户 PATH（新终端生效）" "Added $destDir to user PATH (new shells)"
        }
        $env:Path = "$env:Path;$destDir"
        Write-Ok "rclone 已安装到 $destDir" "rclone installed to $destDir"
        return $true
    } catch {
        Write-Fail "下载/安装 rclone 失败: $_" "Download/install rclone failed: $_"
        Write-Info "请手动安装: https://rclone.org/downloads/" "Manual install: https://rclone.org/downloads/"
        return $false
    } finally {
        if (Test-Path -LiteralPath $tmpZip) { Remove-Item -LiteralPath $tmpZip -Force -ErrorAction SilentlyContinue }
    }
}

# ---- main ----
Write-Host ""
Write-Host "=== AI-cloudhub Windows 依赖安装 / Dependency Installer ===" -ForegroundColor Magenta
Write-Host ""

$isAdmin = Test-IsAdmin
if ($isAdmin) {
    Write-Ok "已以管理员身份运行" "Running as Administrator"
} else {
    Write-Warn "当前非管理员；WinFsp 驱动安装可能失败" "Not elevated; WinFsp driver install may fail"
}

$winfspOk = $false
$rcloneOk = $false

if (Test-WinFspInstalled) {
    Write-Ok "已检测到 WinFsp" "WinFsp already installed"
    $winfspOk = $true
} else {
    Write-Info "未检测到 WinFsp" "WinFsp not detected"
    $winfspOk = Install-WinFsp
    if (-not $winfspOk) {
        # re-check after install
        $winfspOk = Test-WinFspInstalled
    }
}

if (Test-RcloneInstalled) {
    Write-Ok "已检测到 rclone" "rclone already installed"
    $rcloneOk = $true
} else {
    Write-Info "未检测到 rclone" "rclone not detected"
    $rcloneOk = Install-Rclone
    if (-not $rcloneOk) {
        $rcloneOk = Test-RcloneInstalled
    }
}

Write-Host ""
Write-Host "=== 结果 / Summary ===" -ForegroundColor Magenta
if ($winfspOk) {
    Write-Ok "WinFsp: 就绪（FUSE 挂载可用）" "WinFsp: ready (mount mode available)"
} else {
    Write-Fail "WinFsp: 未就绪 — mount 可能失败，仍可用 mode=sync_workspace" "WinFsp: missing — mount may fail; mode=sync_workspace still works"
}
if ($rcloneOk) {
    Write-Ok "rclone: 就绪（hubd 硬依赖）" "rclone: ready (hard requirement for hubd)"
} else {
    Write-Fail "rclone: 未就绪 — hubd 无法启动挂载" "rclone: missing — hubd cannot mount"
}

if ($rcloneOk -and $winfspOk) {
    Write-Host ""
    Write-Ok "全部依赖已就绪。请重新打开终端后运行 hubd。" "All deps ready. Open a new terminal, then run hubd."
    exit 0
} elseif ($rcloneOk) {
    Write-Host ""
    Write-Warn "rclone 已就绪；WinFsp 缺失时仅 mount 模式受限。" "rclone OK; without WinFsp only mount mode is limited."
    Write-Info "安装脚本路径: scripts\windows\install-deps.ps1" "Installer: scripts\windows\install-deps.ps1"
    exit 0
} else {
    Write-Host ""
    Write-Fail "安装未完成。请查看上方错误并重试（建议管理员 PowerShell）。" "Install incomplete. Review errors above and retry (Admin PowerShell recommended)."
    exit 1
}
