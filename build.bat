@echo off
chcp 65001 >nul
setlocal
cd /d "%~dp0"
title SnapRank 帧选 构建脚本

echo ============================================
echo  帧选 SnapRank 一键构建
echo  步骤：杀旧进程 → 构建前端/后端 → 打开浏览器
echo ============================================

echo [1/4] 停止旧的 SnapRank 进程...
taskkill /IM SnapRank.exe /F >nul 2>&1
timeout /t 1 /nobreak >nul

echo [2/4] 构建前端（Vite）...
pushd frontend
call npm run build
if errorlevel 1 (
    popd
    echo 前端构建失败！
    pause
    exit /b 1
)
popd

echo [3/4] 编译后端（Go，内嵌前端）...
go build -ldflags "-s -w" -o SnapRank.exe .
if errorlevel 1 (
    echo 后端编译失败！
    pause
    exit /b 1
)

echo [4/4] 启动服务并打开浏览器...
start "" http://127.0.0.1:8787
SnapRank.exe serve --port 8787 --no-open

endlocal
