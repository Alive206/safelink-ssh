@echo off
REM Package a safelink release for Windows.
REM Builds the binary, bundles frontend, and zips everything.
REM
REM Usage: package-release.bat [version]
REM   If version omitted, uses "dev"

setlocal enabledelayedexpansion
set VERSION=%1
if "%VERSION%"=="" set VERSION=dev

echo === Building frontend ===
cd /d "%~dp0..\web"
call npm run build
if %ERRORLEVEL% neq 0 exit /b %ERRORLEVEL%

echo === Building Windows amd64 binary ===
cd /d "%~dp0.."
set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=0
go build -o safelink.exe -ldflags="-s -w" .\cmd\safelink
if %ERRORLEVEL% neq 0 exit /b %ERRORLEVEL%

echo === Building Linux amd64 binary (for one-click deploy) ===
set GOOS=linux
set GOARCH=amd64
set CGO_ENABLED=0
go build -o safelink-linux -ldflags="-s -w" .\cmd\safelink
if %ERRORLEVEL% neq 0 exit /b %ERRORLEVEL%

echo === Creating Windows release zip ===
set OUTFILE=safelink-%VERSION%-windows-amd64.zip
powershell -NoProfile -Command "Compress-Archive -Path safelink.exe, configs\safelink.yaml, README.md, start.cmd, stop.cmd, install-autostart.cmd -DestinationPath '%OUTFILE%' -Force"

echo === Creating Linux binary package ===
set OUTFILE2=safelink-%VERSION%-linux-amd64.tar.gz
powershell -NoProfile -Command "tar -czf '%OUTFILE2%' safelink-linux"

del safelink-linux

echo === Done ===
echo   Windows: %OUTFILE%
echo   Linux:   %OUTFILE2%
echo.
echo The Linux binary is used by the one-click deploy feature
echo to upload to your VPS. Keep safelink-linux files for future
echo deployments, or delete them to save space.
