@echo off
REM Double-click launcher for sshtunneld.
REM
REM First run: creates configs\sshtunneld.yaml with a random admin password,
REM            saves credentials to configs\admin_credentials.txt, and opens
REM            the browser at http://127.0.0.1:8080.
REM Later runs: just starts the daemon and re-opens the UI.

setlocal
cd /d "%~dp0"
if not exist sshtunneld.exe (
  echo sshtunneld.exe not found in %CD%.
  echo Build it first:  go build -o sshtunneld.exe .\cmd\sshtunneld
  pause
  exit /b 1
)

REM Run hidden — no console window.  PowerShell launches it detached so this
REM .cmd returns immediately.
powershell -NoProfile -Command "Start-Process -FilePath '.\sshtunneld.exe' -WindowStyle Hidden -RedirectStandardOutput '.\sshtunneld.out.log' -RedirectStandardError '.\sshtunneld.err.log'"
echo sshtunneld started.  UI should open momentarily at http://127.0.0.1:8080
echo Logs: %CD%\sshtunneld.err.log
endlocal
