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
echo.
echo Done! Output: client\build\bin\SafeLink.exe
pause
