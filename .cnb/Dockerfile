# SnapRank 流水线专用构建环境：Go 1.22 + Node 18（前端 Vite 构建）+ zip 打包。
# 在官方 Go 镜像基础上预装依赖，避免每次流水线执行时重复 apt-get（CNB 最佳实践）。
FROM golang:1.22
RUN apt-get update \
    && apt-get install -y --no-install-recommends zip unzip nodejs npm git \
    && rm -rf /var/lib/apt/lists/*
