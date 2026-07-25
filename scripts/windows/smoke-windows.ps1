# AI-cloudhub Windows dry-check smoke (no real mount / no object store).
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\windows\smoke-windows.ps1
#
# Exit 0 if rclone is available (WinFsp optional); 1 if rclone missing.

$ErrorActionPreference = "Continue"
$Root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
if (-not (Test-Path (Join-Path $Root "go.mod"))) {
    $Root = Split-Path $PSScriptRoot -Parent
}
Set-Location $Root

Write-Host "=== smoke-windows (dry-check) ===" -ForegroundColor Magenta
Write-Host "repo: $Root"

$fail = 0

# 1) install-deps check-only
$deps = Join-Path $Root "scripts\windows\install-deps.ps1"
if (Test-Path $deps) {
    Write-Host "-- install-deps.ps1 -CheckOnly --"
    & powershell -ExecutionPolicy Bypass -File $deps -CheckOnly
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[FAIL] rclone missing (install-deps CheckOnly)" -ForegroundColor Red
        $fail = 1
    } else {
        Write-Host "[OK] install-deps CheckOnly" -ForegroundColor Green
    }
} else {
    Write-Host "[WARN] install-deps.ps1 not found" -ForegroundColor Yellow
}

# 2) runtimeenv unit tests if go present
if (Get-Command go -ErrorAction SilentlyContinue) {
    Write-Host "-- go test ./internal/runtimeenv --"
    $env:CGO_ENABLED = "0"
    go test ./internal/runtimeenv/ -count=1
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[FAIL] runtimeenv tests" -ForegroundColor Red
        $fail = 1
    } else {
        Write-Host "[OK] runtimeenv tests" -ForegroundColor Green
    }
} else {
    Write-Host "[WARN] go not in PATH — skip unit tests" -ForegroundColor Yellow
}

# 3) hubd binary smoke (expect fatal without token)
$hubd = Join-Path $Root ".bin\hubd.exe"
if (-not (Test-Path $hubd)) {
    $hubd = Join-Path $Root "hubd.exe"
}
if (Test-Path $hubd) {
    Write-Host "-- hubd.exe no-token should exit --"
    $env:AI_CLOUDHUB_TOKEN = ""
    & $hubd 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) {
        Write-Host "[WARN] hubd exited 0 without token (unexpected)" -ForegroundColor Yellow
    } else {
        Write-Host "[OK] hubd refuses empty token" -ForegroundColor Green
    }
} else {
    Write-Host "[WARN] hubd.exe not built — skip binary check (go build -o .bin\hubd.exe .\cmd\hubd)" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "Checklist:" -ForegroundColor Cyan
Write-Host "  [ ] Admin once: install-deps.ps1 (WinFsp driver)"
Write-Host "  [ ] New terminal: rclone version"
Write-Host "  [ ] mount_point G: needs WinFsp; else mode=sync_workspace + directory path"
Write-Host "  [ ] GET /v1/runtime/check is API host — trust hubd local logs"
Write-Host "  See docs/WINDOWS.md"

if ($fail -ne 0) {
    Write-Host "RESULT: FAIL" -ForegroundColor Red
    exit 1
}
Write-Host "RESULT: PASS (dry-check)" -ForegroundColor Green
exit 0
