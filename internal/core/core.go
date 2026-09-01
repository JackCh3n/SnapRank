// Package core 面向桥接层（serve HTTP / bind Wails）的用例聚合，
// 两种运行形态共用同一套方法，保证行为一致。
package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"time"

	"snaprank/internal/archive"
	"snaprank/internal/config"
	"snaprank/internal/logutil"
	"snaprank/internal/pipeline"
	"snaprank/internal/provider"
	"snaprank/internal/scorer"
	"snaprank/internal/store"
)

// Core 应用核心
type Core struct {
	cfg      *config.Config
	cfgMu    sync.Mutex
	eng      *pipeline.Engine
	engMu    sync.Mutex
	st       *store.Store
	curModel string // 当前所选模型（批次粒度生效）
	mu       sync.Mutex
}

// New 构造核心；dataDir 为空时取默认用户目录
func New(dataDir string) (*Core, error) {
	if dataDir == "" {
		dataDir = config.DefaultDataDir()
	}
	cfg, err := config.Load(dataDir)
	if err != nil {
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	if err := os.MkdirAll(cfg.Paths.DataDir, 0o755); err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(cfg.Paths.DataDir, "snaprank.db"))
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	c := &Core{cfg: cfg, st: st, curModel: cfg.Model.Default}
	c.eng = pipeline.New(c.snapshotConfig, st)
	return c, nil
}

// Close 释放资源
func (c *Core) Close() { c.st.Close() }

func (c *Core) snapshotConfig() *config.Config {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	return c.cfg
}

// Engine 流水线引擎
func (c *Core) Engine() *pipeline.Engine {
	c.engMu.Lock()
	defer c.engMu.Unlock()
	return c.eng
}

// ---------- 配置 ----------

// GetConfig 返回配置（API Key 脱敏：仅返回是否已设置）
func (c *Core) GetConfig() *config.Config {
	cfg := c.snapshotConfig()
	if cfg.Provider.APIKey != "" {
		masked := *cfg
		masked.Provider.APIKey = maskKey(cfg.Provider.APIKey)
		return &masked
	}
	return cfg
}

func maskKey(k string) string {
	if len(k) <= 8 {
		return "****"
	}
	return k[:4] + "****" + k[len(k)-4:]
}

// SaveConfigRequest 保存配置请求（api_key 为空表示保持不变；"-" 表示清除）
type SaveConfigRequest struct {
	Provider   *config.Provider       `json:"provider,omitempty"`
	DirHistory []string               `json:"dir_history,omitempty"` // 直接替换历史（删除标签用）
	Model      *config.ModelConfig    `json:"model,omitempty"`
	Weights    *config.Weights        `json:"weights,omitempty"`
	Score      *config.ScoreConfig    `json:"score,omitempty"`
	Pipeline   *config.PipelineConfig `json:"pipeline,omitempty"`
	Cost       *config.CostConfig     `json:"cost,omitempty"`
	Paths      *config.PathsConfig    `json:"paths,omitempty"`
}

// SaveConfig 部分更新并持久化配置
func (c *Core) SaveConfig(req SaveConfigRequest) (*config.Config, error) {
	c.cfgMu.Lock()
	cfg := c.cfg
	if req.DirHistory != nil {
		cfg.DirHistory = req.DirHistory
	}
	if req.Provider != nil {
		old := cfg.Provider.APIKey
		cfg.Provider = *req.Provider
		if cfg.Provider.APIKey == "" || cfg.Provider.APIKey == maskKey(old) {
			cfg.Provider.APIKey = old // 未改动
		}
	}
	if req.Model != nil {
		cfg.Model = *req.Model
	}
	if req.Weights != nil {
		cfg.Weights = *req.Weights
	}
	if req.Score != nil {
		cfg.Score = *req.Score
	}
	if req.Pipeline != nil {
		cfg.Pipeline = *req.Pipeline
	}
	if req.Cost != nil {
		cfg.Cost = *req.Cost
	}
	if req.Paths != nil {
		cfg.Paths = *req.Paths
	}
	err := cfg.Save()
	c.cfgMu.Unlock()
	if err != nil {
		return nil, err
	}
	c.SetCurrentModel(cfg.Model.Default)
	return c.GetConfig(), nil
}

// ---------- 连接与模型 ----------

// ConnState 连接测试结果
type ConnState struct {
	OK       bool     `json:"ok"`
	Provider string   `json:"provider"`
	Message  string   `json:"message"`
	Models   []string `json:"models,omitempty"`
}

// TestConnection 校验 Key/base_url（调 /v1/models）
func (c *Core) TestConnection() *ConnState {
	cfg := c.snapshotConfig()
	if cfg.Provider.Type == "mock" {
		return &ConnState{OK: true, Provider: "mock", Message: "离线 mock 模式（不调用平台）", Models: []string{"mock-scorer", "mock-strict"}}
	}
	p, err := provider.New(cfg)
	if err != nil {
		return &ConnState{OK: false, Provider: cfg.Provider.Type, Message: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ids, err := p.ListModels(ctx)
	if err != nil {
		return &ConnState{OK: false, Provider: cfg.Provider.Type, Message: sanitizeErr(err)}
	}
	return &ConnState{OK: true, Provider: cfg.Provider.Type, Message: fmt.Sprintf("连接成功，平台共 %d 个模型", len(ids)), Models: ids}
}

// ModelList 模型清单
type ModelList struct {
	All    []string `json:"all"`
	Vision []string `json:"vision"`
}

// ListModels 在线拉取模型清单并按视觉规则过滤
func (c *Core) ListModels() (*ModelList, error) {
	cfg := c.snapshotConfig()
	if cfg.Provider.Type == "mock" {
		return &ModelList{All: []string{"mock-scorer", "mock-strict"}, Vision: []string{"mock-scorer", "mock-strict"}}, nil
	}
	p, err := provider.New(cfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	all, err := p.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("拉取模型清单失败: %s（请检查网络与 API Key）", sanitizeErr(err))
	}
	return &ModelList{All: all, Vision: provider.FilterVision(all, cfg.Model.VisionPatterns)}, nil
}

// GetCurrentModel 当前打分模型（批次粒度生效）
func (c *Core) GetCurrentModel() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.curModel
}

// SetCurrentModel 切换当前模型：下一次“开始评分”时生效（批次内锁定）
func (c *Core) SetCurrentModel(id string) error {
	if id == "" {
		return fmt.Errorf("模型 ID 不能为空")
	}
	c.mu.Lock()
	c.curModel = id
	c.mu.Unlock()
	c.cfgMu.Lock()
	c.cfg.Model.Default = id
	err := c.cfg.Save()
	c.cfgMu.Unlock()
	return err
}

// ---------- 扫描 / 流水线 ----------

// Scan 扫描目录
func (c *Core) Scan(dir string) ([]*pipeline.ScanItem, float64, error) {
	return c.Engine().Scan(dir)
}

// Start 启动流水线（阶段一）
func (c *Core) Start(opts pipeline.StartOpts) (string, error) {
	if opts.Model == "" {
		opts.Model = c.GetCurrentModel()
	}
	sessID, err := c.Engine().Start(opts)
	if err != nil {
		return "", err
	}
	return sessID, nil
}

// Stop 停止
func (c *Core) Stop() bool { return c.Engine().Stop() }

// Summary 批次统计
func (c *Core) Summary() (*pipeline.Summary, error) { return c.Engine().GetSummary() }

// Archive 执行归档（阶段二）
func (c *Core) Archive(mode string) (*pipeline.ArchiveSummary, error) {
	return c.Engine().Archive(archive.Mode(mode))
}

// Recalculate 权重变更后本地重算
func (c *Core) Recalculate() (int, error) { return c.Engine().Recalculate() }

// Rescore 复检重评指定照片（force=true 忽略缓存强制重调 API）
func (c *Core) Rescore(ids []int64, force bool) (string, error) {
	return c.Engine().Rescore(ids, force)
}

// RescoreParseFail 一键重评全部解析失败照片
func (c *Core) RescoreParseFail() (string, int, error) { return c.Engine().RescoreParseFail() }

// ---------- 明细 / 缩略图 / 调档 ----------

// PhotoPage 明细分页
type PhotoPage struct {
	Total int            `json:"total"`
	Items []*store.Photo `json:"items"`
}

// ListPhotos 明细分页查询（sessionID 为空取最近会话）
func (c *Core) ListPhotos(sessionID, status string, page, pageSize int) (*PhotoPage, error) {
	if sessionID == "" {
		sess, err := c.st.LastSession()
		if err != nil {
			return &PhotoPage{}, nil
		}
		sessionID = sess.ID
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	items, total, err := c.st.ListPhotos(sessionID, status, (page-1)*pageSize, pageSize)
	if err != nil {
		return nil, err
	}
	return &PhotoPage{Total: total, Items: items}, nil
}

// GetPhoto 单张明细
func (c *Core) GetPhoto(id int64) (*store.Photo, error) { return c.st.GetPhoto(id) }

// SetPhotoBucket 手动调档（阶段二执行时生效）
func (c *Core) SetPhotoBucket(id int64, bucket string) error {
	if !scorer.BucketOverride(bucket) {
		return fmt.Errorf("非法档位: %s", bucket)
	}
	return c.st.SetPhotoBucket(id, bucket)
}

// Thumb 缩略图响应
type Thumb struct {
	DataURI string `json:"data_uri,omitempty"`
	Path    string `json:"path,omitempty"`
}

// ThumbForPhoto 供 serve 直接读文件返回，desktop 转 dataURI
func (c *Core) ThumbPath(id int64) (string, error) {
	p, err := c.st.GetPhoto(id)
	if err != nil {
		return "", err
	}
	if p.CompressedPath == "" {
		return "", os.ErrNotExist
	}
	return p.CompressedPath, nil
}

// ThumbDataURI 桌面壳用：压缩图转 data URI
func (c *Core) ThumbDataURI(id int64) (string, error) {
	p, err := c.st.GetPhoto(id)
	if err != nil {
		return "", err
	}
	if p.CompressedPath == "" {
		return "", os.ErrNotExist
	}
	data, err := os.ReadFile(p.CompressedPath)
	if err != nil {
		return "", err
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data), nil
}

// ---------- 目录 / 报告 ----------

// OpenFolder 用系统文件管理器打开目录
func (c *Core) OpenFolder(path string) error {
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		return fmt.Errorf("目录不存在: %s", path)
	}
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", path).Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

// RemoveDirHistory 删除一条目录历史标签
func (c *Core) RemoveDirHistory(dir string) error {
	c.cfgMu.Lock()
	var out []string
	for _, d := range c.cfg.DirHistory {
		if d != dir {
			out = append(out, d)
		}
	}
	c.cfg.DirHistory = out
	err := c.cfg.Save()
	c.cfgMu.Unlock()
	return err
}

// ListSessions 全部会话
func (c *Core) ListSessions() ([]*store.Session, error) { return c.st.ListSessions() }

// ClearAllData 一键清空全部业务数据（会话/明细/缓存/费用 + 压缩缓存目录），保留配置
func (c *Core) ClearAllData() (int64, error) {
	if c.Engine().IsRunning() {
		return 0, fmt.Errorf("任务运行中，请先停止或等待完成")
	}
	if err := c.st.ClearAllData(); err != nil {
		return 0, err
	}
	return c.CleanWorkCache()
}

// CleanWorkCache 清理压缩图缓存（data/work 整目录），返回释放的字节数（近似）。
// 缓存按指纹可重建，清理后重跑同批照片会重新压缩（评分缓存仍在，不会重复计费）。
func (c *Core) CleanWorkCache() (int64, error) {
	if c.Engine().IsRunning() {
		return 0, fmt.Errorf("任务运行中，请先停止或等待完成")
	}
	dir := filepath.Join(c.snapshotConfig().Paths.DataDir, "work")
	size := int64(0)
	filepath.Walk(dir, func(_ string, st os.FileInfo, err error) error {
		if err == nil && !st.IsDir() {
			size += st.Size()
		}
		return nil
	})
	if err := os.RemoveAll(dir); err != nil {
		return 0, err
	}
	logutil.Info("已清理压缩缓存 %s（%.1f MB）", dir, float64(size)/1048576)
	return size, nil
}

// ReportPath 最近会话的 report.csv（若已归档）
func (c *Core) ReportPath() (string, error) {
	cfg := c.snapshotConfig()
	sess, err := c.st.LastSession()
	if err != nil {
		return "", os.ErrNotExist
	}
	p := filepath.Join(cfg.Paths.ArchiveRoot, sess.ID, "report.csv")
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}

// DataDir 数据目录
func (c *Core) DataDir() string { return c.snapshotConfig().Paths.DataDir }

func sanitizeErr(err error) string {
	msg := err.Error()
	// 避免把完整 Key 带进错误信息
	re := regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]{6})[A-Za-z0-9_-]+`)
	return re.ReplaceAllString(msg, "${1}****")
}
