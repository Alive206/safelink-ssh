@echo off
REM Remove the auto-start shortcut created by install-autostart.cmd.

setlocal
set "LNK=%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\sshtunneld.lnk"
if exist "%LNK%" (
  del "%LNK%"
  echo Removed %LNK%
) else (
  echo No auto-start shortcut found at %LNK%
)
endlocal
