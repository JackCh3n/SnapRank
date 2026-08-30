//go:build desktop

// Package bind Wails 桌面壳绑定层：与 serve 模式共用 internal/core，
// 事件总线经 Wails Events 转发给前端（事件名与 SSE 的 type 一致）。
package bind

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"snaprank/internal/config"
	"snaprank/internal/core"
	"snaprank/internal/pipeline"
)

// App 暴露给前端的对象（wails.json / Bind 注册）
type App struct {
	ctx context.Context
	c   *core.Core
}

// New 构造绑定层
func New() (*App, error) {
	c, err := core.New("")
	if err != nil {
		return nil, err
	}
	return &App{c: c}, nil
}

// Close 释放资源
func (a *App) Close() { a.c.Close() }

// Startup Wails 生命周期
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	go a.pumpEvents()
}

// Shutdown Wails 生命周期
func (a *App) Shutdown() { a.c.Close() }

// pumpEvents 事件总线 → Wails Events
func (a *App) pumpEvents() {
	ch := a.c.Engine().Bus.Subscribe()
	defer a.c.Engine().Bus.Unsubscribe(ch)
	for ev := range ch {
		runtime.EventsEmit(a.ctx, ev.Type, ev.Data)
	}
}

// ---------- 配置 / 连接 ----------

// GetConfig 读取配置（Key 脱敏）
func (a *App) GetConfig() *config.Config { return a.c.GetConfig() }

// SaveConfig 部分更新配置
func (a *App) SaveConfig(req core_SaveConfigRequest) (*config.Config, error) {
	return a.c.SaveConfig(core.SaveConfigRequest(req))
}

// TestConnection 测试平台连通性
func (a *App) TestConnection() *core.ConnState { return a.c.TestConnection() }

// ListModels 在线拉取模型清单
func (a *App) ListModels() (*core.ModelList, error) { return a.c.ListModels() }

// GetCurrentModel 当前模型
func (a *App) GetCurrentModel() string { return a.c.GetCurrentModel() }

// SetCurrentModel 切换模型（批次粒度）
func (a *App) SetCurrentModel(id string) error { return a.c.SetCurrentModel(id) }

// ---------- 流水线 ----------

// ScanDirectory 扫描目录
func (a *App) ScanDirectory(dir string) ([]*pipeline.ScanItem, float64, error) {
	return a.c.Scan(dir)
}

// SelectDirectory 系统目录选择对话框
func (a *App) SelectDirectory() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择照片目录"})
}

// StartPipeline 启动阶段一
func (a *App) StartPipeline(opts pipeline.StartOpts) (string, error) { return a.c.Start(opts) }

// StopPipeline 停止
func (a *App) StopPipeline() bool { return a.c.Stop() }

// GetSummary 批次统计
func (a *App) GetSummary() (*pipeline.Summary, error) { return a.c.Summary() }

// Recalculate 本地重算
func (a *App) Recalculate() (int, error) { return a.c.Recalculate() }

// Archive 执行阶段二归档
func (a *App) Archive(mode string) (*pipeline.ArchiveSummary, error) { return a.c.Archive(mode) }

// ---------- 明细 ----------

// ListPhotos 明细分页
func (a *App) ListPhotos(session, status string, page, pageSize int) (*core.PhotoPage, error) {
	return a.c.ListPhotos(session, status, page, pageSize)
}

// SetPhotoBucket 手动调档
func (a *App) SetPhotoBucket(id int64, bucket string) error { return a.c.SetPhotoBucket(id, bucket) }

// GetThumbDataURI 缩略图 dataURI（WebView2 无法直接访问本地路径）
func (a *App) GetThumbDataURI(id int64) (string, error) { return a.c.ThumbDataURI(id) }

// OpenFolder 打开目录
func (a *App) OpenFolder(path string) error { return a.c.OpenFolder(path) }

// GetState 汇总状态（前端 onMounted 拉一次）
func (a *App) GetState() map[string]interface{} {
	sum, _ := a.c.Summary()
	return map[string]interface{}{
		"config":       a.c.GetConfig(),
		"currentModel": a.c.GetCurrentModel(),
		"running":      a.c.Engine().IsRunning(),
		"session":      a.c.Engine().CurrentSession(),
		"summary":      sum,
	}
}

// 类型别名（避免 bind 包直接暴露 core.SaveConfigRequest 的 import 差异）
type core_SaveConfigRequest = core.SaveConfigRequest

