@echo off
REM 设置 SafeLink 开机自启动（当前用户）
REM 在启动文件夹中创建快捷方式，无需管理员权限

setlocal
cd /d "%~dp0"
set STARTUP="%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup"
set LNK=%STARTUP%\SafeLink.lnk

powershell -NoProfile -Command ^
  "$s = New-Object -ComObject WScript.Shell;" ^
  "$lnk = $s.CreateShortcut('%LNK%');" ^
  "$lnk.TargetPath = '%CD%\safelink.exe';" ^
  "$lnk.Arguments = '-config configs/safelink.yaml -no-open';" ^
  "$lnk.WorkingDirectory = '%CD%';" ^
  "$lnk.WindowStyle = 7;" ^
  "$lnk.Description = 'SafeLink SSH Tunnel Daemon';" ^
  "$lnk.Save()"

if errorlevel 1 (
  echo Failed to create startup shortcut.
  pause
  exit /b 1
)

echo Auto-start installed.
echo   Shortcut: %LNK%
echo.
echo SafeLink will auto-start on next login.
endlocal
