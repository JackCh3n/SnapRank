# 帧选 SnapRank

本地 AI 照片评分归档工具：批量导入照片 → 本地压缩（DD鹅/MozJPEG）→ 视觉模型多维打分 → 预览复核 → 按分数段自动归档到分类文件夹，并产出评分明细（SQLite + CSV）。

> 设计方案见 [SnapRank-设计方案.md](SnapRank-设计方案.md)（v1.2，含评审修订记录）。

## 功能特性

- **两阶段归档**：评分 → 分布预览/人工复核/手动调档 → 确认后才复制/移动（默认复制，源图安全）
- **本地加权总分**：模型只输出维度分，总分本地计算；改权重后 0 API 成本重算重分档
- **在线选模型**：从聚合平台（基元律动 tokenrhythm.studio）`/v1/models` 在线拉取视觉模型，批次粒度切换，兼容 Anthropic Messages 协议
- **DD鹅 压缩**：复用本地项目 [ddGoose-go](https://github.com/JackCh3n/ddGoose-go) 的 MozJPEG 引擎；EXIF 方向校正、缩边 ≤2048px、剥离 EXIF
- **成本护栏**：启动前费用预估、单批次/每日上限、跨会话指纹缓存（重复导入不重复计费）、mock 离线模式（0 成本）
- **断点续跑 + 失败自动重试**：按内容指纹（SHA-256）跳过已完成项；失败照片自动排到队尾重试（间隔 15s，安全阀防永久失败烧钱），可整夜挂机直到全部评分成功
- **任务管理**：多任务队列、暂停/继续当前任务、移出排队任务、图片级队列视图（成功/待重试/强制评分/缓存命中标记）
- **评分历史**：每张照片的历次评分记录（跨批次按内容指纹聚合）可点击切换查看详情
- **图库管理**：跨批次去重视图、分数段/文件名/只看已选筛选、批量删除（联动 RAW 伴生文件，记录保留）、照片弹窗快捷删除
- **系统通知 + 数据保险**：任务完成弹 Windows 通知；数据库每日自动备份（保留 7 份）；评分明细一键导出 CSV
- **微信风格界面**：Vue 3，亮/暗双主题，实时进度（SSE）、缩略图长短缓存、分布柱状图、明细分页排序

## 快速开始

### 桌面壳（推荐，双击即用）

```bat
build-desktop.bat
```

一键构建原生 WebView2 窗口程序 `SnapRank-desktop.exe` 并启动（无控制台）。窗口内就是完整界面；再次双击只会新开窗口指向已运行实例（单实例多窗口，共用数据）。

也可以直接下载 CI Release 的 `SnapRank-windows-amd64.zip`：解压后运行 `build.bat`（构建并启动 serve 形态，双击 exe 带控制台看日志），或按下方「开发」一节本地构建桌面壳 exe。

> 桌面壳（`SnapRank-desktop.exe`）暂未随 CI 发布，属本地构建形态；如需进 Release 产物可加 CI 步骤。

### 服务模式（带控制台，便于看日志）

```bat
build.bat
```

脚本自动完成：**杀死旧进程 → 构建前端与后端 → 打开浏览器**（`http://127.0.0.1:8787`）。

首次使用请到「设置」页：
1. 填入基元律动的 **API Key**（或切换 `mock` 模式离线体验，评分演示数据、不计费）；
2. 点「测试连接」确认平台可达；
3. 确认「DD鹅 lib 目录」指向 `lib` 目录（随包分发，或本地 `ddGoose-go\lib`）。

然后到「运行」页：输入照片目录 → 扫描（显示数量与预估费用）→ 选模型 → 勾选「🔁 失败自动重试」→ 「▶ 抽样试跑 10 张」或「▶ 开始评分」→ 完成后到「复核归档」查看分布、调档、执行归档。

### CLI 批量模式

```bash
SnapRank.exe run --dir D:\photos --model qwen3.7-flash --sample 10 --dry-run
SnapRank.exe run --dir D:\photos --yes          # 评分后确认，回车=复制归档
SnapRank.exe serve --port 8787                  # Web 服务模式（默认）
SnapRank.exe version
```

## 目录说明（便携）

所有数据存放在 **exe 同级 `data\` 目录**（便携，随程序目录走，重装/换机直接拷整个目录）：

| 路径 | 说明 |
|---|---|
| `data\config.yaml` | 配置（base_url / API Key / 权重 / 阈值 / 限额 / 并发 / 路径） |
| `data\snaprank.db` | SQLite：会话、明细、评分历史、评分缓存、费用记录 |
| `data\backups\` | 数据库自动备份（每日一份，保留最近 7 份；设置页可手动备份） |
| `data\work\compressed\` | 压缩图共享缓存（按内容指纹命名，可随时删除） |
| `data\logs\` | 按天轮转日志 |
| `data\imports\` | 粘贴/拖入照片的自动导入目录 |
| 归档输出 | `Pictures\SnapRank\<session>\`：`35_精选 / 34_良好 / 33_一般 / 30_待清理 / 29_待复检` + `report.csv`（归档模式可配置） |

## 评分体系

0–10 分制，总分 = 四维度加权和（本地计算）：技术质量 40% · 构图 30% · 内容与情感 20% · 色彩 10%（可配置）。
分档默认阈值 9 / 7 / 5；解析失败的照片不伪造分数，归入 `29_待复检`。
评分并发默认 1（串行最稳），可在设置页调至 30（过高易触发平台限流）。

## 开发

```bash
go build ./...                        # 后端编译（需本地存在 ../ddGoose-go，go.mod 以 replace 引用）
go build -tags desktop ...            # 桌面壳形态（main_wails.go，WebView2 窗口 + 本地 HTTP）
go vet ./...                          # 静态检查
go test ./...                         # 单元测试（评分解析/归档冲突/EXIF/存储）
cd frontend && npm install && npm run build   # 前端构建，产物输出到 web/dist 并内嵌
go run ./scripts/gen_samples -out samples     # 生成 E2E 测试样片
```

CI（GitHub Actions + CNB 双流水线）推送 main 自动构建 Windows amd64 并发布 Release。

## 隐私说明

使用平台评分时，压缩图（已剥离 EXIF/GPS）会上传至聚合平台；源图始终留在本地。离线体验请切换 `mock` 模式。

## 技术栈

Go 1.22 · WebView2（jchv/go-webview2，可选桌面壳）· Vue 3 + Vite · modernc.org/sqlite（纯 Go 无 CGO）· DD鹅（ddGoose-go）MozJPEG/pngquant