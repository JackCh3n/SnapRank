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

// Event 事件总线消息（serve 转 SSE，desktop 转 Wails Events）
type Event struct {
	Type string      `json:"type"` // progress | stage | done | error
	Data interface{} `json:"data"`
}

// Progress 单张完成进度
type Progress struct {
	SessionID string  `json:"session_id"`
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
	Dir      string `json:"dir"`
	Model    string `json:"model"`     // 为空取配置默认
	SampleN  int    `json:"sample_n"`  // 抽样试跑数量，0=全量
	ForceNew bool   `json:"force_new"` // 强制新建会话（不续跑）
}

// Engine 流水线引擎
type Engine struct {
	cfgFn func() *config.Config // 每次读取最新配置（设置热更新）
	store *store.Store
	Bus   *Bus

	running   atomic.Bool
	cancel    context.CancelFunc
	sessionID string
	concScale atomic.Int64 // 429 降速倍率（1=正常，越大退避越久）
	mu        sync.Mutex
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

// Scan 扫描目录：过滤、按内容指纹去重；返回清单与预估费用
func (e *Engine) Scan(dir string) ([]*ScanItem, float64, error) {
	cfg := e.cfgFn()
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, 0, fmt.Errorf("目录不存在或不可读: %s", dir)
	}
	var items []*ScanItem
	seen := map[string]bool{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 不可读的子树跳过
		}
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if !compress.SupportedExts[ext] && !compress.UnsupportedExts[ext] {
			return nil
		}
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
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
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

// Start 校验并启动阶段一（压缩+评分）。校验同步返回错误，执行异步进行。
func (e *Engine) Start(opts StartOpts) (string, error) {
	if e.IsRunning() {
		return "", errors.New("已有任务在运行中")
	}
	cfg := e.cfgFn()
	if opts.Dir == "" {
		return "", errors.New("缺少源图目录")
	}
	if cfg.Provider.Type != "mock" && cfg.Provider.APIKey == "" {
		return "", errors.New("尚未配置 API Key（可在设置切换 mock 模式离线体验）")
	}
	model := opts.Model
	if model == "" {
		model = cfg.Model.Default
	}

	items, _, err := e.Scan(opts.Dir)
	if err != nil {
		return "", err
	}
	live := 0
	for _, it := range items {
		if !it.Dup {
			live++
		}
	}
	if live == 0 {
		return "", errors.New("目录中没有可处理的图片")
	}
	if opts.SampleN > 0 && opts.SampleN < live {
		live = opts.SampleN
	}
	estCost := e.estCost(model) * float64(live)

	// 成本护栏：批次上限 + 每日上限
	if e.estCost(model) > 0 {
		if cfg.Cost.BatchLimit > 0 && estCost > cfg.Cost.BatchLimit {
			return "", fmt.Errorf("预估费用 ¥%.2f 超过单批次上限 ¥%.2f（可先抽样试跑或在设置调整上限）", estCost, cfg.Cost.BatchLimit)
		}
		day := time.Now().Format("2006-01-02")
		spent, _ := e.store.SpendToday(day)
		if cfg.Cost.DailyLimit > 0 && spent+estCost > cfg.Cost.DailyLimit {
			return "", fmt.Errorf("今日预估累计 ¥%.2f 将超过每日上限 ¥%.2f（可在设置调整）", spent+estCost, cfg.Cost.DailyLimit)
		}
	}

	// 会话：续跑或新建
	var sess *store.Session
	if !opts.ForceNew {
		sess, _ = e.store.FindResumableSession(opts.Dir)
	}
	sessID := ""
	if sess != nil {
		sessID = sess.ID
		e.store.UpdateSessionStatus(sessID, store.SessionRunning, len(items), sess.Done)
		logutil.Info("续跑会话 %s（已完成 %d）", sessID, sess.Done)
	} else {
		sessID = newSessionID()
		if err := e.store.CreateSession(sessID, opts.Dir, model, scorer.PromptVersion); err != nil {
			return "", err
		}
	}

	// 登记清单；内容已变更的照片重置状态
	for _, it := range items {
		status := store.StatusPending
		if it.Dup {
			status = store.StatusDuplicate
		} else if compress.UnsupportedExts[strings.ToLower(filepath.Ext(it.Path))] {
			status = store.StatusUnsupported // HEIC/RAW：登记但不支持
		}
		id, err := e.store.UpsertPhoto(&store.Photo{
			SessionID: sessID, Fingerprint: it.FP, SrcPath: it.Path,
			Filename: it.Filename, RelPath: relPath(opts.Dir, it.Path), Size: it.Size, Status: status,
		})
		if err == nil && status == store.StatusPending {
			e.resetIfChanged(id, it.FP)
		}
	}

	if err := e.startAsync(sessID, opts, model, live); err != nil {
		return "", err
	}
	return sessID, nil
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
func (e *Engine) startAsync(sessID string, opts StartOpts, model string, totalLive int) error {
	ctx, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	e.cancel = cancel
	e.sessionID = sessID
	e.mu.Unlock()
	e.running.Store(true)
	e.concScale.Store(1)

	go func() {
		defer e.running.Store(false)
		stopped := e.run(ctx, sessID, opts, model, totalLive)
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
		e.Bus.Publish(Event{Type: "done", Data: DonePayload{SessionID: sessID, Stopped: stopped, Failed: counts[store.StatusFailed]}})
	}()
	return nil
}

// run 执行压缩与评分（单工作池：内联压缩缓存命中即跳过），返回是否被停止
func (e *Engine) run(ctx context.Context, sessID string, opts StartOpts, model string, totalLive int) bool {
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

	photos, _, err := e.store.ListPhotos(sessID, "", 0, 1<<20)
	if err != nil {
		logutil.Error("读取清单失败: %v", err)
		return false
	}
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
	total := len(queue)
	doneCount := atomic.Int64{}
	e.Bus.Publish(Event{Type: "stage", Data: Stage{SessionID: sessID, Stage: "score", Total: total}})

	cacheDir := filepath.Join(cfg.Paths.DataDir, "work", sessID, "compressed")
	jobs := make(chan *store.Photo)
	workers := cfg.Pipeline.ScoreConcurrency
	var wg sync.WaitGroup
	var stopFlag atomic.Bool
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				if ctx.Err() != nil {
					stopFlag.Store(true)
					continue
				}
				// 确保压缩图就绪（ToCache 幂等，命中即跳过）
				cp := filepath.Join(cacheDir, compress.CacheName(p.Fingerprint))
				if _, serr := os.Stat(cp); os.IsNotExist(serr) {
					if _, cerr := eng.ToCache(p.SrcPath, p.Fingerprint, cacheDir); cerr != nil {
						logutil.Info("压缩失败 %s: %v", p.Filename, cerr)
						e.store.SetPhotoStatus(p.ID, store.StatusBadImage, truncate(cerr.Error(), 300))
						idx := doneCount.Add(1)
						e.Bus.Publish(Event{Type: "progress", Data: Progress{SessionID: sessID, Index: int(idx), Total: total, File: p.Filename, Status: store.StatusBadImage, Error: truncate(cerr.Error(), 120)}})
						continue
					}
				}
				e.store.SetPhotoCompressed(p.ID, cp)
				prog := e.scoreOne(ctx, prov, model, p, cp, total, &doneCount)
				e.Bus.Publish(Event{Type: "progress", Data: prog})
			}
		}()
	}
	// 生产者：受 ctx 控制提前收尾
producer:
	for _, p := range queue {
		select {
		case <-ctx.Done():
			break producer
		case jobs <- p:
		}
	}
	close(jobs)
	wg.Wait()
	return stopFlag.Load() && ctx.Err() != nil
}

func isFinalStatus(s string) bool {
	switch s {
	case store.StatusScored, store.StatusParseFail, store.StatusFailed,
		store.StatusBadImage, store.StatusUnsupported:
		return true
	}
	return false
}

// scoreOne 评分单张（含跨会话缓存命中与重试退避）
func (e *Engine) scoreOne(ctx context.Context, prov provider.Provider, model string, p *store.Photo, compressedPath string, total int, doneCount *atomic.Int64) Progress {
	cfg := e.cfgFn()
	start := time.Now()

	// 跨会话缓存命中（0 计费）
	if cfg.Score.ReuseScores {
		if ent, err := e.store.CacheGet(p.Fingerprint, model, scorer.PromptVersion); err == nil {
			score := scorer.WeightedScore(ent.Dims, cfg.WeightsNormalized())
			e.store.SetPhotoResult(p.ID, store.StatusScored, score, &ent.Dims, ent.Tags, ent.Strength, ent.Weakness, model, scorer.PromptVersion, "cache", false, 0)
			idx := doneCount.Add(1)
			return Progress{SessionID: p.SessionID, Index: int(idx), Total: total, File: p.Filename, Score: score, Status: store.StatusScored, Cached: true}
		}
	}

	b64, err := readB64(compressedPath)
	if err != nil {
		e.store.SetPhotoStatus(p.ID, store.StatusBadImage, truncate(err.Error(), 300))
		idx := doneCount.Add(1)
		return Progress{SessionID: p.SessionID, Index: int(idx), Total: total, File: p.Filename, Status: store.StatusBadImage, Error: truncate(err.Error(), 120)}
	}

	req := provider.ScoreRequest{
		ImageB64:  b64,
		Filename:  p.Filename,
		Prompt:    scorer.BuildPrompt(),
		Temp:      cfg.Score.Temperature,
		MaxTokens: cfg.Score.MaxTokens,
		Timeout:   time.Duration(cfg.Score.TimeoutSec) * time.Second,
	}

	var content string
	attempts := 0
	for {
		attempts++
		content, err = prov.Score(ctx, model, req)
		if err == nil || errors.Is(err, provider.ErrModelNoVision) || ctx.Err() != nil || attempts >= 3 {
			break
		}
		backoff := time.Duration(1<<attempts) * time.Second // 2s / 4s
		if errors.Is(err, provider.ErrRateLimit) {
			backoff *= time.Duration(e.concScale.Load())
			// 收到限流：全局降速，成功后无需恢复（评分结束后进程内自然复位）
			e.concScale.Add(1)
		}
		logutil.Info("重试 %s（第 %d 次）: %v", p.Filename, attempts, err)
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
	}
	idx := doneCount.Add(1)
	if err != nil {
		if errors.Is(err, provider.ErrModelNoVision) {
			err = fmt.Errorf("%w（请在模型下拉更换视觉模型）", err)
		}
		e.store.SetPhotoStatus(p.ID, store.StatusFailed, truncate(err.Error(), 300))
		return Progress{SessionID: p.SessionID, Index: int(idx), Total: total, File: p.Filename, Status: store.StatusFailed, Error: truncate(err.Error(), 120)}
	}

	dims, tags, strength, weakness, clamped, perr := scorer.Parse(content)
	if perr != nil {
		// 解析失败不伪造分数：标记待复检，不参与正常分档
		e.store.SetPhotoResult(p.ID, store.StatusParseFail, 0, nil, nil, "", "", model, scorer.PromptVersion, "api", false, time.Since(start).Milliseconds())
		e.store.SetPhotoStatus(p.ID, store.StatusParseFail, truncate(perr.Error(), 200))
		return Progress{SessionID: p.SessionID, Index: int(idx), Total: total, File: p.Filename, Status: store.StatusParseFail, Error: truncate(perr.Error(), 120)}
	}
	score := scorer.WeightedScore(dims, cfg.WeightsNormalized())
	e.store.SetPhotoResult(p.ID, store.StatusScored, score, &dims, tags, strength, weakness, model, scorer.PromptVersion, "api", clamped, time.Since(start).Milliseconds())
	e.store.CachePut(p.Fingerprint, model, scorer.PromptVersion, dims, tags, strength, weakness)
	return Progress{SessionID: p.SessionID, Index: int(idx), Total: total, File: p.Filename, Score: score, Status: store.StatusScored}
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
	if !e.IsRunning() {
		return false
	}
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
}

// ---------- 归档（阶段二） ----------

// ArchiveSummary 归档结果
type ArchiveSummary struct {
	SessionID string         `json:"session_id"`
	Mode      string         `json:"mode"`
	Placed    int            `json:"placed"`
	Skipped   int            `json:"skipped"`
	Failed    int            `json:"failed"`
	Dir       string         `json:"dir"`
	Buckets   map[string]int `json:"buckets"`
	ReportCSV string         `json:"report_csv"`
	Errors    []string       `json:"errors,omitempty"`
}

// Archive 执行阶段二：按确认的方式复制/移动到档位目录并导出报告
func (e *Engine) Archive(mode archive.Mode) (*ArchiveSummary, error) {
	if e.IsRunning() {
		return nil, errors.New("任务运行中，请先停止或等待完成")
	}
	if !archive.ValidMode(mode) {
		return nil, fmt.Errorf("非法归档方式: %s", mode)
	}
	cfg := e.cfgFn()
	sess, err := e.store.LastSession()
	if err != nil {
		return nil, errors.New("暂无可归档的会话")
	}
	photos, err := e.store.ScoredPhotos(sess.ID)
	if err != nil {
		return nil, err
	}
	if len(photos) == 0 {
		return nil, errors.New("该会话没有可归档的评分结果")
	}
	sessionDir := filepath.Join(cfg.Paths.ArchiveRoot, sess.ID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return nil, err
	}

	sum := &ArchiveSummary{SessionID: sess.ID, Mode: string(mode), Buckets: map[string]int{}, Dir: sessionDir}
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
		rows = append(rows, toRow(p, bucket))
	}
	// 失败类照片仅进报告（文件留在原目录）
	if failedList, ferr := e.store.FailedPhotos(sess.ID); ferr == nil {
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

// GetSummary 汇总最近会话
func (e *Engine) GetSummary() (*Summary, error) {
	cfg := e.cfgFn()
	sess, err := e.store.LastSession()
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

// Recalculate 权重变更后基于已存维度分本地重算总分（0 API 成本）
func (e *Engine) Recalculate() (int, error) {
	cfg := e.cfgFn()
	sess, err := e.store.LastSession()
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
