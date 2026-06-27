@echo off
REM 双击启动 SafeLink 隧道守护程序
REM
REM 首次运行：自动创建 configs/safelink.yaml 并随机生成管理员密码
REM           凭据保存在 configs/admin_credentials.txt
REM           自动打开浏览器 http://127.0.0.1:9090
REM 后续运行：直接启动并在浏览器打开控制面板

setlocal
cd /d "%~dp0"
if not exist safelink.exe (
  echo safelink.exe not found in %CD%.
  echo Build it first:  go build -o safelink.exe .\cmd\safelink
  pause
  exit /b 1
)

REM 后台启动（无窗口），日志输出到 safelink.out.log / safelink.err.log
powershell -NoProfile -Command "Start-Process -FilePath '.\safelink.exe' -ArgumentList '-config configs/safelink.yaml' -WindowStyle Hidden -RedirectStandardOutput '.\safelink.out.log' -RedirectStandardError '.\safelink.err.log'"
echo SafeLink started.  Open http://127.0.0.1:9090 in your browser
echo Logs: %CD%\safelink.err.log
endlocal
