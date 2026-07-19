@echo off
REM AI-cloudhub Windows dependency installer launcher
REM Calls install-deps.ps1 with ExecutionPolicy Bypass

setlocal
cd /d "%~dp0"

echo AI-cloudhub: installing Windows deps (WinFsp + rclone)...
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install-deps.ps1" %*
set EXITCODE=%ERRORLEVEL%

if %EXITCODE% NEQ 0 (
  echo.
  echo [FAIL] install-deps.ps1 exited with code %EXITCODE%
  echo 请以管理员身份重新运行，或手动安装 WinFsp / rclone。
  echo See docs\WINDOWS.md
  pause
  exit /b %EXITCODE%
)

echo.
echo [OK] Done. Open a new terminal before running hubd.
pause
exit /b 0
