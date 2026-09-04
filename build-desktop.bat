@echo off
chcp 65001 >nul
title SnapRank 桌面壳构建
cd /d "%~dp0"

echo [1/3] 停止已有实例...
taskkill /F /IM SnapRank-desktop.exe >nul 2>&1

echo [2/3] 构建桌面壳（WebView2 原生窗口，无控制台）...
go build -trimpath -tags desktop -ldflags "-s -w -H windowsgui" -o SnapRank-desktop.exe .
if errorlevel 1 (
  echo 构建失败！
  pause
  exit /b 1
)

echo [3/3] 启动桌面窗口...
start "" SnapRank-desktop.exe
exit
