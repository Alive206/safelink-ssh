@echo off
REM Stop a running sshtunneld instance launched with start.cmd.

setlocal
powershell -NoProfile -Command "Get-Process sshtunneld -ErrorAction SilentlyContinue | Stop-Process -Force"
echo sshtunneld stopped.
endlocal
