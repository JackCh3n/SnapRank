# 帧选 SnapRank

本地 AI 照片评分归档工具：批量导入照片 → 本地压缩（DD鹅/MozJPEG）→ 视觉模型多维打分 → 预览复核 → 按分数段自动归档到分类文件夹，并产出评分明细（SQLite + CSV）。

> 设计方案见 [SnapRank-设计方案.md](SnapRank-设计方案.md)（v1.2，含评审修订记录）。

## 功能特性

- **两阶段归档**：评分 → 分布预览/人工复核/手动调档 → 确认后才复制/移动（默认复制，源图安全）
- **本地加权总分**：模型只输出维度分，总分本地计算；改权重后 0 API 成本重算重分档
- **在线选模型**：从聚合平台（基元律动 tokenrhythm.studio）`/v1/models` 在线拉取视觉模型，批次粒度切换
- **DD鹅 压缩**：复用本地项目 [ddGoose-go](../ddGoose-go) 的 MozJPEG 引擎；EXIF 方向校正、缩边 ≤2048px、剥离 EXIF
- **成本护栏**：启动前费用预估、单批次/每日上限、跨会话指纹缓存（重复导入不重复计费）、mock 离线模式（0 成本）
- **断点续跑**：按内容指纹（SHA-256）跳过已完成项；损坏/不支持/解析失败单独标记，不中断流水线
- **微信风格界面**：Vue 3，亮/暗双主题，实时进度（SSE）、缩略图、分布柱状图、明细分页

## 快速开始

### 一键构建（推荐）

```bat
build.bat
```

脚本自动完成：**杀死旧进程 → 构建前端与后端 → 打开浏览器**（`http://127.0.0.1:8787`）。

首次使用请到「设置」页：
1. 填入基元律动的 **API Key**（或切换 `mock` 模式离线体验，评分演示数据、不计费）；
2. 点「测试连接」确认平台可达；
3. 确认「DD鹅 lib 目录」指向 `D:\wwwroot\wwwroot\ddGoose-go\lib`。

然后到「运行」页：输入照片目录 → 扫描（显示数量与预估费用）→ 选模型 → 「▶ 抽样试跑 10 张」或「▶ 开始评分」→ 完成后到「复核归档」查看分布、调档、执行归档。

### CLI 批量模式

```bash
SnapRank.exe run --dir D:\photos --model qwen3.7-flash --sample 10 --dry-run
SnapRank.exe run --dir D:\photos --yes          # 评分后确认，回车=复制归档
SnapRank.exe serve --port 8787                  # Web 服务模式（默认）
SnapRank.exe version
```

### Wails 桌面壳（可选）

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0
wails build        # 或 go build -tags desktop
```

## 目录说明

| 路径 | 说明 |
|---|---|
| `%LOCALAPPDATA%\SnapRank\config.yaml` | 配置（base_url / API Key / 权重 / 阈值 / 限额 / 路径） |
| `%LOCALAPPDATA%\SnapRank\snaprank.db` | SQLite：会话、明细、评分缓存、费用记录 |
| `%LOCALAPPDATA%\SnapRank\work\<session>\` | 压缩图缓存（按指纹命名，可随时删除） |
| `%LOCALAPPDATA%\SnapRank\logs\` | 按天轮转日志（保留 7 份） |
| `%USERPROFILE%\Pictures\SnapRank\<session>\` | 归档输出：`35_精选 / 34_良好 / 33_一般 / 30_待清理 / 29_待复检` + `report.csv` |

## 评分体系

0–10 分制，总分 = 四维度加权和（本地计算）：技术质量 40% · 构图 30% · 内容与情感 20% · 色彩 10%（可配置）。
分档默认阈值 9 / 7 / 5；解析失败的照片不伪造分数，归入 `29_待复检`。

## 开发

```bash
go build ./...          # 后端编译（需本地存在 ../ddGoose-go，go.mod 以 replace 引用）
go vet ./...            # 静态检查
go test ./...           # 单元测试（评分解析/归档冲突/EXIF/存储）
cd frontend && npm install && npm run build   # 前端构建，产物输出到 web/dist 并内嵌
go run ./scripts/gen_samples -out samples     # 生成 E2E 测试样片
```

## 隐私说明

使用平台评分时，压缩图（已剥离 EXIF/GPS）会上传至聚合平台；源图始终留在本地。离线体验请切换 `mock` 模式。

## 技术栈

Go 1.22 · Wails v2.12（可选桌面壳）· Vue 3 + Vite · modernc.org/sqlite（纯 Go 无 CGO）· DD鹅（ddGoose-go）MozJPEG/pngquant
