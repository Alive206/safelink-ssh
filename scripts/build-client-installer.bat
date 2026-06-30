@echo off
setlocal

powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0build-client-installer.ps1" %*
exit /b %ERRORLEVEL%
