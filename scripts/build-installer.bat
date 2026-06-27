@echo off
REM Build SafeLink installer for Windows.
REM Prerequisites: Go, Node.js, go-winres, Inno Setup 6
REM
REM Usage: build-installer.bat [version]
REM   If version omitted, uses "1.0.0"

setlocal enabledelayedexpansion
cd /d "%~dp0.."

set VERSION=%1
if "%VERSION%"=="" set VERSION=1.0.0

echo ============================================================
echo  SafeLink Installer Build - v%VERSION%
echo ============================================================
echo.

echo [1/6] Building frontend...
cd web
call npm run build
if %ERRORLEVEL% neq 0 (
    echo FAILED: frontend build
    exit /b %ERRORLEVEL%
)
cd ..

echo.
echo [2/6] Generating Windows resources...
cd cmd\tray
go-winres make
if %ERRORLEVEL% neq 0 (
    echo FAILED: go-winres for tray
    exit /b %ERRORLEVEL%
)
cd ..\safelink
go-winres make
if %ERRORLEVEL% neq 0 (
    echo FAILED: go-winres for safelink
    exit /b %ERRORLEVEL%
)
cd ..\..

echo.
echo [3/6] Building safelink.exe...
set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=0
go build -o safelink.exe -ldflags="-s -w" .\cmd\safelink
if %ERRORLEVEL% neq 0 (
    echo FAILED: safelink.exe build
    exit /b %ERRORLEVEL%
)

echo.
echo [4/6] Building safelink-tray.exe...
go build -o safelink-tray.exe -ldflags="-s -w -H=windowsgui" .\cmd\tray
if %ERRORLEVEL% neq 0 (
    echo FAILED: safelink-tray.exe build
    exit /b %ERRORLEVEL%
)

echo.
echo [5/6] Creating dist directory...
if not exist dist mkdir dist

echo.
echo [6/6] Compiling Inno Setup installer...

REM Try common Inno Setup installation paths
set ISCC=
if exist "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" (
    set "ISCC=C:\Program Files (x86)\Inno Setup 6\ISCC.exe"
) else if exist "C:\Program Files\Inno Setup 6\ISCC.exe" (
    set "ISCC=C:\Program Files\Inno Setup 6\ISCC.exe"
) else (
    where iscc >nul 2>&1
    if %ERRORLEVEL% equ 0 (
        set ISCC=iscc
    )
)

if "%ISCC%"=="" (
    echo WARNING: Inno Setup not found. Skipping installer compilation.
    echo   Install from: https://jrsoftware.org/isdl.php
    echo   Binaries built successfully:
    echo     - safelink.exe
    echo     - safelink-tray.exe
    exit /b 0
)

"%ISCC%" /DMyAppVersion=%VERSION% installer\safelink.iss
if %ERRORLEVEL% neq 0 (
    echo FAILED: Inno Setup compilation
    exit /b %ERRORLEVEL%
)

echo.
echo ============================================================
echo  BUILD SUCCESSFUL!
echo  Output: dist\SafeLink-%VERSION%-Setup.exe
echo ============================================================
endlocal
