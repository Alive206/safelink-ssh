@echo off
REM Register sshtunneld to auto-start when the current user logs in.
REM Implementation: drop a .lnk shortcut into the per-user Startup folder
REM that points at start.cmd in this directory.  No admin rights required,
REM nothing in HKLM, and easy to inspect (Win+R -> shell:startup).

setlocal
set "ROOT=%~dp0"
set "TARGET=%ROOT%start.cmd"
if not exist "%TARGET%" (
  echo Cannot find %TARGET%.
  pause
  exit /b 1
)

set "STARTUP=%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup"
set "LNK=%STARTUP%\sshtunneld.lnk"

powershell -NoProfile -Command ^
  "$s = New-Object -ComObject WScript.Shell;" ^
  "$lnk = $s.CreateShortcut('%LNK%');" ^
  "$lnk.TargetPath = '%TARGET%';" ^
  "$lnk.WorkingDirectory = '%ROOT%';" ^
  "$lnk.WindowStyle = 7;" ^
  "$lnk.Description = 'sshtunneld auto-start';" ^
  "$lnk.Save()"

if errorlevel 1 (
  echo Failed to create startup shortcut.
  pause
  exit /b 1
)

echo Auto-start installed.
echo   Shortcut: %LNK%
echo   Will run: %TARGET%
echo.
echo Sign out and back in to verify, or run start.cmd now.
endlocal
