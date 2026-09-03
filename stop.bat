@echo off
setlocal
cd /d "%~dp0"
title SnapRank 停止脚本

echo 正在停止 SnapRank...
taskkill /IM SnapRank.exe /F >nul 2>&1

timeout /t 1 /nobreak >nul

tasklist | find "SnapRank.exe" >nul 2>&1
if not errorlevel 1 (
    echo 停止失败，进程仍在运行，请手动检查。
) else (
    echo SnapRank 已停止。
)
pause
