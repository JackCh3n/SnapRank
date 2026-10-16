// Package pipeline 流水线编排：导入 → 压缩 → 打分（阶段一）→（确认）→ 归档（阶段二）。
// 含并发管理、重试/限流、断点续跑、跨会话缓存、成本护栏与事件总线。
package pipeline

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"snaprank/internal/archive"
	"snaprank/internal/compress"
	"snaprank/internal/config"
	"snaprank/internal/fp"
	"snaprank/internal/logutil"
	"snaprank/internal/provider"
	"snaprank/internal/scorer"
	"snaprank/internal/store"
)

// 成本估算常数（压缩为 max_edge 后的经验值，P0 用实测校准）
const (
	EstInTokens  = 3000
	EstOutTokens = 350
)

// SessionAll 「所有会话/全部」哨兵值：明细与复核的合并视图用（与前端约定一致）
const SessionAll = "__ALL__"

// RecycleDirName 图库删除回收站目录名（建在源图目录下；Scan 跳过该目录，避免重扫进评分流程）
const RecycleDirName = "SnapRank_回收站"

// Event 事件总线消息（serve 转 SSE，desktop 转 Wails Events）
type Event struct {
	Type string      `json:"type"` // progress | stage | done | error
	Data interface{} `json:"data"`
}

// Progress 单张完成进度
type Progress struct {
	SessionID string  `json:"session_id"`
	PhotoID   int64   `json:"photo_id,omitempty"` // 照片记录 ID（前端点击最近评完条目弹窗查看用）
	Index     int     `json:"index"`
	Total     int     `json:"total"`
	File      string  `json:"file"`
	Score     float64 `json:"score"`
	Status    string  `json:"status"`
	Error     string  `json:"error,omitempty"`
	Cached    bool    `json:"cached,omitempty"`
}

// Stage 流水线阶段切换
type Stage struct {
	SessionID string `json:"session_id"`
	Stage     string `json:"stage"` // scan | score
	Total     int    `json:"total"`
	Done      int    `json:"done"`
}

// DonePayload 完成汇总
type DonePayload struct {
	SessionID string `json:"session_id"`
	Stopped   bool   `json:"stopped"`
	Failed    int    `json:"failed"`
	Night     bool   `json:"night,omitempty"` // 夜间评分模式（失败数含解析失败）
}

// NightPayload 夜间模式重试轮次通知
type NightPayload struct {
	SessionID string `json:"session_id"`
	Round     int    `json:"round"`
	Remaining int    `json:"remaining"`
	DelaySec  int    `json:"delay_sec"`
}

// Bus 事件总线（慢消费者丢帧，不阻塞流水线）
type Bus struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// NewBus 构造
func NewBus() *Bus { return &Bus{subs: map[chan Event]struct{}{}} }

// Subscribe 订阅事件
func (b *Bus) Subscribe() chan Event {
	ch := make(chan Event, 256)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe 取消订阅
func (b *Bus) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

// Publish 广播（非阻塞）
func (b *Bus) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// StartOpts 启动参数
type StartOpts struct {
	Dir      string   `json:"dir"`      // 源目录（可含 ; / ；/ 换行分隔的多目录，等价于 Dirs）
	Dirs     []string `json:"dirs"`     // 多目录：合并为一个批次（一条照片队列）
	Model    string   `json:"model"`    // 为空取配置默认
	SampleN  int      `json:"sample_n"` // 抽样试跑数量，0=全量
	ForceNew bool     `json:"force_new"` // 强制新建批次（不续跑、不并入运行中批次）
	Formats  []string `json:"formats"`   // 格式白名单（空=全部支持格式；如 ["jpg","jpeg","png"]）
	Night    bool     `json:"night"`     // 夜间评分模式：失败照片轮末重新入队，循环重试直到全部成功
	Models   []string `json:"models"`    // 多模型对比：≥2 个模型并行评同一张；首个为主模型（入正记录），其余进评分历史
	// 跨平台对比：引用预设（各平台 URL/Key/协议）+ 该平台的模型 ID，
	// 与当前平台的模型并行评同一张（如 A 站的 a 模型 + B 站的 b 模型）
	CrossModels []CrossModel `json:"cross_models"`
}

// CrossModel 跨平台对比模型：Preset 为配置里的平台预设名
type CrossModel struct {
	Preset string `json:"preset"`
	Model  string `json:"model"`
}

// scoreTarget 一次评分的调用目标：模型 + 所属 Provider + 入库标签
// （同平台标签=模型 ID；跨平台为 "预设/模型"，避免不同平台同名模型在缓存/历史里冲突）
type scoreTarget struct {
	prov  provider.Provider
	model string
	label string
}

// queuedTask 排队中的任务
type queuedTask struct {
	sessID string
	opts   StartOpts
	model  string
}

// Engine 流水线引擎
type Engine struct {
	cfgFn func() *config.Config // 每次读取最新配置（设置热更新）
	store *store.Store
	Bus   *Bus

	running   atomic.Bool
	cancel    context.CancelFunc
	sessionID string
	paused    atomic.Bool // 暂停标记：跑完当前这张后挂起，恢复后继续（内存态，随任务结束清除）
	pools     atomic.Pointer[runPools] // 运行中任务的模型池集合（供单池暂停/停止/追加模型）
	listMu    sync.Mutex
	listReady bool   // 当前运行任务的照片清单已加载：此后新登记的必须追加进队列（防漏）
	scanMu    sync.Mutex
	scanning  map[string]bool // 仍在后台扫描的会话（批次收尾要等它扫完）
	mu        sync.Mutex
	queued    []queuedTask // 排队任务（当前任务结束后顺序执行）
}

// New 构造引擎
func New(cfgFn func() *config.Config, st *store.Store) *Engine {
	return &Engine{cfgFn: cfgFn, store: st, Bus: NewBus()}
}

// Config 读取当前配置
func (e *Engine) Config() *config.Config { return e.cfgFn() }

// Store 暴露存储（供 core 层查询）
func (e *Engine) Store() *store.Store { return e.store }

// IsRunning 是否有任务在跑
func (e *Engine) IsRunning() bool { return e.running.Load() }

// CurrentSession 当前会话 ID
func (e *Engine) CurrentSession() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sessionID
}

// ---------- 扫描与预估 ----------

// ScanItem 扫描结果项
type ScanItem struct {
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	FP       string `json:"-"`
	Dup      bool   `json:"dup"` // 批内重复
}

// Scan 扫描目录：过滤、按内容指纹去重；返回清单与预估费用。
// formats 为扩展名白名单（如 ["jpg","jpeg"]，不含点、小写；空=全部支持格式，
// 不支持的格式 HEIC/RAW 等仍会登记为 unsupported）。
func (e *Engine) Scan(dir string, formats []string) ([]*ScanItem, float64, error) {
	cfg := e.cfgFn()
	whitelist := map[string]bool{}
	for _, f := range formats {
		f = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(f), "."))
		if f != "" {
			whitelist[f] = true
		}
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, 0, fmt.Errorf("目录不存在或不可读: %s", dir)
	}
	var items []*ScanItem
	seen := map[string]bool{}
	scanned := 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 不可读的子树跳过
		}
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") {
				return filepath.SkipDir
			}
			if name == RecycleDirName {
				return filepath.SkipDir // 图库删除回收站：不进评分流程
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		supported := compress.SupportedExts[ext]
		unsupported := compress.UnsupportedExts[ext]
		if !supported && !unsupported {
			return nil
		}
		// 格式白名单：不在名单内的直接跳过（含不支持的 RAW/HEIC）
		if len(whitelist) > 0 && !whitelist[strings.TrimPrefix(ext, ".")] {
			return nil
		}
		// 未指定白名单时，不支持的格式仅登记标记（unsupported）
		if strings.HasPrefix(info.Name(), "~$") || strings.HasSuffix(info.Name(), ".tmp") {
			return nil
		}
		if info.Size() < int64(cfg.Pipeline.MinFileSizeKB)*1024 {
			return nil
		}
		f, err := fp.OfFile(path)
		if err != nil {
			return nil
		}
		dup := seen[f]
		seen[f] = true
		items = append(items, &ScanItem{Path: path, Filename: info.Name(), Size: info.Size(), FP: f, Dup: dup})
		// 指纹较慢（大图要整份读）：每 25 个推一次进度，前端显示「扫描中…已扫 N 个文件」
		scanned++
		if scanned%25 == 0 {
			e.Bus.Publish(Event{Type: "scan", Data: ScanProgress{Scanned: int64(scanned)}})
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	if len(items) > 0 {
		cfg.RememberDir(dir) // 扫描到照片才记入历史
	}
	live := 0
	for _, it := range items {
		if !it.Dup {
			live++
		}
	}
	model := cfg.Model.Default
	return items, e.estCost(model) * float64(live), nil
}

// estCost 单张预估费用（元）；mock 或未知价格为 0
func (e *Engine) estCost(model string) float64 {
	cfg := e.cfgFn()
	if cfg.Provider.Type == "mock" {
		return 0
	}
	p, ok := cfg.Cost.Prices[model]
	if !ok {
		return 0
	}
	return (float64(EstInTokens)*p.InputPerM + float64(EstOutTokens)*p.OutputPerM) / 1e6
}

// ---------- 启动与执行 ----------

// StartResult 任务创建结果
type StartResult struct {
	SessionID string `json:"session_id"`
	Resumed   bool   `json:"resumed"` // 是否续跑未完成的批次
	Merged    bool   `json:"merged"`  // 是否并入运行中的批次（一条照片队列，不再按目录分批次）
	Pending   int    `json:"pending"` // 续跑/并入时剩余待处理张数
	Model     string `json:"model"`   // 本批次实际使用的模型（批次内锁定）
}

// Start 校验并创建评分任务；已有任务运行时自动排队（顺序执行）。
// 同目录存在未完成批次时自动续跑（只处理剩余的）。校验同步返回错误，执行异步进行。
func (e *Engine) Start(opts StartOpts) (*StartResult, error) {
	cfg := e.cfgFn()
	if len(effectiveDirs(opts)) == 0 {
		return nil, errors.New("缺少源图目录")
	}
	if cfg.Provider.Type != "mock" && cfg.Provider.APIKey == "" {
		return nil, errors.New("尚未配置 API Key（可在设置切换 mock 模式离线体验）")
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = strings.TrimSpace(cfg.Model.Default)
	}
	// 多模型对比：去重保序，主模型（首个）决定照片正记录，其余只进评分历史
	if len(opts.Models) > 1 {
		seen := map[string]bool{}
		models := make([]string, 0, len(opts.Models))
		for _, m := range opts.Models {
			if m = strings.TrimSpace(m); m != "" && !seen[m] {
				seen[m] = true
				models = append(models, m)
			}
		}
		if len(models) < 2 {
			opts.Models = nil
		} else {
			opts.Models = models
			model = models[0]
		}
	}
	// 跨平台对比模型：校验预设存在、去重（同平台同名模型只保留一个）
	if len(opts.CrossModels) > 0 {
		presetNames := map[string]bool{}
		for _, p := range cfg.Presets {
			presetNames[p.Name] = true
		}
		seen := map[string]bool{}
		valid := make([]CrossModel, 0, len(opts.CrossModels))
		for _, cm := range opts.CrossModels {
			if cm.Preset == "" || cm.Model == "" {
				continue
			}
			key := cm.Preset + "/" + cm.Model
			if seen[key] {
				continue
			}
			if !presetNames[cm.Preset] {
				return nil, fmt.Errorf("跨平台模型引用的预设不存在: %s（可在设置页添加）", cm.Preset)
			}
			seen[key] = true
			valid = append(valid, cm)
		}
		opts.CrossModels = valid
	}
	if model == "" && len(cfg.Model.VisionPatterns) > 0 {
		// 配置默认模型为空：从 Provider 拉取首个可用模型兜底由 UI 层处理；
		// 此处直接报错避免空模型导致评分卡死
		return nil, errors.New("未选择打分模型：请在运行页选择模型或到设置页配置默认模型")
	}

	// 目录集合：多目录合并为一个批次（Dirs 优先；兼容 Dir 里 ; / ；/ 换行分隔）
	dirs := effectiveDirs(opts)
	if len(dirs) == 0 {
		return nil, errors.New("未指定目录")
	}
	for _, d := range dirs {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("目录不存在或不可读: %s", d)
		}
	}
	// 先列出候选文件（不做指纹，很快），首批指纹够 initialScanBatch 张就让任务开跑；
	// 其余在后台继续指纹并追加进队列（8867 张也不卡「创建任务」）
	files := e.listCandidates(dirs, opts.Formats)
	if len(files) == 0 {
		return nil, errors.New("目录中没有可处理的图片")
	}
	seenFP := map[string]bool{}
	var seenMu sync.Mutex
	var entries []scanEntry
	idx := 0
	scanBatch := e.initialScanSize() // 首批 = 并发×5：够各模型池同时开工即开跑
	for idx < len(files) && len(entries) == 0 {
		end := idx + scanBatch
		if end > len(files) {
			end = len(files)
		}
		entries = append(entries, fingerprintFiles(files[idx:end], seenFP, &seenMu)...)
		idx = end
	}
	if len(entries) == 0 {
		return nil, errors.New("目录中没有可处理的图片")
	}
	live := 0
	for _, en := range entries {
		if !en.it.Dup {
			live++
		}
	}
	if live == 0 {
		return nil, errors.New("目录中没有可处理的图片")
	}
	if opts.SampleN > 0 && opts.SampleN < live {
		live = opts.SampleN
	}
	estCost := e.estCost(model) * float64(live)

	// 成本护栏：批次上限 + 每日上限
	if e.estCost(model) > 0 {
		if cfg.Cost.BatchLimit > 0 && estCost > cfg.Cost.BatchLimit {
			return nil, fmt.Errorf("预估费用 ¥%.2f 超过单批次上限 ¥%.2f（可先抽样试跑或在设置调整）", estCost, cfg.Cost.BatchLimit)
		}
		day := time.Now().Format("2006-01-02")
		spent, _ := e.store.SpendToday(day)
		if cfg.Cost.DailyLimit > 0 && spent+estCost > cfg.Cost.DailyLimit {
			return nil, fmt.Errorf("今日预估累计 ¥%.2f 将超过每日上限 ¥%.2f（可在设置调整）", spent+estCost, cfg.Cost.DailyLimit)
		}
	}

	// ---------- 运行中：并入当前批次 ----------
	// 只有一条照片队列，不再按目录分批次：新目录的照片直接追加进运行中批次的
	// 投料队列，各模型池接着评（压缩缓存复用，已评过的走缓存秒过）。
	// force_new 例外：明确要求独立批次时走下面的新建+排队路径。
	if e.running.Load() && !opts.ForceNew {
		rp := e.pools.Load()
		if rp == nil {
			return nil, errors.New("任务正在切换，请稍后重试")
		}
		sessID := e.CurrentSession()
		rp.work.beginAppend() // 标记「正在并入」：投料队列暂空也不会被判收尾
		defer rp.work.endAppend()
		if !opts.ForceNew {
			e.mergeSessionsInto(dirs, sessID) // 这些目录之前的未完成批次并入当前批次
		}
		e.registerPhotos(sessID, entries)
		pend := e.pendingPhotosUnderDirs(sessID, dirs)
		if opts.SampleN > 0 && opts.SampleN < len(pend) {
			pend = pend[:opts.SampleN]
		}
		if len(pend) > 0 {
			if err := e.AddPhotosToRunning(pend); err != nil {
				return nil, err
			}
		}
		_ = e.store.RefreshSessionCounts(sessID)
		e.store.SetSessionDirs(sessID, e.sessionDirsJoined(sessID, dirs))
		logutil.Info("并入当前批次 %s：%d 个目录 %d 张待评分", sessID, len(dirs), len(pend))
		e.startBackgroundScan(sessID, files[idx:], seenFP, &seenMu, len(files), idx)
		return &StartResult{SessionID: sessID, Resumed: true, Merged: true, Pending: len(pend), Model: model}, nil
	}

	// ---------- 空闲：合并为一个批次后入队 ----------
	var sess *store.Session
	if !opts.ForceNew {
		// 与这些目录相关的未完成批次：取最新一个作基准，其余并入（目录合并为一个批次）
		if cands, err := e.store.FindResumableSessionsForDirs(dirs); err == nil && len(cands) > 0 {
			sess = cands[0]
			e.RemoveQueued(sess.ID) // 若已在队列里：先摘掉，稍后统一重新入队（避免重复跑）
			for _, other := range cands[1:] {
				if n, err := e.store.MergeSessionInto(other.ID, sess.ID); err == nil && n > 0 {
					logutil.Info("目录合并：批次 %s 并入 %s（%d 张）", other.ID, sess.ID, n)
					e.RemoveQueued(other.ID)
				}
			}
		}
	}
	sessID := ""
	if sess != nil {
		sessID = sess.ID
		e.store.UpdateSessionStatus(sessID, store.SessionRunning, len(entries), sess.Done)
		logutil.Info("续跑会话 %s（已完成 %d）", sessID, sess.Done)
	} else {
		sessID = newSessionID()
		if err := e.store.CreateSession(sessID, strings.Join(dirs, ";"), model, scorer.PromptVersion); err != nil {
			return nil, err
		}
	}

	// 登记清单；内容已变更的照片重置状态
	e.registerPhotos(sessID, entries)
	if sess != nil {
		e.store.SetSessionDirs(sessID, e.sessionDirsJoined(sessID, dirs))
		_ = e.store.RefreshSessionCounts(sessID)
	}

	resumed := sess != nil
	if resumed {
		// 批次内锁定模型：续跑沿用该批次创建时的模型，忽略新选择
		// （避免同批混用两个模型的分数；想换模型请新建任务）
		if sess.Model != "" && sess.Model != model {
			logutil.Info("批次 %s 锁定模型 %s，忽略新选择 %s", sessID, sess.Model, model)
			model = sess.Model
		}
	}
	pending := 0
	if resumed {
		pending = live - sess.Done
		if pending < 0 {
			pending = 0
		}
	}
	e.enqueue(sessID, opts, model)
	// 剩余文件后台继续扫描并追加进队列（任务已在跑，扫描不再阻塞创建）
	e.startBackgroundScan(sessID, files[idx:], seenFP, &seenMu, len(files), idx)
	return &StartResult{SessionID: sessID, Resumed: resumed, Pending: pending, Model: model}, nil
}

// scanEntry 扫描项 + 所属根目录（登记时算相对路径用）
type scanEntry struct {
	it   *ScanItem
	base string
}

// ScanProgress 后台扫描进度（SSE 推给前端：已扫描 N / 总数 M）
type ScanProgress struct {
	SessionID string `json:"session_id"`
	Scanned   int64  `json:"scanned"`
	Total     int64  `json:"total"`
	Done      bool   `json:"done,omitempty"`
}

// initialScanFactor 首批指纹张数 = 评分并发 × 该系数（如并发 4 → 先扫 20 张就开跑）
const initialScanFactor = 5

// initialScanSize 首批指纹张数：并发数 × 5（并发越大首批越大，够各池同时开工）
func (e *Engine) initialScanSize() int {
	sc := e.cfgFn().Pipeline.ScoreConcurrency
	if sc < 1 {
		sc = 1
	}
	return sc * initialScanFactor
}

// scanChunk 后台扫描每登记这么多张就追加进运行中批次并推进度
const scanChunk = 40

// beginScan / endScan / isScanning 后台扫描登记：批次收尾要等扫描结束（新照片要并入队列）
func (e *Engine) beginScan(sessID string) {
	e.scanMu.Lock()
	if e.scanning == nil {
		e.scanning = map[string]bool{}
	}
	e.scanning[sessID] = true
	e.scanMu.Unlock()
}

func (e *Engine) endScan(sessID string) {
	e.scanMu.Lock()
	delete(e.scanning, sessID)
	e.scanMu.Unlock()
}

func (e *Engine) isScanning(sessID string) bool {
	e.scanMu.Lock()
	defer e.scanMu.Unlock()
	return e.scanning[sessID]
}

// candFile 候选文件：路径 + 所属根目录（登记时算相对路径用）
type candFile struct {
	path string
	base string
}

// listCandidates 收集目录下符合过滤条件的文件（不做指纹哈希，很快）。
// 过滤口径与 Scan 保持一致：扩展名/白名单/最小体积/跳过隐藏与回收站目录
func (e *Engine) listCandidates(dirs, formats []string) []candFile {
	cfg := e.cfgFn()
	whitelist := map[string]bool{}
	for _, f := range formats {
		f = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(f), "."))
		if f != "" {
			whitelist[f] = true
		}
	}
	var out []candFile
	seenPath := map[string]bool{}
	for _, dir := range dirs {
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				name := info.Name()
				if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") || name == RecycleDirName {
					return filepath.SkipDir
				}
				return nil
			}
			ext := strings.ToLower(filepath.Ext(info.Name()))
			supported := compress.SupportedExts[ext]
			unsupported := compress.UnsupportedExts[ext]
			if !supported && !unsupported {
				return nil
			}
			if len(whitelist) > 0 && !whitelist[strings.TrimPrefix(ext, ".")] {
				return nil
			}
			if strings.HasPrefix(info.Name(), "~$") || strings.HasSuffix(info.Name(), ".tmp") {
				return nil
			}
			if info.Size() < int64(cfg.Pipeline.MinFileSizeKB)*1024 {
				return nil
			}
			if !seenPath[path] {
				seenPath[path] = true
				out = append(out, candFile{path: path, base: dir})
			}
			return nil
		})
	}
	return out
}

// fingerprintFiles 指纹 + 跨目录去重（后台扫描与首批共用一个 seen 表）
func fingerprintFiles(files []candFile, seen map[string]bool, mu *sync.Mutex) []scanEntry {
	var out []scanEntry
	for _, f := range files {
		fpv, err := fp.OfFile(f.path)
		if err != nil {
			continue
		}
		mu.Lock()
		dup := seen[fpv]
		seen[fpv] = true
		mu.Unlock()
		if dup {
			continue
		}
		size := int64(0)
		if st, err := os.Stat(f.path); err == nil {
			size = st.Size()
		}
		out = append(out, scanEntry{it: &ScanItem{Path: f.path, Filename: filepath.Base(f.path), Size: size, FP: fpv}, base: f.base})
	}
	return out
}

// effectiveDirs 目录集合：Dirs 优先；否则把 Dir 按 ; / ；/ 换行拆开（去重保序）
func effectiveDirs(opts StartOpts) []string {
	raw := opts.Dirs
	if len(raw) == 0 && strings.TrimSpace(opts.Dir) != "" {
		raw = strings.FieldsFunc(opts.Dir, func(r rune) bool {
			return r == ';' || r == '；' || r == '\n' || r == '\r'
		})
	}
	var out []string
	seen := map[string]bool{}
	for _, d := range raw {
		d = strings.TrimSpace(d)
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// registerEntries 登记扫描项并返回新登记的待评分照片（含数据库 ID，供追加进队列）
func (e *Engine) registerEntries(sessID string, entries []scanEntry) []*store.Photo {
	var out []*store.Photo
	for _, en := range entries {
		status := store.StatusPending
		if en.it.Dup {
			status = store.StatusDuplicate
		} else if compress.UnsupportedExts[strings.ToLower(filepath.Ext(en.it.Path))] {
			status = store.StatusUnsupported // HEIC/RAW：登记但不支持
		}
		p := &store.Photo{
			SessionID: sessID, Fingerprint: en.it.FP, SrcPath: en.it.Path,
			Filename: en.it.Filename, RelPath: relPath(en.base, en.it.Path), Size: en.it.Size, Status: status,
		}
		id, err := e.store.UpsertPhoto(p)
		if err != nil {
			continue
		}
		p.ID = id
		if status == store.StatusPending {
			e.resetIfChanged(id, en.it.FP)
			out = append(out, p)
		}
	}
	return out
}

// registerPhotos 把扫描清单登记进会话（内容已变更的重置为待处理）
func (e *Engine) registerPhotos(sessID string, entries []scanEntry) {
	e.registerEntries(sessID, entries)
}

// startBackgroundScan 后台继续指纹剩余文件：分批登记进会话，能追加就追加进运行中
// 批次（模型池接着评）；每次追加推进度事件（前端显示「扫描 N/M」）。
func (e *Engine) startBackgroundScan(sessID string, rest []candFile, seen map[string]bool, mu *sync.Mutex, total, already int) {
	if len(rest) == 0 {
		return
	}
	e.beginScan(sessID)
	go func() {
		defer e.endScan(sessID)
		scanned := int64(already)
		var buf []scanEntry
		flush := func() {
			if len(buf) == 0 {
				return
			}
			photos := e.registerEntries(sessID, buf)
			buf = buf[:0]
			e.listMu.Lock()
			mustAppend := e.listReady
			e.listMu.Unlock()
			if mustAppend {
				e.appendToRunning(sessID, photos)
			}
			e.Bus.Publish(Event{Type: "scan", Data: ScanProgress{SessionID: sessID, Scanned: scanned, Total: int64(total)}})
		}
		for i := 0; i < len(rest); i += scanChunk {
			end := i + scanChunk
			if end > len(rest) {
				end = len(rest)
			}
			buf = append(buf, fingerprintFiles(rest[i:end], seen, mu)...)
			scanned += int64(end - i)
			flush()
			if e.scanCostExceeded(sessID) {
				e.Bus.Publish(Event{Type: "error", Data: map[string]string{
					"error": "预估费用已达单批次上限，已停止继续扫描剩余文件（已登记的照片会照常评完）"}})
				break
			}
		}
		flush()
		e.Bus.Publish(Event{Type: "scan", Data: ScanProgress{SessionID: sessID, Scanned: int64(total), Total: int64(total), Done: true}})
	}()
}

// appendToRunning 把照片追加进运行中的批次（会话不符则只保留登记，等下次续跑）
func (e *Engine) appendToRunning(sessID string, photos []*store.Photo) {
	if len(photos) == 0 {
		return
	}
	if e.CurrentSession() != sessID {
		return
	}
	if err := e.AddPhotosToRunning(photos); err != nil {
		logutil.Info("后台扫描追加未成功（照片已登记，下次开始时续跑）：%v", err)
	}
}

// scanCostExceeded 累计预估是否已超单批次费用上限（后台扫描持续复核）
func (e *Engine) scanCostExceeded(sessID string) bool {
	cfg := e.cfgFn()
	if cfg.Cost.BatchLimit <= 0 {
		return false
	}
	s, err := e.store.GetSession(sessID)
	if err != nil || s == nil {
		return false
	}
	per := e.estCost(s.Model)
	if per <= 0 {
		return false
	}
	cnt, err := e.store.CountPhotos(sessID)
	if err != nil {
		return false
	}
	return per*float64(cnt) > cfg.Cost.BatchLimit
}

// mergeSessionsInto 把与这些目录相关的未完成批次并入目标批次（目录合并为一个队列）
func (e *Engine) mergeSessionsInto(dirs []string, target string) {
	cands, err := e.store.FindResumableSessionsForDirs(dirs)
	if err != nil {
		return
	}
	for _, s := range cands {
		if s.ID == target {
			continue
		}
		if n, err := e.store.MergeSessionInto(s.ID, target); err == nil && n > 0 {
			logutil.Info("目录合并：批次 %s 并入当前批次 %s（%d 张）", s.ID, target, n)
			e.RemoveQueued(s.ID) // 该批次（若在排队）已并入，不能再按独立批次跑
		}
	}
}

// pendingPhotosUnderDirs 会话内这些目录下尚未完成的照片（并入当前批次用）
func (e *Engine) pendingPhotosUnderDirs(sessID string, dirs []string) []*store.Photo {
	photos, _, err := e.store.ListPhotos(sessID, "", 0, 1<<20, "", "", "", "", -1, -1)
	if err != nil {
		return nil
	}
	prefixes := make([]string, 0, len(dirs))
	for _, d := range dirs {
		prefixes = append(prefixes, strings.ToLower(strings.ReplaceAll(strings.TrimRight(d, "\\/"), "\\", "/"))+"/")
	}
	var out []*store.Photo
	for _, p := range photos {
		if isFinalStatus(p.Status) {
			continue
		}
		key := strings.ToLower(strings.ReplaceAll(p.SrcPath, "\\", "/"))
		for _, pre := range prefixes {
			if strings.HasPrefix(key, pre) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// sessionDirsJoined 会话已有的目录集合与新增目录合并（去重保序，";" 连接）
func (e *Engine) sessionDirsJoined(sessID string, add []string) string {
	var all []string
	if s, err := e.store.GetSession(sessID); err == nil && s != nil {
		all = append(all, effectiveDirs(StartOpts{Dir: s.SourceDir})...)
	}
	seen := map[string]bool{}
	for _, d := range all {
		seen[d] = true
	}
	for _, d := range add {
		if !seen[d] {
			seen[d] = true
			all = append(all, d)
		}
	}
	return strings.Join(all, ";")
}

// RepairFailedWithScores 修复历史数据：状态是失败/解析失败、但确有成功评分记录的
// 照片（多模型下主模型失败曾覆盖其它模型的成功结果）。按新口径——任一模型评成功即
// 算评完——改为已评分，分数取该指纹下各模型最新记录的合议平均。启动时跑一次。
func (e *Engine) RepairFailedWithScores() int {
	list, err := e.store.FailedPhotosWithScores()
	if err != nil || len(list) == 0 {
		return 0
	}
	n := 0
	for _, p := range list {
		recs, err := e.store.PhotoScoresByFingerprint(p.Fingerprint)
		if err != nil {
			continue
		}
		var best *store.PhotoModelScore
		for _, r := range recs {
			if r.Dims != nil {
				best = r
				break
			}
		}
		if best == nil {
			continue
		}
		score := e.RecomputeAggregate(p.Fingerprint)
		if score <= 0 {
			score = scorer.WeightedScore(*best.Dims, e.cfgFn().WeightsNormalized())
		}
		if err := e.store.SetPhotoResult(p.ID, store.StatusScored, score, best.Dims, best.Tags, best.Strength, best.Weakness, best.Model, best.PromptVersion, best.Source, false, 0); err != nil {
			continue
		}
		e.store.SetPhotoStatus(p.ID, store.StatusScored, "")
		n++
	}
	if n > 0 {
		logutil.Info("已修复 %d 张「有评分记录却标为失败」的照片（多模型下主模型失败覆盖成功结果）", n)
	}
	return n
}

// enqueue 任务入队；空闲则立即启动
func (e *Engine) enqueue(sessID string, opts StartOpts, model string) {
	e.mu.Lock()
	e.queued = append(e.queued, queuedTask{sessID: sessID, opts: opts, model: model})
	busy := e.running.Load()
	e.mu.Unlock()
	e.publishQueue()
	if !busy {
		e.popAndRun()
		return
	}
	// 排队任务后台预压缩：当前任务评分阶段（等 API、CPU 空闲）时把下一个目录的
	// 压缩缓存先备好，轮到它时压缩全命中、直接进入评分
	go e.preCompress(sessID)
}

// preCompress 后台预压缩排队任务的待处理照片（只落压缩缓存，不动数据库状态）。
// 任务开跑（自己的压缩池接管）或被移出队列时退出；ToCache 幂等且并发安全，重叠无害。
func (e *Engine) preCompress(sessID string) {
	cfg := e.cfgFn()
	eng, err := compress.NewEngine(cfg.Paths.LibDir, cfg.Pipeline.MaxEdge, cfg.Pipeline.JPEGQuality)
	if err != nil {
		return
	}
	cacheDir := filepath.Join(cfg.Paths.DataDir, "work", "compressed")
	photos, _, err := e.store.ListPhotos(sessID, "", 0, 1<<20, "", "", "", "", -1, -1)
	if err != nil {
		return
	}
	n := 0
	for _, p := range photos {
		if (e.running.Load() && e.CurrentSession() == sessID) || !e.inQueue(sessID) {
			break
		}
		if p.Status != store.StatusPending && p.Status != store.StatusCompressed {
			continue
		}
		if _, err := eng.ToCache(p.SrcPath, p.Fingerprint, cacheDir); err == nil {
			n++
		}
	}
	if n > 0 {
		logutil.Info("预压缩完成：%s %d 张（轮到评分时直接开始）", sessID, n)
	}
}

func (e *Engine) inQueue(sessID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.queued {
		if t.sessID == sessID {
			return true
		}
	}
	return false
}

// popAndRun 取出下一个排队任务执行（顺序执行，同时只跑一个）
func (e *Engine) popAndRun() {
	e.mu.Lock()
	if e.running.Load() || len(e.queued) == 0 {
		e.mu.Unlock()
		return
	}
	t := e.queued[0]
	e.queued = e.queued[1:]
	e.mu.Unlock()
	logutil.Info("任务开始: %s（剩余排队 %d）", t.sessID, len(e.queued))
	e.startAsync(t.sessID, t.opts, t.model)
}

// QueueStatus 当前运行与排队中的任务（current 仅在有任务运行时返回）
func (e *Engine) QueueStatus() map[string]interface{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := make([]string, 0, len(e.queued))
	for _, t := range e.queued {
		ids = append(ids, t.sessID)
	}
	current := ""
	if e.running.Load() {
		current = e.sessionID
	}
	return map[string]interface{}{"current": current, "queued": ids, "paused": e.paused.Load()}
}

func (e *Engine) publishQueue() {
	e.Bus.Publish(Event{Type: "queue", Data: e.QueueStatus()})
}

// PauseTask 暂停当前运行中的任务：跑完正在评分的这张后挂起，恢复后继续
func (e *Engine) PauseTask() bool {
	if !e.running.Load() || e.paused.Load() {
		return false
	}
	e.paused.Store(true)
	logutil.Info("任务已暂停: %s", e.sessionID)
	e.publishQueue()
	return true
}

// ResumeTask 恢复暂停中的任务
func (e *Engine) ResumeTask() bool {
	if !e.paused.Load() {
		return false
	}
	e.paused.Store(false)
	logutil.Info("任务已恢复: %s", e.sessionID)
	e.publishQueue()
	return true
}

// RemoveQueued 从队列中移除一个排队中的任务（不影响运行中任务）
func (e *Engine) RemoveQueued(sessID string) bool {
	e.mu.Lock()
	found := false
	out := e.queued[:0]
	for _, t := range e.queued {
		if t.sessID == sessID {
			found = true
			continue
		}
		out = append(out, t)
	}
	e.queued = out
	e.mu.Unlock()
	if found {
		logutil.Info("已从队列移除任务: %s", sessID)
		e.publishQueue()
	}
	return found
}

// waitIfPaused 暂停期间阻塞（producer 取任务前调用）；返回 false 表示已被停止
func (e *Engine) waitIfPaused(ctx context.Context) bool {
	for e.paused.Load() {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(200 * time.Millisecond):
		}
	}
	return ctx.Err() == nil
}

// ClearQueue 清空排队任务（清空数据时用）
func (e *Engine) ClearQueue() {
	e.mu.Lock()
	e.queued = nil
	e.mu.Unlock()
	e.publishQueue()
}

// resetIfChanged 文件内容与库中指纹不一致时重置为待处理
func (e *Engine) resetIfChanged(id int64, curFP string) {
	p, err := e.store.GetPhoto(id)
	if err != nil || p == nil {
		return
	}
	if p.Fingerprint != curFP && p.Status != store.StatusPending && p.Status != store.StatusDuplicate {
		e.store.ResetPhoto(id)
		logutil.Info("文件内容已变更，重置: %s", p.Filename)
	}
}

// Rescore 复检重评：把指定照片重置为待处理并立即重跑（走当前所选模型），
// 已有压缩缓存命中即跳过压缩，0 重复压缩；forceCost=true 时忽略跨会话评分缓存强制重调 API。
// 支持跨批次：按照片所属批次分组逐批入队（图库勾选重评依赖此行为），批次间顺序执行。
func (e *Engine) Rescore(ids []int64, forceCost bool) (string, error) {
	if len(ids) == 0 {
		return "", errors.New("未选择要重评的照片")
	}
	cfg := e.cfgFn()
	if cfg.Provider.Type != "mock" && cfg.Provider.APIKey == "" {
		return "", errors.New("尚未配置 API Key（或切换到 mock 模式体验）")
	}
	// 按照片所属批次分组；不支持的格式（RAF/HEIC 等）与解码失败无法通过重试解决，直接跳过
	type group struct {
		sessID string
		dir    string
		ids    []int64
	}
	groups := make(map[string]*group)
	n := 0
	for _, id := range ids {
		p, err := e.store.GetPhoto(id)
		if err != nil || p == nil {
			continue
		}
		if p.Status == store.StatusUnsupported || p.Status == store.StatusBadImage {
			continue
		}
		e.store.ResetPhoto(id)
		if forceCost {
			// 重置后清除指纹缓存条目，强制重调 API（同模型+版本）
			e.store.CacheDelete(p.Fingerprint, p.Model, p.PromptVersion)
		}
		g := groups[p.SessionID]
		if g == nil {
			sess, err := e.store.GetSession(p.SessionID)
			if err != nil || sess == nil {
				continue // 批次已被删除，照片无法重跑
			}
			g = &group{sessID: p.SessionID, dir: sess.SourceDir}
			groups[p.SessionID] = g
		}
		g.ids = append(g.ids, id)
		n++
	}
	if n == 0 {
		return "", errors.New("没有可重评的照片（不存在、批次已删除或格式不支持）")
	}
	// 复检重评统一使用当前配置的默认模型（而非批次旧模型）
	model := cfg.Model.Default
	first := ""
	for _, g := range groups {
		e.enqueue(g.sessID, StartOpts{Dir: g.dir}, model)
		if first == "" {
			first = g.sessID
		}
	}
	logutil.Info("复检重评 %d 张入队（%d 个批次，强制重调=%v）", n, len(groups), forceCost)
	return first, nil
}

// RescoreAllFailed 一键重试当前会话全部失败照片（解析失败 + 调用失败）
func (e *Engine) RescoreAllFailed() (string, int, error) {
	// 合并视图：收集全部会话的失败照片（ListPhotos 空会话 = 不按会话过滤）
	photos, _, err := e.store.ListPhotos("", "", 0, 1<<20, "", "", "", "", -1, -1)
	if err != nil {
		return "", 0, err
	}
	ids := make([]int64, 0, len(photos))
	for _, p := range photos {
		if p.Status == store.StatusParseFail || p.Status == store.StatusFailed {
			ids = append(ids, p.ID)
		}
	}
	if len(ids) == 0 {
		return "", 0, errors.New("没有可重试的失败照片")
	}
	sessID, err := e.Rescore(ids, true)
	if err != nil {
		return "", 0, err
	}
	return sessID, len(ids), nil
}

// CurrentSessionModel 会话锁定的模型
func (e *Engine) CurrentSessionModel(sessID string) string {
	s, err := e.store.GetSession(sessID)
	if err != nil {
		return ""
	}
	return s.Model
}

// 夜间评分模式参数
const (
	nightRetryDelay     = 15 * time.Second // 轮间等待：给限流/瞬态故障留恢复时间
	nightMaxStallRounds = 3                // 安全阀：连续 N 轮失败数无减少则停止，防止永久性失败无限重调烧钱
)

// nightLoop 夜间评分模式：一轮结束后收集失败照片（解析失败/调用失败）重新入队再跑，
// 循环直到全部成功、被手动停止，或连续多轮无进展（安全阀触发）。压缩有全局缓存，重试轮零重复压缩。
func (e *Engine) nightLoop(ctx context.Context, sessID string, opts StartOpts, model string) bool {
	stall, prev := 0, -1
	for round := 1; ; round++ {
		if ctx.Err() != nil {
			return true
		}
		ids, err := e.nightFailedIDs(sessID)
		if err != nil {
			logutil.Error("夜间模式读取失败照片: %v", err)
			return false
		}
		if len(ids) == 0 {
			logutil.Info("夜间模式：全部评分成功，结束重试")
			return false
		}
		if prev == len(ids) {
			stall++
		} else {
			stall = 0
		}
		prev = len(ids)
		if stall >= nightMaxStallRounds {
			logutil.Info("夜间模式：连续 %d 轮失败数无减少（剩 %d 张），停止重试", stall, len(ids))
			e.Bus.Publish(Event{Type: "error", Data: map[string]string{
				"session_id": sessID,
				"error":      fmt.Sprintf("🌙 夜间模式停止：连续 %d 轮无进展，剩余 %d 张持续失败（多为内容或格式问题，建议白天手动复检）", stall, len(ids)),
			}})
			return false
		}
		e.Bus.Publish(Event{Type: "night", Data: NightPayload{SessionID: sessID, Round: round, Remaining: len(ids), DelaySec: int(nightRetryDelay / time.Second)}})
		logutil.Info("夜间模式第 %d 轮：%d 张失败照片 %v 后重新入队", round, len(ids), nightRetryDelay)
		select {
		case <-ctx.Done():
			return true
		case <-time.After(nightRetryDelay):
		}
		for _, id := range ids {
			e.store.ResetPhoto(id)
		}
		if stopped := e.run(ctx, sessID, opts, model); stopped {
			return true
		}
	}
}

// nightFailedIDs 收集会话内可重试的失败照片（解析失败+调用失败；格式不支持/解码失败不可重试，不进重试循环）
func (e *Engine) nightFailedIDs(sessID string) ([]int64, error) {
	photos, _, err := e.store.ListPhotos(sessID, "", 0, 1<<20, "", "", "", "", -1, -1)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, 8)
	for _, p := range photos {
		if p.Status == store.StatusParseFail || p.Status == store.StatusFailed {
			ids = append(ids, p.ID)
		}
	}
	return ids, nil
}

func relPath(dir, path string) string {
	r, err := filepath.Rel(dir, path)
	if err != nil {
		return filepath.Base(path)
	}
	return filepath.ToSlash(r)
}

func newSessionID() string {
	return time.Now().Format("20060102_150405") + "_" + randomSuffix()
}

func randomSuffix() string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 4)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// startAsync 启动后台执行
func (e *Engine) startAsync(sessID string, opts StartOpts, model string) {
	ctx, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	e.cancel = cancel
	e.sessionID = sessID
	e.mu.Unlock()
	e.running.Store(true)
	e.store.SetSessionStatus(sessID, store.SessionRunning)
	e.publishQueue()

	go func() {
		defer e.running.Store(false)
		stopped := e.run(ctx, sessID, opts, model)
		if opts.Night && !stopped {
			// 夜间评分模式：首轮结束后循环重试失败照片，直到全部成功/被停止/连续无进展
			stopped = e.nightLoop(ctx, sessID, opts, model)
		}
		status := store.SessionCompleted
		if stopped {
			status = store.SessionStopped
		}
		counts, _ := e.store.SessionStatusCounts(sessID)
		done := counts[store.StatusScored] + counts[store.StatusParseFail] + counts[store.StatusFailed] +
			counts[store.StatusBadImage] + counts[store.StatusUnsupported] + counts[store.StatusDuplicate]
		e.store.UpdateSessionStatus(sessID, status, 0, done)
		// 按实际评分数记账
		if c := e.estCost(model); c > 0 {
			scored := counts[store.StatusScored] + counts[store.StatusParseFail]
			e.store.SpendAdd(time.Now().Format("2006-01-02"), model, scored, c*float64(scored))
		}
		failed := counts[store.StatusFailed]
		if opts.Night {
			failed += counts[store.StatusParseFail] // 夜间模式目标两类清零
		}
		e.Bus.Publish(Event{Type: "done", Data: DonePayload{SessionID: sessID, Stopped: stopped, Failed: failed, Night: opts.Night}})
		e.Bus.Publish(Event{Type: "pools", Data: []PoolState{}}) // 清空池面板（任务已结束）
		e.listMu.Lock()
		e.listReady = false // 本任务结束：新扫描的照片留给下次续跑
		e.listMu.Unlock()
		// 先复位运行态再拉起排队任务：popAndRun 依赖 running=false 才会取任务，
		// 不能依赖 defer 的复位时机（那要等本 goroutine 返回，队列会永远卡住）
		e.paused.Store(false) // 暂停标记不跨任务
		e.running.Store(false)
		e.publishQueue()
		e.popAndRun()
	}()
}

// run 执行压缩与评分（单工作池：内联压缩缓存命中即跳过），返回是否被停止
func (e *Engine) run(ctx context.Context, sessID string, opts StartOpts, model string) bool {
	cfg := e.cfgFn()
	prov, err := provider.New(cfg)
	if err != nil {
		logutil.Error("构造 Provider 失败: %v", err)
		e.Bus.Publish(Event{Type: "error", Data: map[string]string{"error": err.Error()}})
		return false
	}
	eng, err := compress.NewEngine(cfg.Paths.LibDir, cfg.Pipeline.MaxEdge, cfg.Pipeline.JPEGQuality)
	if err != nil {
		logutil.Error("%v", err)
		e.Bus.Publish(Event{Type: "error", Data: map[string]string{"error": err.Error()}})
		return false
	}

	// 运行中的池集合提前建立（在照片清单加载之前）：后台扫描新登记的照片能立即
	// 追加进投料队列（workQueue 按 ID 去重，与稍后加载的清单不会重复），
	// 否则「加载完成 → 池建立」之间会有一个追加失手的窗口
	rp := &runPools{ready: newReadyList(), work: newWorkQueue(nil)}
	e.pools.Store(rp)
	defer e.pools.Store(nil)

	photos, _, err := e.store.ListPhotos(sessID, "", 0, 1<<20, "", "", "", "", -1, -1)
	if err != nil {
		logutil.Error("读取清单失败: %v", err)
		return false
	}
	// 标记清单已加载：此后后台扫描新登记的照片必须走「追加进队列」而不是等下次加载
	e.listMu.Lock()
	e.listReady = true
	e.listMu.Unlock()
	// 待处理队列：非重复且未完成；抽样限额
	var queue []*store.Photo
	for _, p := range photos {
		if p.Status == store.StatusDuplicate {
			continue
		}
		if isFinalStatus(p.Status) {
			continue
		}
		if opts.SampleN > 0 && len(queue) >= opts.SampleN {
			break
		}
		queue = append(queue, p)
	}
	// ---------- 阶段〇：评分缓存先行 ----------
	// 同指纹+模型+版本已评分过的照片直接复用结果，跳过压缩与调用
	// （追加照片到同一目录再评分时，老照片不会重新压缩）
	cacheDir := filepath.Join(cfg.Paths.DataDir, "work", "compressed")
	cacheHits := 0
	multiModel := len(opts.Models) > 1 || len(opts.CrossModels) > 0
	if cfg.Score.ReuseScores && !multiModel { // 多模型对比走池内逐模型缓存，不走单模型捷径
		var remaining []*store.Photo
		for _, p := range queue {
		if ent, err := e.store.CacheGet(p.Fingerprint, model, scorer.PromptVersion); err == nil {
			score := scorer.WeightedScore(ent.Dims, cfg.WeightsNormalized())
			// 压缩图若在共享缓存中存在则登记（老批次可能没有）。
			// 必须先登记再写评分结果：SetPhotoCompressed 会把状态置为 compressed，
			// 顺序颠倒会把已评分状态覆盖掉，导致归档/统计/图库全部漏掉这张
			cp := filepath.Join(cacheDir, compress.CacheName(p.Fingerprint))
			if _, serr := os.Stat(cp); serr == nil {
				e.store.SetPhotoCompressed(p.ID, cp)
			}
			e.store.SetPhotoResult(p.ID, store.StatusScored, score, &ent.Dims, ent.Tags, ent.Strength, ent.Weakness, model, scorer.PromptVersion, "cache", false, 0)
			if agg := e.RecomputeAggregate(p.Fingerprint); agg > 0 {
				score = agg // 合议模式：缓存命中的单模型分被平均分覆盖
			}
			// 与 scoreOne 的缓存命中路径保持一致：命中也计入多模型评分历史
			e.store.SavePhotoModelScore(p.ID, model, scorer.PromptVersion, score, &ent.Dims, ent.Tags, ent.Strength, ent.Weakness, "cache")
			e.Bus.Publish(Event{Type: "progress", Data: Progress{PhotoID: p.ID, SessionID: sessID, File: p.Filename, Score: score, Status: store.StatusScored, Cached: true}})
				cacheHits++
				continue
			}
			remaining = append(remaining, p)
		}
		queue = remaining
		if cacheHits > 0 {
			logutil.Info("评分缓存命中 %d 张，跳过压缩与调用", cacheHits)
		}
	}

	// ---------- 阶段一+二：压缩→评分流水线（双池并行） ----------
	// 压缩池每压完一张立即投喂评分池，评分池有空闲 worker 就开始打分，
	// 不再等全部压缩完成——大批次时压缩与评分重叠执行，压出第一张即进入评分。
	// 不支持的格式（RAF/HEIC 等）直接标记跳过，不进压缩队列
	var compressQueue []*store.Photo
	for _, p := range queue {
		if compress.UnsupportedExts[strings.ToLower(filepath.Ext(p.SrcPath))] {
			e.store.SetPhotoStatus(p.ID, store.StatusUnsupported, "不支持的格式（RAW/HEIC），已跳过")
			e.Bus.Publish(Event{Type: "progress", Data: Progress{PhotoID: p.ID, SessionID: sessID, File: p.Filename, Status: store.StatusUnsupported, Error: "不支持的格式（RAW/HEIC），已跳过"}})
			continue
		}
		compressQueue = append(compressQueue, p)
	}
	totalAll := cacheHits + len(compressQueue) // 评分阶段进度分母含缓存命中
	if len(compressQueue) > 0 {
		e.Bus.Publish(Event{Type: "stage", Data: Stage{SessionID: sessID, Stage: "compress", Total: len(compressQueue)}})
	}
	// 评分阶段事件：第一张图喂进评分池时切换；全缓存命中批次没有压缩环节，直接宣告
	var scoreStageOnce sync.Once
	publishScoreStage := func() {
		scoreStageOnce.Do(func() {
			e.Bus.Publish(Event{Type: "stage", Data: Stage{SessionID: sessID, Stage: "score", Total: totalAll}})
		})
	}
	if len(compressQueue) == 0 && totalAll > 0 {
		publishScoreStage()
	}
	// 评分目标：当前平台的模型 + 跨平台预设的模型（首个为主模型，入照片正记录）
	targets := []scoreTarget{{prov: prov, model: model, label: model}}
	for _, m := range opts.Models {
		if m != model {
			targets = append(targets, scoreTarget{prov: prov, model: m, label: m})
		}
	}
	for _, cm := range opts.CrossModels {
		cp, err := e.providerForPreset(cm.Preset)
		if err != nil {
			logutil.Error("跨平台模型 %s/%s 构造 Provider 失败: %v", cm.Preset, cm.Model, err)
			continue
		}
		targets = append(targets, scoreTarget{prov: cp, model: cm.Model, label: cm.Preset + "/" + cm.Model})
	}
	return e.streamPools(ctx, rp, eng, targets, compressQueue, cacheDir, sessID, totalAll, publishScoreStage)
}

// recomputeAggregate 多专家合议：按内容指纹收集全部评分记录，每个模型只取
// 最新一条（多次评测取最新），用当前权重从维度分重算后取平均，作为该图的正式
// 分数（单一模型的偏差被其余模型稀释）。同指纹的全部照片（跨批次）一并更新。
// aggregate=primary 时不动（保持主模型分数）。返回平均分（未启用返回 0）。
func (e *Engine) RecomputeAggregate(fingerprint string) float64 {
	cfg := e.cfgFn()
	if cfg.Score.Aggregate != "average" {
		return 0
	}
	records, err := e.store.PhotoScoresByFingerprint(fingerprint)
	if err != nil || len(records) == 0 {
		return 0
	}
	w := cfg.WeightsNormalized()
	// 每模型最新一条（记录已按 created_at DESC, id DESC 排序）
	latest := map[string]*store.PhotoModelScore{}
	for _, r := range records {
		if _, ok := latest[r.Model]; !ok {
			latest[r.Model] = r
		}
	}
	var sum float64
	n := 0
	for _, r := range latest {
		if r.Dims == nil {
			continue
		}
		sum += scorer.WeightedScore(*r.Dims, w)
		n++
	}
	if n == 0 {
		return 0
	}
	avg := round1(sum / float64(n))
	e.store.UpdatePhotoScoresByFingerprint(fingerprint, avg)
	return avg
}

// providerForPreset 按预设名构造该平台的 Provider（跨平台对比用）
func (e *Engine) providerForPreset(name string) (provider.Provider, error) {
	cfg := e.cfgFn()
	for _, p := range cfg.Presets {
		if p.Name == name {
			pc := *cfg
			pc.Provider = config.Provider{BaseURL: p.BaseURL, APIKey: p.APIKey, Protocol: p.Protocol}
			if p.Type != "" {
				pc.Provider.Type = p.Type
			} else {
				pc.Provider.Type = "tokenrhythm"
			}
			return provider.New(&pc)
		}
	}
	return nil, fmt.Errorf("预设不存在: %s", name)
}

// streamPools 压缩池 + n 个模型池并行：压缩池把照片压进内容寻址缓存并推入共享清单，
// 每个模型池用独立游标消费整份清单、独立并发与生命周期（暂停/停止/额度故障互不影响）。
// 一张照片在其「投递时活跃的池」都有结果（或该池被停止/致命）后定稿入正记录并计进度；
// 运行中新增的池从第一张起评（复用压缩缓存，不重新压缩），只并入合议平均、不阻塞定稿。
// 暂停语义：整任务暂停在投料口与各池取数前挂起；单池暂停只挂起该池。
// 停止语义：ctx 取消后各环节退出，未评分照片留在原状态供续跑。
// 返回是否被停止。
func (e *Engine) streamPools(ctx context.Context, rp *runPools, eng *compress.Engine, targets []scoreTarget,
	compressQueue []*store.Photo, cacheDir, sessID string, totalAll int, publishScoreStage func()) bool {

	ready := rp.ready
	work := rp.work // 压缩投料队列（run 阶段已建立：后台扫描的照片可能已在里面）
	scDesired := atomic.Int64{}
	ccDesired := atomic.Int64{}
	compressJobs := make(chan *store.Photo) // 无缓冲：投料节奏跟随压缩 worker，动态扩容窗口覆盖整个压缩阶段

	var stopFlag atomic.Bool
	var compressWG sync.WaitGroup
	var compressN atomic.Int64
	var compressInflight atomic.Int64 // 已从投料队列取出、尚未压完（成功或失败）的张数
	var compressClosed bool
	var poolMu sync.Mutex // 保护 compressClosed / 压缩池扩容
	doneCount := atomic.Int64{}

	// 本批次的照片入队（按 ID 去重：与 run 阶段后台扫描提前追加的不重复）
	if added := work.push(compressQueue...); added > 0 {
		rp.mu.Lock()
		rp.planned += int64(added)
		rp.mu.Unlock()
		rp.compress.total.Add(int64(added))
	}

	// 照片完成度：required = 投递时活跃的池标签（后加的池不阻塞已投递照片定稿）
	type photoState struct {
		mu        sync.Mutex
		p         *store.Photo
		outcomes  map[string]modelOutcome
		required  map[string]bool
		finalized bool
		start     time.Time
	}
	var statesMu sync.Mutex
	states := map[int64]*photoState{}

	// poolByLabel 查池（锁序：持 st.mu 时可取 rp.mu，反向禁止）
	poolByLabel := func(label string) *modelPool {
		rp.mu.Lock()
		defer rp.mu.Unlock()
		for _, p := range rp.pools {
			if p.label == label {
				return p
			}
		}
		return nil
	}
	// satisfied 该照片要求的池是否都已满足（有结果，或池已停止/致命）——需持 st.mu
	satisfied := func(st *photoState) bool {
		for label := range st.required {
			if _, ok := st.outcomes[label]; ok {
				continue
			}
			if p := poolByLabel(label); p != nil && p.halted() {
				continue
			}
			return false
		}
		return true
	}

	// finalize 定稿：主池结果优先入正记录，主池缺席（停止/失败）时用任一可用结果
	finalize := func(st *photoState) {
		p := st.p
		idx := doneCount.Add(1)
		cfg := e.cfgFn()
		// 取用顺序：主池成功 → 其它池成功 → 主池失败 → 其它池失败。
		// 只要有一个模型评成功，就用它（多专家合议：个别专家失败/片面不影响整体），
		// 正式分再由 RecomputeAggregate 覆盖为各模型平均。
		var primary modelOutcome
		label := ""
		ok := false
		if o, has := st.outcomes[rp.primaryLabel]; has && o.status == store.StatusScored {
			primary, label, ok = o, rp.primaryLabel, true
		}
		if !ok {
			for l, o := range st.outcomes {
				if o.status == store.StatusScored {
					primary, label, ok = o, l, true
					break
				}
			}
		}
		if !ok {
			if o, has := st.outcomes[rp.primaryLabel]; has {
				primary, label, ok = o, rp.primaryLabel, true
			}
			if !ok {
				for l, o := range st.outcomes {
					primary, label, ok = o, l, true
					break
				}
			}
		}
		if !ok {
			e.store.SetPhotoStatus(p.ID, store.StatusFailed, "所有模型池均未产出评分（池被停止或调用失败），可重跑")
			e.Bus.Publish(Event{Type: "progress", Data: Progress{PhotoID: p.ID, SessionID: sessID, Index: int(idx), Total: totalAll, File: p.Filename, Status: store.StatusFailed, Error: "所有模型池均未产出评分"}})
			return
		}
		switch primary.status {
		case store.StatusScored:
			score := scorer.WeightedScore(*primary.dims, cfg.WeightsNormalized())
			src := "api"
			if primary.cached {
				src = "cache"
			}
			e.store.SetPhotoResult(p.ID, store.StatusScored, score, primary.dims, primary.tags, primary.strength, primary.weakness, label, scorer.PromptVersion, src, primary.clamped, time.Since(st.start).Milliseconds())
			// 合议模式：正式分 = 各模型最新评分的平均（覆盖主模型分数）
			if agg := e.RecomputeAggregate(p.Fingerprint); agg > 0 {
				score = agg
			}
			e.Bus.Publish(Event{Type: "progress", Data: Progress{PhotoID: p.ID, SessionID: sessID, Index: int(idx), Total: totalAll, File: p.Filename, Score: score, Status: store.StatusScored, Cached: primary.cached}})
		case store.StatusParseFail:
			e.store.SetPhotoResult(p.ID, store.StatusParseFail, 0, nil, nil, "", "", label, scorer.PromptVersion, "api", false, time.Since(st.start).Milliseconds())
			e.store.SetPhotoStatus(p.ID, store.StatusParseFail, truncate(primary.errDetail, 500))
			e.Bus.Publish(Event{Type: "progress", Data: Progress{PhotoID: p.ID, SessionID: sessID, Index: int(idx), Total: totalAll, File: p.Filename, Status: store.StatusParseFail, Error: truncate(primary.errDetail, 200)}})
		case store.StatusBadImage:
			e.store.SetPhotoStatus(p.ID, store.StatusBadImage, truncate(primary.errDetail, 300))
			e.Bus.Publish(Event{Type: "progress", Data: Progress{PhotoID: p.ID, SessionID: sessID, Index: int(idx), Total: totalAll, File: p.Filename, Status: store.StatusBadImage, Error: truncate(primary.errDetail, 120)}})
		default: // failed
			e.store.SetPhotoStatus(p.ID, store.StatusFailed, truncate(primary.errDetail, 300))
			e.Bus.Publish(Event{Type: "progress", Data: Progress{PhotoID: p.ID, SessionID: sessID, Index: int(idx), Total: totalAll, File: p.Filename, Status: store.StatusFailed, Error: truncate(primary.errDetail, 120)}})
		}
	}

	// spawnPoolLocked 起一个池 worker（需持 rp.mu）：与收尾判定（同持 rp.mu）互斥，
	// 保证刚追加的池不会被漏算成 0 worker。返回 false 表示任务已收尾。
	spawnPoolLocked := func(pool *modelPool) bool {
		if rp.closed {
			return false
		}
		pool.workers.Add(1) // 先计数再起 goroutine
		go func() {
			defer pool.workers.Add(-1)
			for {
				if ctx.Err() != nil || pool.stopped.Load() || pool.fatal.Load() {
					return
				}
				// 清单取尽即退出（暂停中也退出，避免任务永远等不到收尾）
				if ready.finished() && int(pool.cursor.Load()) >= ready.size() {
					pool.endAt.Store(time.Now().UnixNano())
					return
				}
				if pool.paused.Load() {
					time.Sleep(200 * time.Millisecond)
					continue
				}
				i := int(pool.cursor.Add(1) - 1)
				p, ok := ready.at(ctx, i, &pool.stopped)
				if !ok {
					return
				}
				pool.inflight.Add(1)
				cp := filepath.Join(cacheDir, compress.CacheName(p.Fingerprint))
				o := e.scoreModel(ctx, pool, p, cp)
				pool.inflight.Add(-1)
				if o.status == store.StatusScored {
					pool.consec.Store(0)
					pool.done.Add(1)
				} else {
					pool.consec.Add(1)
					pool.fail.Add(1)
				}
				// 先记录本次结果并判定定稿（暂停/出错路径也不能丢这一张）
				statesMu.Lock()
				st := states[p.ID]
				statesMu.Unlock()
				if st != nil {
					st.mu.Lock()
					st.outcomes[pool.label] = o
					already := st.finalized
					last := !already && satisfied(st)
					if last {
						st.finalized = true
					}
					st.mu.Unlock()
					switch {
					case last:
						finalize(st)
					case already && o.status == store.StatusScored:
						// 后加入的池：照片已定稿，只把新评分并入合议平均
						e.RecomputeAggregate(p.Fingerprint)
					}
				}
				// 再看是否要自动暂停本池（模型不存在/额度耗尽/连续失败）——
				// 暂停保留继续的机会，用户可点 ▶ 恢复或 ■ 停止
				if o.fatal {
					pool.pauseWith(o.errDetail)
					e.Bus.Publish(Event{Type: "error", Data: map[string]string{"error": fmt.Sprintf("模型池 %s 已自动暂停：%s——可点 ▶ 继续或 ■ 停止该池，其它池不受影响", pool.label, o.errDetail)}})
					e.publishPools()
					continue
				}
				if pool.consec.Load() >= poolConsecFailLimit {
					pool.pauseWith(fmt.Sprintf("连续 %d 次调用失败（疑似额度不足或平台故障）", poolConsecFailLimit))
					e.Bus.Publish(Event{Type: "error", Data: map[string]string{"error": fmt.Sprintf("模型池 %s 已自动暂停：连续 %d 次调用失败（疑似额度不足或平台故障）——可点 ▶ 继续或 ■ 停止该池，其它池不受影响", pool.label, poolConsecFailLimit)}})
					e.publishPools()
					continue
				}
				if pool.workers.Load() > scDesired.Load() {
					return // 并发被调小：跑完当前这张再退出
				}
			}
		}()
		return true
	}
	spawnPool := func(pool *modelPool) bool {
		rp.mu.Lock()
		defer rp.mu.Unlock()
		return spawnPoolLocked(pool)
	}
	rp.spawn = spawnPoolLocked

	// 初始模型池（首个为主池）
	rp.mu.Lock()
	for i, tg := range targets {
		pool := &modelPool{prov: tg.prov, model: tg.model, label: tg.label, primary: i == 0, started: time.Now()}
		pool.scale.Store(1)
		rp.pools = append(rp.pools, pool)
	}
	if len(targets) > 0 {
		rp.primaryLabel = targets[0].label
	}
	rp.mu.Unlock()

	// 压缩池 worker：压完一张推入共享清单（失败标 bad_image，不进清单）
	spawnCompress := func() {
		compressN.Add(1)
		compressWG.Add(1)
		go func() {
			defer compressWG.Done()
			defer compressN.Add(-1)
			for p := range compressJobs {
				if ctx.Err() != nil {
					compressInflight.Add(-1)
					stopFlag.Store(true)
					continue
				}
				cp, err := eng.ToCache(p.SrcPath, p.Fingerprint, cacheDir)
				compressInflight.Add(-1)
				if err != nil {
					logutil.Info("压缩失败 %s: %v", p.Filename, err)
					e.store.SetPhotoStatus(p.ID, store.StatusBadImage, truncate(err.Error(), 300))
					e.Bus.Publish(Event{Type: "progress", Data: Progress{PhotoID: p.ID, SessionID: sessID, File: p.Filename, Status: store.StatusBadImage, Error: truncate(err.Error(), 120)}})
					rp.compress.done.Add(1)
					continue
				}
				e.store.SetPhotoCompressed(p.ID, cp)
				e.Bus.Publish(Event{Type: "progress", Data: Progress{PhotoID: p.ID, SessionID: sessID, File: p.Filename, Status: store.StatusCompressed}})
				publishScoreStage()
				// 登记完成度（required=当前活跃池）+ 推入共享清单
				required := map[string]bool{}
				rp.mu.Lock()
				for _, pool := range rp.pools {
					required[pool.label] = true
				}
				rp.mu.Unlock()
				statesMu.Lock()
				states[p.ID] = &photoState{p: p, outcomes: map[string]modelOutcome{}, required: required, start: time.Now()}
				statesMu.Unlock()
				ready.push(p)
				rp.compress.done.Add(1)
				if compressN.Load() > ccDesired.Load() {
					return // 并发被调小：处理完当前这张再退出
				}
			}
		}()
	}

	// 动态并发 + 池状态推送：每秒对齐配置、扫描因池停止而可定稿的照片
	adjust := func() {
		sc := int64(e.cfgFn().Pipeline.ScoreConcurrency)
		cc := int64(e.cfgFn().Pipeline.CompressConcurrency)
		scDesired.Store(sc)
		ccDesired.Store(cc)
		rp.mu.Lock()
		pools := append([]*modelPool(nil), rp.pools...)
		closed := rp.closed
		rp.mu.Unlock()
		if !closed {
			for _, pool := range pools {
				if pool.halted() {
					continue
				}
				for pool.workers.Load() < sc {
					if !spawnPool(pool) {
						break
					}
				}
			}
		}
		poolMu.Lock()
		if !compressClosed {
			for compressN.Load() < cc {
				spawnCompress()
			}
		}
		poolMu.Unlock()
		e.publishPools()
		// 池停止/致命后其未完成照片应可定稿：扫一遍未定稿的
		var todo []*photoState
		statesMu.Lock()
		for _, st := range states {
			st.mu.Lock()
			if !st.finalized && satisfied(st) {
				st.finalized = true
				todo = append(todo, st)
			}
			st.mu.Unlock()
		}
		statesMu.Unlock()
		for _, st := range todo {
			finalize(st)
		}
	}
	adjust() // 主流程同步起满（先于投料与收尾）
	supQuit := make(chan struct{})
	supDone := make(chan struct{})
	go func() {
		defer close(supDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-supQuit:
				return
			case <-time.After(time.Second):
				adjust()
			}
		}
	}()

	// 投料：从可追加的投料队列逐张派发给压缩池（暂停时挂起派发）。
	// 运行中并入的新目录照片会陆续追加进来，压缩池接着压、模型池接着评。
	producerQuit := make(chan struct{})
	var producerWG sync.WaitGroup
	producerWG.Add(1)
	go func() {
		defer producerWG.Done()
		for {
			if !e.waitIfPaused(ctx) {
				stopFlag.Store(true)
				return
			}
			p, ok := work.next(ctx, &stopFlag, producerQuit)
			if !ok {
				return
			}
			compressInflight.Add(1)
			select {
			case <-ctx.Done():
				stopFlag.Store(true)
				return
			case <-producerQuit:
				return
			case compressJobs <- p:
			}
		}
	}()
	// 收尾判定：投料队列取尽（且无正在并入的目录）+ 无「已取出未压完」+ 各池都取到
	// 清单末尾（停止/致命的池不等）。运行中并入新目录会自然把批次延长。
	drained := func() bool {
		if ctx.Err() != nil {
			return true
		}
		if e.isScanning(sessID) {
			return false // 后台还在扫描：等它把剩余照片并进来
		}
		if !work.empty() || compressInflight.Load() > 0 {
			return false
		}
		rp.mu.Lock()
		defer rp.mu.Unlock()
		sz := int64(ready.size())
		for _, pool := range rp.pools {
			if pool.halted() {
				continue
			}
			if pool.inflight.Load() > 0 || pool.cursor.Load() < sz {
				return false
			}
		}
		return true
	}
	for !drained() {
		if ctx.Err() != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	close(producerQuit)
	producerWG.Wait()
	poolMu.Lock()
	compressClosed = true
	close(compressJobs)
	poolMu.Unlock()
	compressWG.Wait()
	rp.compress.finished.Store(true)
	ready.finish()
	for {
		rp.mu.Lock()
		alive := 0
		for _, pool := range rp.pools {
			if pool.workers.Load() > 0 {
				alive++
			}
		}
		if alive == 0 {
			rp.closed = true // 与 spawnPool 互斥：此后不再起新 worker
			rp.mu.Unlock()
			break
		}
		rp.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
	close(supQuit)
	<-supDone
	// 停止判定只看 ctx：喂料早已结束时没人再置 stopFlag，用 ctx 才准
	// （ctx 只由 Stop/中止取消，正常跑完不会被取消）
	return ctx.Err() != nil
}

func isFinalStatus(s string) bool {
	switch s {
	case store.StatusScored, store.StatusParseFail, store.StatusFailed,
		store.StatusBadImage, store.StatusUnsupported:
		return true
	}
	return false
}

// modelOutcome 单模型评分结果（scoreModel 返回）
type modelOutcome struct {
	dims      *store.Dims
	tags      []string
	strength  string
	weakness  string
	status    string // scored | parse_fail | failed | bad_image
	errDetail string
	cached    bool
	clamped   bool
	fatal     bool // 该模型的致命错误（模型不存在/额度耗尽）：只停本池，不影响其它池
}

// scoreModel 用单个模型池评一张（缓存优先，含重试退避）。
// label 为入库标签（缓存/历史键）：同平台=模型 ID，跨平台="预设/模型"，
// 避免不同平台的同名模型在缓存与历史里互相覆盖。
// 429 退避倍率按池隔离（某个平台限流不影响其它池）。
// 成功时写评分缓存与多模型历史，不写照片正记录（由调用方决定主模型结果）
func (e *Engine) scoreModel(ctx context.Context, pool *modelPool, p *store.Photo, compressedPath string) modelOutcome {
	cfg := e.cfgFn()
	prov, model, label := pool.prov, pool.model, pool.label
	// 跨会话缓存命中（0 计费）
	if cfg.Score.ReuseScores {
		if ent, err := e.store.CacheGet(p.Fingerprint, label, scorer.PromptVersion); err == nil {
			d := ent.Dims
			return modelOutcome{dims: &d, tags: ent.Tags, strength: ent.Strength, weakness: ent.Weakness, status: store.StatusScored, cached: true}
		}
	}
	b64, err := readB64(compressedPath)
	if err != nil {
		return modelOutcome{status: store.StatusBadImage, errDetail: truncate(err.Error(), 300)}
	}
	req := provider.ScoreRequest{
		ImageB64:  b64,
		Filename:  p.Filename,
		Prompt:    scorer.BuildPrompt(),
		Temp:      cfg.Score.Temperature,
		MaxTokens: cfg.Score.MaxTokens,
		Timeout:   time.Duration(cfg.Score.TimeoutSec) * time.Second,
		Effort:    cfg.Score.ReasoningEffort,
	}
	var content string
	attempts := 0
	for {
		attempts++
		content, err = prov.Score(ctx, model, req)
		if err == nil || errors.Is(err, provider.ErrModelNoVision) || errors.Is(err, provider.ErrModelNotFound) ||
			errors.Is(err, provider.ErrQuotaExhausted) || ctx.Err() != nil || attempts >= 3 {
			break
		}
		backoff := time.Duration(1<<attempts) * time.Second // 2s / 4s
		if errors.Is(err, provider.ErrRateLimit) {
			backoff *= time.Duration(pool.scale.Load())
			// 收到限流：本池降速（其它池不受影响），成功后无需恢复（评分结束后进程内自然复位）
			pool.scale.Add(1)
		}
		logutil.Info("重试 %s（第 %d 次）: %v", p.Filename, attempts, err)
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
	}
	if err != nil {
		if errors.Is(err, provider.ErrModelNoVision) {
			err = fmt.Errorf("%w（请在模型下拉更换视觉模型）", err)
		}
		if errors.Is(err, provider.ErrModelNotFound) {
			// 模型在当前平台不存在：重试无意义，只停本池
			return modelOutcome{status: store.StatusFailed, fatal: true,
				errDetail: truncate(fmt.Sprintf("模型 %s 在当前平台不存在或无权限：%s", model, err.Error()), 300)}
		}
		if errors.Is(err, provider.ErrQuotaExhausted) {
			// 额度耗尽/无权限：重试无意义，只停本池
			return modelOutcome{status: store.StatusFailed, fatal: true,
				errDetail: truncate(fmt.Sprintf("模型 %s 额度不足或无权限：%s", model, err.Error()), 300)}
		}
		return modelOutcome{status: store.StatusFailed, errDetail: truncate(err.Error(), 300)}
	}
	dims, tags, strength, weakness, clamped, perr := scorer.Parse(content)
	if perr != nil {
		// 解析失败不伪造分数：标记待复检，并把模型原始输出片段存入 error 便于排查
		frag := summarizeContent(content, 220)
		if strings.TrimSpace(content) == "" {
			frag = "（模型返回了空内容——可能是安全拦截、max_tokens 截断或平台异常，可更换模型重试）"
		}
		return modelOutcome{status: store.StatusParseFail, errDetail: truncate(fmt.Sprintf("%s ｜ 模型输出片段: %s", perr.Error(), frag), 500)}
	}
	// 成功：写评分缓存 + 多模型历史（每次 API 评分追加一条，切换模型可对比）
	e.store.CachePut(p.Fingerprint, label, scorer.PromptVersion, dims, tags, strength, weakness)
	score := scorer.WeightedScore(dims, cfg.WeightsNormalized())
	e.store.SavePhotoModelScore(p.ID, label, scorer.PromptVersion, score, &dims, tags, strength, weakness, "api")
	return modelOutcome{dims: &dims, tags: tags, strength: strength, weakness: weakness, status: store.StatusScored, clamped: clamped}
}

// summarizeContent 压缩模型原始输出为单行片段（去换行/代码块标记），用于失败详情展示
func summarizeContent(s string, n int) string {
	s = strings.ReplaceAll(s, "```", "'")
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func readB64(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// Stop 停止当前任务（已提交请求跑完，不再新增）
func (e *Engine) Stop() bool {
	stopped := false
	e.mu.Lock()
	if e.running.Load() {
		if e.cancel != nil {
			e.cancel()
			stopped = true
		}
	}
	// 同时清空排队任务（用户预期：停止 = 全部停止）
	e.queued = nil
	e.mu.Unlock()
	e.publishQueue()
	if !stopped {
		logutil.Info("停止请求：当前无运行中任务")
	}
	return stopped
}

// ---------- 归档（阶段二） ----------

// ArchiveSummary 归档结果
type ArchiveSummary struct {
	SessionID string         `json:"session_id"`
	Mode      string         `json:"mode"`
	Placed    int            `json:"placed"`
	Skipped   int            `json:"skipped"`
	Failed    int            `json:"failed"`
	Raws      int            `json:"raws"` // 连带归档的同名 RAW/HEIC 伴生文件数
	Dir       string         `json:"dir"`
	Buckets   map[string]int `json:"buckets"`
	ReportCSV string         `json:"report_csv"`
	Errors    []string       `json:"errors,omitempty"`
}

// ScoreBucket 分值归档分桶：<6.0 一桶；6.0-6.5 / 6.6-7.0 / 7.1-7.5 各一桶；
// 7.6 起每 0.1 分一桶（7.6、7.7 … 10.0）
func ScoreBucket(score float64) string {
	switch {
	case score < 6.0:
		return "6.0以下"
	case score <= 6.5:
		return "6.0-6.5"
	case score <= 7.0:
		return "6.6-7.0"
	case score <= 7.5:
		return "7.1-7.5"
	default:
		return fmt.Sprintf("%.1f", score)
	}
}

// ScoreArchive 分值归档：把指定照片按分值分桶移动/复制到目标目录下的分值文件夹
// （6.0-6.5、6.6-7.0、7.1-7.5，7.6 起每 0.1 一档）。只处理有分数的照片；已归档过的
// 跳过。destDir 为空时按源图所在盘推导（<源图父目录>\SnapRank），避免默认塞满 C 盘。
func (e *Engine) ScoreArchive(ids []int64, mode archive.Mode, destDir string) (*ArchiveSummary, error) {
	if e.IsRunning() {
		return nil, errors.New("任务运行中，请先停止或等待完成")
	}
	if !archive.ValidMode(mode) {
		return nil, fmt.Errorf("非法归档方式: %s", mode)
	}
	if len(ids) == 0 {
		return nil, errors.New("未选择要归档的照片")
	}
	// 目标目录：优先用传入值；否则按第一张照片的源目录推导到同级 SnapRank（同一块盘）
	root := strings.TrimSpace(destDir)
	if root == "" {
		if p, err := e.store.GetPhoto(ids[0]); err == nil && p != nil && p.SrcPath != "" {
			root = filepath.Join(filepath.Dir(filepath.Dir(p.SrcPath)), "SnapRank")
		}
	}
	if root == "" {
		root = e.cfgFn().Paths.ArchiveRoot
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	sum := &ArchiveSummary{Mode: string(mode), Buckets: map[string]int{}, Dir: root}
	for _, id := range ids {
		p, err := e.store.GetPhoto(id)
		if err != nil || p == nil {
			sum.Failed++
			continue
		}
		if p.Status != store.StatusScored {
			continue // 无分数（解析失败/失败等）不参与分值归档
		}
		if p.ArchivedPath != "" {
			sum.Skipped++
			continue
		}
		bucket := ScoreBucket(p.Score)
		dest, skipped, perr := archive.Place(root, bucket, p.SrcPath, p.Fingerprint, mode)
		if perr != nil {
			sum.Failed++
			sum.Errors = append(sum.Errors, fmt.Sprintf("%s: %v", p.Filename, perr))
			continue
		}
			if skipped {
				sum.Skipped++
			} else {
				sum.Placed++
			}
			sum.Buckets[bucket]++
			e.store.SetPhotoArchived(p.ID, dest)
			// 同名 RAW/HEIC 伴生文件跟随主图一起归档（保持成对命名）
			moved, _, rerrs := archive.PlaceRaws(dest, p.SrcPath, mode)
			sum.Raws += moved
			sum.Errors = append(sum.Errors, rerrs...)
		}
		if sum.Placed == 0 && sum.Skipped == 0 {
		return nil, errors.New("没有可归档的照片（需要有分数且源文件存在）")
	}
	return sum, nil
}

// RestoreSummary 归档还原结果
type RestoreSummary struct {
	Dir      string   `json:"dir"`      // 从哪个目录还原
	Scanned  int      `json:"scanned"`  // 该目录下扫描到的图片数
	Restored int      `json:"restored"` // 按内容匹配并移回原目录的张数
	Raws     int      `json:"raws"`     // 连带还原的同名 RAW/HEIC 伴生文件数
	Same     int      `json:"same"`     // 原目录已有同内容文件（视为已还原）
	Conflict int      `json:"conflict"` // 原目录已有同名不同内容：改名放回
	NotFound int      `json:"not_found"`// 在该目录里没找到对应文件的已归档照片数
	Failed   int      `json:"failed"`
	Errors   []string `json:"errors,omitempty"`
}

// RestoreArchive 还原归档：把 dir 下的图片按**内容指纹**匹配已归档记录，移回各自
// 原始源目录（src_path）。按内容匹配可正确处理「已改名」「同名不同内容」「整个归档
// 目录被搬走」等情况。还原成功后清掉 archived_path，照片重新出现在图库。
func (e *Engine) RestoreArchive(dir string) (*RestoreSummary, error) {
	if e.IsRunning() {
		return nil, errors.New("任务运行中，请先停止或等待完成")
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = e.cfgFn().Paths.ArchiveRoot
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("目录不存在或不可读: %s", dir)
	}
	photos, err := e.store.ArchivedPhotos()
	if err != nil {
		return nil, err
	}
	sum := &RestoreSummary{Dir: dir}
	if len(photos) == 0 {
		return sum, nil
	}
	// 指纹 → 待还原照片（同一指纹的多条记录：一个文件只能还原一条）
	byFP := map[string][]*store.Photo{}
	for _, p := range photos {
		byFP[p.Fingerprint] = append(byFP[p.Fingerprint], p)
	}
	restoreOne := func(path string, p *store.Photo) {
		dest := p.SrcPath
		if dest == "" {
			sum.Failed++
			sum.Errors = append(sum.Errors, fmt.Sprintf("%s: 记录缺少原始路径", filepath.Base(path)))
			return
		}
		if _, err := os.Stat(dest); err == nil {
			if archive.SameContent(dest, p.Fingerprint) {
				sum.Same++ // 原目录已有同内容文件：清掉归档标记即可
				e.store.SetPhotoArchived(p.ID, "")
				// 伴生文件若还留在归档目录，一并移回
				n, rerrs := archive.RestoreRaws(path, dest)
				sum.Raws += n
				sum.Errors = append(sum.Errors, rerrs...)
				return
			}
			dest = archive.UniqueName(filepath.Dir(dest), dest) // 同名不同内容：改名放回
			sum.Conflict++
		}
		if err := archive.MoveTo(path, dest); err != nil {
			sum.Failed++
			sum.Errors = append(sum.Errors, fmt.Sprintf("%s: %v", filepath.Base(path), err))
			return
		}
		e.store.SetPhotoArchived(p.ID, "")
		sum.Restored++
		// 同名 RAW/HEIC 伴生文件跟随主图一起还原（命名跟随还原后的主图名）
		n, rerrs := archive.RestoreRaws(path, dest)
		sum.Raws += n
		sum.Errors = append(sum.Errors, rerrs...)
	}
	filepath.Walk(dir, func(path string, info os.FileInfo, werr error) error {
		if werr != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !compress.SupportedExts[ext] && !compress.UnsupportedExts[ext] {
			return nil
		}
		fpv, ferr := fp.OfFile(path)
		if ferr != nil {
			return nil
		}
		sum.Scanned++
		list := byFP[fpv]
		if len(list) == 0 {
			return nil
		}
		p := list[0]
		byFP[fpv] = list[1:]
		restoreOne(path, p)
		return nil
	})
	for _, rest := range byFP {
		sum.NotFound += len(rest)
	}
	return sum, nil
}

// Archive 执行阶段二：按确认的方式复制/移动到档位目录并导出报告；sessionID 为空取最近会话
func (e *Engine) Archive(mode archive.Mode, sessionID string) (*ArchiveSummary, error) {
	if e.IsRunning() {
		return nil, errors.New("任务运行中，请先停止或等待完成")
	}
	if !archive.ValidMode(mode) {
		return nil, fmt.Errorf("非法归档方式: %s", mode)
	}
	// 合并视图：归档全部批次中未归档的评分照片（各批次仍落到各自的 <批次ID>/ 目录）
	if sessionID == SessionAll {
		return e.archiveAll(mode)
	}
	sess, err := e.resolveSession(sessionID)
	if err != nil {
		return nil, err
	}
	return e.archiveOne(mode, sess.ID)
}

// archiveAll 聚合全部会话的归档结果；没有评分照片的会话跳过
func (e *Engine) archiveAll(mode archive.Mode) (*ArchiveSummary, error) {
	list, err := e.store.ListSessions()
	if err != nil {
		return nil, err
	}
	agg := &ArchiveSummary{Mode: string(mode), Buckets: map[string]int{}, Dir: e.cfgFn().Paths.ArchiveRoot}
	archived := 0
	for _, sess := range list {
		sum, err := e.archiveOne(mode, sess.ID)
		if err != nil {
			continue // 该会话没有可归档的评分结果
		}
		archived++
		agg.Placed += sum.Placed
		agg.Skipped += sum.Skipped
		agg.Failed += sum.Failed
		agg.Raws += sum.Raws
		agg.Errors = append(agg.Errors, sum.Errors...)
		for b, n := range sum.Buckets {
			agg.Buckets[b] += n
		}
	}
	if archived == 0 {
		return nil, errors.New("没有可归档的评分结果")
	}
	return agg, nil
}

// archiveOne 归档单个会话（报告 CSV 写在各自 <归档根>/<批次ID>/ 下）
func (e *Engine) archiveOne(mode archive.Mode, sessID string) (*ArchiveSummary, error) {
	photos, err := e.store.ScoredPhotos(sessID)
	if err != nil {
		return nil, err
	}
	if len(photos) == 0 {
		return nil, errors.New("该会话没有可归档的评分结果")
	}
	cfg := e.cfgFn()
	sessionDir := filepath.Join(cfg.Paths.ArchiveRoot, sessID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return nil, err
	}

	sum := &ArchiveSummary{SessionID: sessID, Mode: string(mode), Buckets: map[string]int{}, Dir: sessionDir}
	var rows []*archive.ReportRow
	for _, p := range photos {
		bucket := scorer.BucketOf(p.Score, p.Status == store.StatusParseFail, cfg.Score.Thresholds, p.OverrideBucket)
		if p.ArchivedPath != "" {
			sum.Skipped++
			continue
		}
		// parse_fail：不伪造分数；复制到待复检目录便于人工检查
		if p.Status == store.StatusParseFail {
			dest, skipped, perr := archive.Place(sessionDir, bucket, p.SrcPath, p.Fingerprint, mode)
			if perr != nil {
				sum.Failed++
				sum.Errors = append(sum.Errors, fmt.Sprintf("%s: %v", p.Filename, perr))
				rows = append(rows, toRow(p, bucket))
				continue
			}
			if skipped {
				sum.Skipped++
			} else {
				sum.Placed++
			}
			sum.Buckets[bucket]++
			e.store.SetPhotoArchived(p.ID, dest)
			// 同名 RAW/HEIC 伴生文件跟随主图一起归档
			moved, _, rerrs := archive.PlaceRaws(dest, p.SrcPath, mode)
			sum.Raws += moved
			sum.Errors = append(sum.Errors, rerrs...)
			rows = append(rows, toRow(p, bucket))
			continue
		}
		// 归档对象为源图（压缩图仅用于评分与预览）
		dest, skipped, err := archive.Place(sessionDir, bucket, p.SrcPath, p.Fingerprint, mode)
		if err != nil {
			sum.Failed++
			sum.Errors = append(sum.Errors, fmt.Sprintf("%s: %v", p.Filename, err))
			continue
		}
		if skipped {
			sum.Skipped++
		} else {
			sum.Placed++
		}
		sum.Buckets[bucket]++
		e.store.SetPhotoArchived(p.ID, dest)
		// 同名 RAW/HEIC 伴生文件跟随主图一起归档
		moved, _, rerrs := archive.PlaceRaws(dest, p.SrcPath, mode)
		sum.Raws += moved
		sum.Errors = append(sum.Errors, rerrs...)
		rows = append(rows, toRow(p, bucket))
	}
	// 失败类照片仅进报告（文件留在原目录）
	if failedList, ferr := e.store.FailedPhotos(sessID); ferr == nil {
		for _, p := range failedList {
			rows = append(rows, toRow(p, ""))
		}
	}
	if csvPath, cerr := archive.WriteReportCSV(sessionDir, rows); cerr == nil {
		sum.ReportCSV = csvPath
	} else {
		sum.Errors = append(sum.Errors, "导出 CSV 失败: "+cerr.Error())
	}
	return sum, nil
}

func toRow(p *store.Photo, bucket string) *archive.ReportRow {
	return &archive.ReportRow{
		Filename: p.Filename, SrcPath: p.SrcPath, Score: p.Score, Dims: p.Dims, Tags: p.Tags,
		Strength: p.Strength, Weakness: p.Weakness, Model: p.Model, Bucket: bucket,
		DurationMs: p.DurationMs, Status: p.Status, UpdatedAt: p.UpdatedAt,
	}
}

// ---------- 汇总 / 重算 ----------

// Summary 批次统计
type Summary struct {
	Session    *store.Session `json:"session"`
	Status     map[string]int `json:"status"`
	Buckets    map[string]int `json:"buckets"`
	AvgScore   float64        `json:"avg_score"`
	MaxScore   float64        `json:"max_score"`
	EstCost    float64        `json:"est_cost"`
	Archived   bool           `json:"archived"`
	ArchiveDir string         `json:"archive_dir,omitempty"`
}

// GetSummary 汇总会话；sessionID 为空取最近会话
func (e *Engine) GetSummary(sessionID string) (*Summary, error) {
	cfg := e.cfgFn()
	sess, err := e.resolveSession(sessionID)
	if err != nil {
		return nil, nil // 尚无会话不是错误
	}
	status, err := e.store.SessionStatusCounts(sess.ID)
	if err != nil {
		return nil, err
	}
	photos, err := e.store.ScoredPhotos(sess.ID)
	if err != nil {
		return nil, err
	}
	sum := &Summary{Session: sess, Status: status, Buckets: map[string]int{}}
	var sumScore float64
	n := 0
	for _, p := range photos {
		if p.Status == store.StatusScored {
			sumScore += p.Score
			n++
			if p.Score > sum.MaxScore {
				sum.MaxScore = p.Score
			}
		}
		if p.ArchivedPath != "" {
			sum.Archived = true
			// 归档根目录 = 会话批次目录
			sum.ArchiveDir = filepath.Join(cfg.Paths.ArchiveRoot, sess.ID)
		}
		bucket := scorer.BucketOf(p.Score, p.Status == store.StatusParseFail, cfg.Score.Thresholds, p.OverrideBucket)
		sum.Buckets[bucket]++
	}
	if n > 0 {
		sum.AvgScore = round1(sumScore / float64(n))
	}
	sum.EstCost = e.estCost(sess.Model) * float64(status[store.StatusScored]+status[store.StatusParseFail])
	return sum, nil
}

// Recalculate 权重变更后基于已存维度分本地重算总分（0 API 成本）；sessionID 为空取最近会话
func (e *Engine) Recalculate(sessionID string) (int, error) {
	cfg := e.cfgFn()
	// 合议模式：逐照片按指纹重算平均（每模型最新一条 × 当前权重），跨批次同图一致
	if cfg.Score.Aggregate == "average" {
		var photos []*store.Photo
		var err error
		if sessionID == SessionAll {
			photos, err = e.store.ScoredPhotosAll()
		} else {
			var sess *store.Session
			sess, err = e.resolveSession(sessionID)
			if err != nil {
				return 0, nil
			}
			photos, err = e.store.ScoredPhotos(sess.ID)
		}
		if err != nil {
			return 0, err
		}
		seen := map[string]bool{}
		n := 0
		for _, p := range photos {
			if seen[p.Fingerprint] {
				continue // 同指纹只算一次（全部照片一并更新）
			}
			seen[p.Fingerprint] = true
			if e.RecomputeAggregate(p.Fingerprint) > 0 {
				n++
			}
		}
		return n, nil
	}
	// primary 模式：主模型维度分 × 当前权重
	if sessionID == SessionAll {
		photos, err := e.store.ScoredPhotosAll()
		if err != nil {
			return 0, err
		}
		scores := scorer.RecomputeAll(photos, cfg.WeightsNormalized())
		if err := e.store.UpdateScoresLocal(scores); err != nil {
			return 0, err
		}
		return len(scores), nil
	}
	sess, err := e.resolveSession(sessionID)
	if err != nil {
		return 0, nil
	}
	photos, err := e.store.ScoredPhotos(sess.ID)
	if err != nil {
		return 0, err
	}
	scores := scorer.RecomputeAll(photos, cfg.WeightsNormalized())
	if err := e.store.UpdateScoresLocal(scores); err != nil {
		return 0, err
	}
	return len(scores), nil
}

// resolveSession 解析目标会话：sessionID 为空时取最近会话
func (e *Engine) resolveSession(sessionID string) (*store.Session, error) {
	if sessionID != "" {
		sess, err := e.store.GetSession(sessionID)
		if err != nil {
			return nil, fmt.Errorf("会话不存在: %s", sessionID)
		}
		return sess, nil
	}
	sess, err := e.store.LastSession()
	if err != nil {
		return nil, errors.New("暂无会话")
	}
	return sess, nil
}

// ---------- utils ----------

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}
