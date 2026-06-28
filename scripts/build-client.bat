@echo off
REM Build SafeLink Client for Windows
cd /d "%~dp0\..\client"
echo Building SafeLink Client...
echo.
echo Step 1: Install frontend dependencies
cd frontend
call npm install
echo.
echo Step 2: Build frontend
call npm run build
cd ..
echo.
echo Step 3: Build Go binary with Wails
wails build -platform windows/amd64
if errorlevel 1 (
    echo.
    echo Wails CLI build failed. Falling back to go build with Wails production tags...
    go build -tags "desktop,production" -ldflags "-w -s -H windowsgui" -o "build\bin\SafeLink.exe" .
    if errorlevel 1 (
        echo ERROR: fallback go build failed.
        pause
        exit /b 1
    )
)
echo.
echo Step 4: Bundle sing-box core if available
if exist "bin\sing-box.exe" (
    copy /Y "bin\sing-box.exe" "build\bin\sing-box.exe" >nul
    echo Bundled build\bin\sing-box.exe
) else (
    echo WARNING: client\bin\sing-box.exe not found. Proxy subscriptions require sing-box.exe next to SafeLink.exe.
)
echo.
echo Done! Output: client\build\bin\SafeLink.exe
pause
