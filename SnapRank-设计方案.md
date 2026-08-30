# 帧选 SnapRank — 本地 AI 照片评分归档系统 设计方案

> 方案版本：v1.2
> 日期：2026-08-30
> 状态：已评审修订
> v1.2 变更（评审修复）：
> 1. **总分改为本地计算**——模型只输出四个维度分，总分按当前权重在本地加权，权重调整后可零成本重算重分档；
> 2. **模型切换以批次为粒度**，同批次锁定一个模型保证分数可比，跨模型分数不可直接比较；
> 3. **解析失败不再伪造 5.0 分**，归入独立"待复检"目录，不污染正常分档；
> 4. **归档改两阶段**：评分 → 预览分布 → 用户确认后执行；默认"复制"，"移动"需显式选择；
> 5. **文件名冲突与跨卷移动策略**明确（重命名后缀 / copy+delete 回退）；
> 6. **压缩环节应用 EXIF 方向并剥离 EXIF**；
> 7. **运行期数据全部放用户目录**（`%LOCALAPPDATA%\SnapRank`），归档输出路径可配置，不再写安装目录；
> 8. **视觉模型过滤**改为"内置白名单 + 正则规则 + 手动兜底"，不依赖 `/v1/models` 返回模态元数据；
> 9. **HEIC/RAW 明确不支持**（列为扩展项），会跳过并记录原因；
> 10. **session 语义、跨会话指纹缓存、SQLite 表结构、文件指纹算法**补充完整；
> 11. **成本护栏具体化**：启动前费用预估、批次/每日上限、抽样试跑；
> 12. **人工复核**：手动调档、本地重算重分档；
> 13. **绑定接口补全**（配置读写、明细分页、缩略图、SSE 事件）；
> 14. **隐私声明**：上传第三方平台的明示与离线 Mock 模式；
> 15. v1.1 遗留：主力平台基元律动、Go + Wails v2.12、微信风格双主题 UI 维持不变。
> v1.2 新增决策：
> - **图片压缩采用本地项目 DD鹅（`D:\wwwroot\wwwroot\ddGoose-go`）**：以 Go module `replace` 方式导入其 `ui` 压缩包（MozJPEG/pngquant/gifsicle 引擎），缩放与 EXIF 处理由 SnapRank 完成；
> - **运行形态新增"本地 Web 服务模式"为默认**（`SnapRank.exe serve`，监听 `127.0.0.1:8787`，构建脚本自动打开浏览器），Wails 桌面壳保留为可选形态（需 wails CLI 构建）；
> - **构建脚本 `build.bat`**：先杀死旧进程 → 构建前端与后端 → 打开浏览器。

## 1. 项目概述

### 1.1 背景与目标

拍摄的照片数量越来越大，人工挑选、整理、归档耗时费力。本方案设计一套**本地运行的自动化照片处理工具**：

1. 批量导入照片；
2. 使用本地压缩引擎（DD鹅 ddGoose-go）压缩图片；
3. 调用聚合平台基元律动（tokenrhythm.studio）上已订阅的多模型视觉 API 对照片进行多维打分，模型在桌面端在线下拉选择、**按批次切换**；
4. 评分完成后**预览分布、人工复核**，确认后自动将照片归档到分类文件夹，并产出评分明细（SQLite/CSV）。

目标产出：把照片选入一个待处理目录，在界面中点"开始"，评分完成后得到「分布预览 + 评分明细」，确认后产出「各个分数段的分类文件夹 + report.csv/db」。

### 1.2 方案命名

**中文名：帧选**（帧 = 照片帧，选 = 择优分级）
**英文名：SnapRank**（Snap = 快照/拍照，Rank = 评分排序）

### 1.3 名词定义

| 名词 | 含义 |
|---|---|
| 源图 | 手机/相机拍摄的原始照片 |
| 压缩图 | 本地压缩后的照片（仅作为模型输入与缩略图，不动源图） |
| 评分维度 | 模型打分依据的维度集合（见 5.1） |
| 分档 | 依据总分划分的归档级别 |
| 基元律动 | tokenrhythm.studio，多模型聚合接入服务，一个 API Key 调多家模型 |
| 可用模型 | 平台侧开通、可被"在线选择"的视觉理解模型 |
| session | 一次"扫描→压缩→评分"的运行单元，有唯一 ID，支持断点续跑 |
| 指纹 | 文件内容的 SHA-256，用于去重、断点续跑与跨会话评分缓存 |

## 2. 总体架构

### 2.1 处理流水线（两阶段）

```
拍照/导入源图
      │
      ▼
┌─────────────────┐
│ ① 批量导入        │ 递归扫描目录，按内容指纹去重，过滤非图片/损坏文件
└─────────────────┘
      ▼
┌─────────────────┐
│ ② 本地压缩        │ EXIF 方向校正 → 缩边(≤2048px) → DD鹅(MozJPEG q82) → 压缩图缓存
└─────────────────┘
      ▼
┌─────────────────┐
│ ③ AI 打分（阶段一）│ 压缩图 Base64 → 所选视觉模型 → 解析维度分 JSON → 本地加权总分 → 落库
└─────────────────┘
      ▼
┌─────────────────┐
│ ④ 预览复核        │ 分布统计/逐张预览/手动调档（不动文件）
└─────────────────┘
      ▼
┌─────────────────┐
│ ⑤ 分类归档（阶段二）│ 用户确认后，按总分分档 复制/移动 到对应文件夹，写 report.csv/db
└─────────────────┘
```

### 2.2 运行形态

| 形态 | 启动方式 | 说明 |
|---|---|---|
| **本地 Web 服务（默认）** | `SnapRank.exe serve [--port 8787]` | 同机浏览器访问 `http://127.0.0.1:8787`；REST API + SSE 进度；前端静态资源内嵌二进制；`build.bat` 构建/重启/打开浏览器 |
| Wails 桌面壳（可选） | `wails build`（需 wails CLI） | WebView2 窗口渲染同一前端，绑定层调用同一核心（`bind/`） |
| CLI（可选） | `SnapRank.exe run --dir …` | 无界面批量场景，支持 `--dry-run`/`--yes` |

三种形态共用同一业务核心（`internal/…`），保证行为一致。

### 2.3 逻辑分层

| 层 | 组件 | 说明 |
|---|---|---|
| 界面层 | Web UI（Vue 3 + Vite） | 微信风格、亮色为主、暗色切换；页面：运行 / 复核 / 明细 / 设置 |
| 桥接层 | HTTP API + SSE（serve）/ Wails Bind（desktop） | 同一组核心方法的两种暴露 |
| 业务层 | 流水线调度 | 导入 → 压缩 → 打分 →（确认）→ 归档 的编排、并发、重试、断点续跑、成本护栏 |
| 接入层 | Provider 抽象 | 主力基元律动（OpenAI 兼容、单 Key）；内置 `mock` Provider 供离线演示/测试；保留直连原厂扩展位 |
| 基础设施层 | 配置/日志/存储 | 用户目录 YAML 配置、SQLite 明细与缓存、轮转日志 |

## 3. 模块设计

### 3.1 批量导入模块

- 输入：用户选择的源图目录（界面支持手动输入路径；桌面壳支持系统目录对话框）。
- 处理：递归扫描 `jpg/jpeg/png/webp/gif/bmp/tiff`；**HEIC/HEIF 与相机 RAW（CR2/NEF/ARW/DNG…）明确不支持**，扫描时跳过并在明细中记录 `unsupported`（HEIC 解码需系统编解码器或 libheif，列为后续扩展）。
- 排除：隐藏文件、临时文件（`~$`、`.tmp`）、体积 < 10KB 的疑似缩略图（可配置）。
- 去重：读取文件内容计算 **SHA-256 指纹**，同批次内指纹相同只处理一份（其余标记 `duplicate`，不重复计费）；同名不同内容不受影响。
- 输出：待处理清单写入 SQLite，创建 session 记录。

### 3.2 本地压缩模块（DD鹅 ddGoose-go）

- **压缩引擎**：复用本地项目 `D:\wwwroot\wwwroot\ddGoose-go`，`go.mod` 以 `replace` 指令导入其 `ui` 包（MozJPEG `cjpeg-mod` / pngquant+OxiPNG / gifsicle），`lib_dir` 指向其内置工具目录；不重复造轮子。
- **DD鹅不做缩放**，因此流程为两步：

| 步骤 | 实现 | 说明 |
|---|---|---|
| ① 预处理（纯 Go） | 解码（jpeg/png/gif/webp/bmp/tiff）→ **应用 EXIF Orientation** → 最长边 ≥ `max_edge`(默认2048) 则等比缩小 → 重编码为 JPEG(q95) 临时文件 | 统一格式便于 Base64；重编码天然剥离 EXIF（隐私+体积） |
| ② DD鹅压缩 | `ui.NewCompressor(libDir, opts).Compress(临时文件)`，`OutputCustom` 输出到 `work/<session>/compressed/`，JPEG 质量 82，**关闭写标记魔数** | MozJPEG 优化，产物作为模型输入与缩略图 |

- 压缩缓存命名使用 `<指纹前16位>.jpg`，天然避免同名冲突、支持断点续跑（已存在即跳过）。
- 透明通道（PNG）合成白底后转 JPEG；GIF 取首帧。
- 解码失败/不支持的文件：标记 `bad_image` / `unsupported`，不中断流水线。
- `compressor.lib_dir` 可配置，默认指向 `D:\wwwroot\wwwroot\ddGoose-go\lib`，也支持 exe 同级 `lib/` 目录（便于分发时拷贝）。

### 3.3 AI 打分模块

#### 3.3.1 平台与模型（主力：基元律动）

- 平台：**基元律动（tokenrhythm.studio）**，用户已购订阅；OpenAI 兼容协议，一个 API Key 覆盖平台开通的全部模型；`base_url` 与 Key 按平台文档（`/docs/api-integration`）配置。
- **在线选模型**：启动后请求 `GET /v1/models` 拉取清单。**该接口不返回模态元数据**，因此"仅视觉模型"的过滤规则为：
  1. 内置已知视觉模型正则白名单（`qwen.*v|qwen.*flash|glm-.*v|glm-5|seed-2|doubao.*vision|internvl|…`，随版本维护）；
  2. 设置页可编辑正则与查看全部模型；
  3. 选中后首次调用若平台返回"不支持图片输入"错误，标记该模型不可用并提示。
- **模型切换以批次为粒度**：一批照片评分期间锁定当前模型；切换模型在"下一次开始评分"时生效，界面明确提示**不同模型的分数不可直接横向比较**。明细与缓存均记录 `model` + `prompt_version`。
- 适配层保留 Provider 抽象：`tokenrhythm`（OpenAI 兼容）为默认实现，`mock` 为离线实现（确定性伪评分，0 成本，供演示/测试/无 Key 体验），后续可按配置追加直连百炼/方舟等。

模型参考（平台内多模态模型，价格以平台为准）：

| 模型 ID（示例） | 输入 ¥/M | 特点 |
|---|---|---|
| `qwen3.7-flash` | 1.20 | 视觉/视频，成本首选 |
| `glm-5.3-flash` | 0.40 | 视觉/视频，成本最低 |
| `qwen3.8-27b` | 3.00 | 视觉/视频，质量档 |
| `seed-2.1-turbo` | 3.00 | 视觉/视频，高性价比 |
| `seed-2.1-pro` | 6.00 | 视觉/视频，质量档 |

#### 3.3.2 评分 Prompt 设计（核心）

**角色设定**：资深摄影评审。**评分任务**：从摄影角度对照片按四个维度打分，必须只输出 JSON。

**评分维度**（模型只输出维度分，**总分由本地按当前权重加权计算**——权重调整后可基于已存维度分零成本重算、重新分档，无需重新调用 API）：

| 维度 | 键 | 权重(默认) | 说明 |
|---|---|---|---|
| 技术质量 | `technique` | 40% | 清晰度、噪点、曝光、对焦 |
| 构图 | `composition` | 30% | 主体、留白、平衡、层次 |
| 内容与情感 | `content` | 20% | 主题、故事性、感染力 |
| 色彩 | `color` | 10% | 白平衡、饱和度、影调 |

**输出 JSON Schema**：

```json
{
  "dims": {
    "technique": 0.0,
    "composition": 0.0,
    "content": 0.0,
    "color": 0.0
  },
  "tags": ["风光", "夜景"],
  "reasons": {
    "strength": "……",
    "weakness": "……"
  }
}
```

- 各维度 0–10 分、1 位小数；`score = Σ 维度分 × 权重`（本地计算，0–10，1 位小数）。
- 请求带 `response_format: {"type":"json_object"}`（平台报错时自动降级为纯 Prompt 约束重试一次），并设置 `max_tokens` 上限。
- **解析与校验策略**：优先解析 ```json 代码块 → 失败则裸 JSON → 失败则正则抽取四维分数；四维齐全才判定成功；任一维度缺失/越界做裁剪（clamp 0–10），裁剪记录标记 `clamped`；完全失败标记 `parse_fail`，**不伪造分数、不参与分档**，归档阶段进入独立"待复检"目录。`prompt_version` 随 Prompt 文本版本递增，缓存按版本隔离。

#### 3.3.3 请求参数

- 图片输入：Base64 `data:image/jpeg;base64,xxx`（压缩图，无需托管 URL）。
- `temperature` 默认 0.2（<0.3 保证稳定）；单请求超时默认 60s（可配置）。
- 并发：评分默认 4、压缩默认 2；收到 429 时指数退避并动态下调并发（最低 1），恢复后逐步回升。
- 重试：网络/5xx 最多 3 次（退避 2s/4s/8s）；429 单独处理。

### 3.4 预览复核模块（阶段一产物）

- 评分完成即产出：各档分布（柱状）、平均分、失败/待复检清单、预估费用。
- 逐张缩略图（用压缩图）+ 维度分 + 标签 + 评语；支持**手动调档**（覆盖自动分档，落库记录 `override`）。
- 支持"**本地重算**"：修改权重后一键按已存维度分重算总分并重新分档（0 API 成本）。
- 支持**抽样试跑**：先评前 N 张（默认 10）看效果与费用，再决定是否全量。

### 3.5 分类归档模块（阶段二，确认后执行）

分档阈值（默认 9/7/5，可配置）：

| 档位 | 分数区间 | 归档目录 | 建议动作 |
|---|---|---|---|
| 精选 | ≥ 9.0 | `35_精选` | 适合冲印/发圈 |
| 良好 | 7.0 – 8.9 | `34_良好` | 日常留档 |
| 一般 | 5.0 – 6.9 | `33_一般` | 备份 |
| 待清理 | < 5.0 | `30_待清理` | 可删 |
| 待复检 | parse_fail | `29_待复检` | 解析失败，可重新评分 |
| 未处理 | 解码失败/不支持/重复 | 留在原目录 | 明细中列出原因 |

- **归档输出目录**：`archive_root`（默认 `%USERPROFILE%\Pictures\SnapRank`），每次归档生成批次子目录 `<session_id>/`，各档位文件夹平行存放，`report.csv` 与 `report.db` 副本随批次输出。**不写入应用安装目录**。
- **归档方式**：默认**复制**；"移动"需在确认弹窗中显式选择（移动仅对已归档副本生效，源图安全第一）。
- **冲突策略**：目标已存在且指纹相同 → 跳过（已归档）；指纹不同 → 追加序号 `name (2).ext`。
- **跨卷移动**：`os.Rename` 失败自动回退 copy+delete；删除源文件仅按"移动"且确认后执行。
- **断点续跑**：同一 session 重跑按指纹跳过已完成项；**跨会话缓存**：`score_cache` 按 `(指纹, 模型, prompt_version)` 命中时直接复用评分结果（可在设置关闭），重复导入不再重复计费。

### 3.6 成本护栏

| 机制 | 规则 |
|---|---|
| 启动前预估 | 按待评数量 × 单张预估 token（输入/输出）× 模型单价，开始前展示 |
| 批次上限 | 预估费用 > `batch_cost_limit`（默认 ¥10）时需确认 |
| 每日上限 | 当日累计预估 > `daily_cost_limit`（默认 ¥20，0=不限）时拒绝开始，次日自动恢复 |
| 会话/跨会话缓存 | 指纹命中不重复计费 |
| Mock 模式 | `provider: mock` 时 0 成本，用于体验与验证 |

### 3.7 隐私与数据安全

- 压缩图会上传至聚合平台（第三方）参与评分，界面首次使用时明示；用户可选择只用 `mock` 模式离线体验。
- API Key 存储于用户目录配置文件（`%LOCALAPPDATA%\SnapRank\config.yaml`），该目录仅当前用户可读；设置页提供"测试连接"但不回显完整 Key。
- 压缩图重编码后不携带 EXIF（GPS 等敏感信息不上传）；源图与归档产物始终保留完整 EXIF。

## 4. 技术选型

| 类别 | 选型 | 理由 |
|---|---|---|
| 语言 | **Go 1.22+** | 单二进制分发、并发友好、跨平台 |
| 图片压缩 | **DD鹅 ddGoose-go（本地项目，`replace` 导入 `ui` 包）** | 用户指定复用本地项目；MozJPEG/pngquant 等成熟引擎，`go:embed` 免安装 |
| 缩放/EXIF | disintegration/imaging + 自研 EXIF Orientation 解析 | DD鹅不含缩放；重编码顺带剥离 EXIF |
| 模型接入 | sashabaranov/go-openai + 自研 Provider 适配层（含 mock） | OpenAI 兼容协议统一，切换≈0 |
| 桌面壳 | Wails v2.12 + WebView2（可选形态） | 用户指定；`bind/` 与 serve 模式共用核心 |
| Web 服务 | Go 标准库 `net/http` + SSE + `go:embed` 前端 | 默认形态，零额外依赖、可浏览器验证 |
| 前端 | Vue 3 + Vite | 组件化维护微信风格 UI；亮/暗主题 |
| 配置 | YAML（gopkg.in/yaml.v3） | 直观、可读 |
| 明细存储 | modernc.org/sqlite（纯 Go 无 CGO） | 单机查询友好，避免 CGO 构建负担 |
| 版本管理 | git（本地提交，不推送） | 随代码同步更新文档 |

## 5. 评分体系

### 5.1 维度与权重

- 0–10 分制，1 位小数；**总分 = 四维度加权和，本地计算**（权重可配置，改权重可本地重算重分档）。
- Prompt 中给出评分锚点：`9-10：构图与光影俱佳、情绪突出；7-8：整体良好、略有瑕疵；5-6：构图或曝光明显问题；<5：严重干扰适看`。
- 分数方差控制：`temperature=0.2`，同一批次锁定同一模型与同一 `prompt_version`；P0 验收含"同片 3 次重复评分方差"稳定性测试。

### 5.2 可比性约束

- 分数只在「同模型 + 同 prompt_version」内可比；跨模型/跨版本的分数混入同一分档时，批次报告中显式提示。

## 6. 接口规范

### 6.1 核心 API（HTTP 与 Wails Bind 同构）

| 方法 | 说明 |
|---|---|
| `GetConfig / SaveConfig` | 读写配置（base_url、Key、模型、权重、阈值、并发、限额、路径） |
| `TestConnection` | 校验 Key/base_url（调 `/v1/models`），返回可用模型数 |
| `ListModels` | 拉取模型清单并按视觉规则过滤（含"显示全部"） |
| `GetCurrentModel / SetCurrentModel` | 当前模型（批次粒度生效） |
| `ScanDirectory(dir)` | 扫描目录，返回清单与预估费用 |
| `StartPipeline(opts)` | 阶段一：压缩+评分（opts 含 `sampleN` 抽样、覆盖模型等） |
| `StopPipeline` | 停止（已提交请求跑完，不再新增） |
| `GetSummary` | 分布、平均分、失败/待复检清单、预估费用 |
| `ListPhotos(filter,page)` | 明细分页查询 |
| `GetThumb(id)` | 压缩图缩略图（serve 为 HTTP，desktop 为 dataURI） |
| `SetPhotoBucket(id, bucket)` | 手动调档（override） |
| `Recalculate` | 权重变更后本地重算总分与分档 |
| `Archive(mode)` | 阶段二：`copy`/`move` 执行归档（确认后调用） |
| `OpenFolder(path)` | 打开归档目录（桌面壳/服务本机） |
| `OnEvent("progress"|"stage"|"done")` | 事件订阅（serve 走 SSE，desktop 走 Wails Events） |

### 6.2 CLI（内置，可选）

```bash
SnapRank.exe run --dir ./photos \    # 源图目录（必填）
             --model qwen3.7-flash \ # 模型 ID（默认取配置）
             --out ./result \        # 归档输出目录（默认取配置）
             --copy \                # 归档用复制（默认）
             --move \                # 移动（显式选择，与 --copy 互斥）
             --sample 10 \           # 抽样试跑
             --dry-run \             # 只跑压缩+评分，不归档
             --yes                   # 跳过确认
```

### 6.3 模型调用示例（OpenAI 兼容，Go 伪代码）

```go
cfg := openai.DefaultConfig(apiKey)
cfg.BaseURL = platformBaseURL // 基元律动 API 地址
client := openai.NewClientWithConfig(cfg)

resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
    Model:          currentModelID, // 批次内锁定
    Temperature:    0.2,
    MaxTokens:      512,
    ResponseFormat: &openai.ChatResponseFormat{Type: "json_object"}, // 失败自动降级
    Messages: []openai.ChatCompletionMessage{{
        Role: "user",
        Content: []openai.ChatMessagePart{
            {Type: "text", Text: scorePrompt},
            {Type: "image_url", ImageURL: &openai.ChatMessageImageURL{
                URL: "data:image/jpeg;base64," + b64,
            }},
        },
    }},
})
dims := parseDimsJSON(resp.Choices[0].Message.Content) // 校验/裁剪
score := weightedSum(dims, cfg.Weights)                // 本地加权
```

### 6.4 错误处理

| 场景 | 处理 |
|---|---|
| 单张模型调用失败 | 重试 3 次（退避 2s/4s/8s），仍失败记 `failed` 跳过 |
| 图片损坏无法解码 | 标记 `bad_image`，不中断，留原目录 |
| 格式不支持（HEIC/RAW） | 标记 `unsupported`，留原目录 |
| 额度/限流（429） | 指数退避 + 动态降低并发 |
| 解析 JSON 失败 | 标记 `parse_fail` → 归入 `29_待复检`，不伪造分数、不中断 |
| 网络不可用 | 任务暂停并提示，网络恢复后可续跑（断点续跑） |
| 归档目标同名 | 指纹相同跳过；不同则 `name (2).ext` 重命名 |
| 跨卷移动 | rename 失败回退 copy+delete |

## 7. 目录规划

### 7.1 代码仓库

```
SnapRank/
├── main.go                 # 入口：serve（默认）/ run（CLI）/ version
├── main_wails.go           # Wails 桌面壳入口（-tags desktop 构建时启用）
├── wails.json              # Wails 构建配置（可选形态）
├── build.bat               # 一键脚本：杀旧进程 → 构建前端+后端 → 打开浏览器
├── go.mod / go.sum         # replace ddgoose-go => D:/wwwroot/wwwroot/ddGoose-go
├── internal/
│   ├── config/             # 配置（用户目录 YAML，默认值兜底）
│   ├── store/              # SQLite 明细/缓存/费用记录（modernc.org/sqlite）
│   ├── fp/                 # SHA-256 指纹
│   ├── compress/           # 预处理（EXIF/缩放/转 JPEG）+ DD鹅(ui) 封装
│   ├── provider/           # Provider 抽象：tokenrhythm(OpenAI兼容) / mock
│   ├── scorer/             # Prompt、JSON 解析校验、本地加权
│   ├── pipeline/           # 流水线编排、并发、重试、事件总线、成本护栏
│   ├── archive/            # 分档归档（冲突/跨卷）、CSV(UTF-8 BOM)
│   └── core/               # 面向桥接层的用例聚合（serve/bind 共用）
├── server/                 # HTTP API + SSE + 前端静态托管（go:embed）
├── bind/                   # Wails 绑定（-tags desktop）
├── frontend/               # Vue 3 + Vite（微信风格，亮/暗主题）
│   ├── src/views/          # Run / Review / Detail / Settings
│   └── dist/               # 构建产物（嵌入二进制）
└── SnapRank-设计方案.md
```

### 7.2 运行期目录（用户目录，不写安装目录）

```
%LOCALAPPDATA%\SnapRank\
├── config.yaml             # base_url/API Key/权重/阈值/并发/限额/路径
├── snaprank.db             # SQLite：sessions/photos/score_cache/spend_log
├── logs\snaprank-*.log     # 按天轮转，保留 7 份
└── work\<session_id>\compressed\   # 压缩图缓存（<指纹>.jpg）

%USERPROFILE%\Pictures\SnapRank\    # archive_root（可配置）
└── <session_id>\
    ├── 35_精选\ 34_良好\ 33_一般\ 30_待清理\ 29_待复检\
    └── report.csv / report.db
```

### 7.3 SQLite 表结构

```sql
sessions(id TEXT PRIMARY KEY, created_at TEXT, source_dir TEXT,
         model TEXT, prompt_version TEXT, weights TEXT, thresholds TEXT,
         status TEXT, total INTEGER, done INTEGER);

photos(id INTEGER PRIMARY KEY AUTOINCREMENT,
       session_id TEXT REFERENCES sessions(id),
       fingerprint TEXT, src_path TEXT, filename TEXT, rel_path TEXT,
       size INTEGER, status TEXT, error TEXT,             -- pending/compressed/scored/
                                                          -- parse_fail/failed/bad_image/
                                                          -- unsupported/duplicate
       score REAL, dims TEXT, tags TEXT, strength TEXT, weakness TEXT,
       model TEXT, prompt_version TEXT, clamped INTEGER,
       override_bucket TEXT, archived_path TEXT,
       compressed_path TEXT, duration_ms INTEGER, updated_at TEXT);
CREATE UNIQUE INDEX uq_photos_sess_src ON photos(session_id, src_path);
CREATE INDEX idx_photos_sess ON photos(session_id);

score_cache(fingerprint TEXT, model TEXT, prompt_version TEXT,
            dims TEXT, tags TEXT, strength TEXT, weakness TEXT,
            created_at TEXT, PRIMARY KEY(fingerprint, model, prompt_version));

spend_log(id INTEGER PRIMARY KEY AUTOINCREMENT, day TEXT, model TEXT,
          photos INTEGER, est_cost REAL);
```

### 7.4 session 语义

- `session_id = 20260830_141522_xxxx`（时间戳+随机后缀）；每次"开始评分"创建或复用（同源目录 + 未完成 session 可续跑，界面提示"继续上次"）。
- 续跑判定：`photos` 中同 session 内按 `(src_path)` 与指纹比对，`scored/parse_fail` 跳过，其余重跑。
- 跨会话：`score_cache` 命中（指纹+模型+版本）直接复用评分，`source=cached` 标记，0 计费。

## 8. 成本与性能估算（单张照片）

| 项 | 估算 |
|---|---|
| 输入 token | 2048px 压缩图按多数 VLM 的 tile 计费约 2–5k tokens/图（v1.1 估算 1–2k 偏乐观，v1.2 修正）；评分场景可将 `max_edge` 降至 1280 再省约 60%，P0 用 50 张实测校准 |
| 输出 token | JSON 约 0.2–0.5k tokens/图（`max_tokens=512` 兜底） |
| 单张费用 | `glm-5.3-flash` 约 ¥0.004–0.01/图；`qwen3.7-flash` 约 ¥0.01–0.02/图；`seed-2.1-pro` 约 ¥0.05–0.1/图 |
| 单张耗时 | 压缩 <1s + 模型 1–3s（并发摊销） |

> 以基元律动订阅计划抵用/计费为准；上线前先用 50 张样片实测单张费用与分数稳定性（同片 3 次）。

## 9. 实施计划

| 阶段 | 内容 | 验收 |
|---|---|---|
| P0 | 核心链路：导入（指纹去重）→ 压缩（EXIF/缩放+DD鹅）→ 打分（基元律动/mock 单模型）→ 本地加权 → SQLite；CLI `run` 跑通 | CLI 跑通 50 张样片（含重复/损坏/不支持样本），产出 report；同片 3 次评分方差 ≤0.5 |
| P1 | serve 模式 + Vue3 界面：选目录/模型下拉/进度 SSE/缩略图/分布预览/亮暗切换；两阶段归档（确认后 copy/move） | 浏览器内完成完整流程；模型切换按批次生效；`build.bat` 一键重启+开浏览器 |
| P2 | 断点续跑、跨会话缓存、429 动态限流、成本护栏、明细分页、手动调档、本地重算 | 中断重跑不重复计费；改权重重算 0 API 成本 |
| P3 | Wails 桌面壳构建、标签检索、抽样试跑入口、用法用量展示 | `wails build` 产物可运行（环境具备时） |

## 10. 风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| 模型审美与个人标准偏差 | 分数不符合主观预期 | 权重可配置+本地重算；手动调档；抽样试跑先行 |
| 跨模型分数不可比 | 混批分档失真 | 批次锁定模型；明细记录 model+prompt_version；报告显式提示 |
| 聚合平台模型更新/下架 | 在线列表变化 | 运行时拉取；白名单可编辑；选中模型不支持图片输入时标记不可用 |
| 大批量成本失控 | 费用超预期 | 预估+批次/每日上限+缓存复用+Mock 模式 |
| 压缩损失影响极细节评分 | 罕见场景 | 预处理保守（≤2048px/q95 中间码 + MozJPEG q82），`max_edge` 可调 |
| DD鹅 lib 目录缺失 | 压缩失败 | 启动自检 lib_dir，缺失时明确报错并指引 |
| WebView2 运行库缺失（老系统） | 桌面壳打不开 | 默认走 serve 浏览器形态；Wails 可选内置引导安装 |
| 隐私顾虑 | 照片上传第三方 | 界面明示；Mock 离线模式；压缩图剥离 EXIF |

## 附：待确认决策（评审点）

1. ~~主力平台~~ 已定：**基元律动（tokenrhythm.studio）**，模型批次粒度在线选择 ✅
2. ~~实现语言~~ 已定：**Go 1.22+**，默认 **serve 浏览器形态**，Wails v2.12 桌面壳可选 ✅
3. ~~压缩方案~~ 已定：**复用本地项目 DD鹅（ddGoose-go `ui` 包）+ SnapRank 预处理（EXIF/缩放）** ✅
4. 分档阈值默认 `9/7/5` 四档 + 待复检/未处理两类，是否认可？（可配置）
5. Web 前端 **Vue 3 + Vite** 是否认可？（或更轻量原生 JS）
6. 默认模型建议 `qwen3.7-flash`；若已开通 `glm-5.3-flash` 成本更低，是否改默认？
7. 归档输出默认 `%USERPROFILE%\Pictures\SnapRank`，是否需要其他默认路径？
