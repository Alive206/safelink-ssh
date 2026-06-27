@echo off
REM 停止正在运行的 SafeLink 守护进程

setlocal
powershell -NoProfile -Command "Get-Process safelink -ErrorAction SilentlyContinue | Stop-Process -Force"
echo SafeLink stopped.
endlocal
