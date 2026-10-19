@echo off
rem Double-click in File Explorer to open the guided menu.
cd /d "%~dp0"
powershell.exe -NoProfile -File "%~dp0scripts\od2gp.ps1" menu
set "od2gp_exit=%errorlevel%"
if not "%od2gp_exit%"=="0" pause
exit /b %od2gp_exit%
