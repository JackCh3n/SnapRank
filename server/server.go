// Package server 本地 Web 服务：REST API + SSE 进度 + 内嵌前端托管。
// 与 Wails 绑定层共用 internal/core 的同一套用例。
package server

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"snaprank/internal/core"
	"snaprank/internal/pipeline"
	"snaprank/web"
)

// Server HTTP 服务
type Server struct {
	core *core.Core
	mux  *http.ServeMux
}

// New 构造并注册路由
func New(c *core.Core) *Server {
	s := &Server{core: c, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler 返回根处理器
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/state", s.handleState)
	s.mux.HandleFunc("GET /api/config", s.handleGetConfig)
	s.mux.HandleFunc("POST /api/config", s.handleSaveConfig)
	s.mux.HandleFunc("POST /api/test-connection", s.handleTestConn)
	s.mux.HandleFunc("GET /api/models", s.handleModels)
	s.mux.HandleFunc("POST /api/model", s.handleSetModel)
	s.mux.HandleFunc("POST /api/scan", s.handleScan)
	s.mux.HandleFunc("POST /api/start", s.handleStart)
	s.mux.HandleFunc("POST /api/stop", s.handleStop)
	s.mux.HandleFunc("GET /api/summary", s.handleSummary)
	s.mux.HandleFunc("GET /api/sessions", s.handleSessions)
	s.mux.HandleFunc("GET /api/gallery", s.handleGallery)
	s.mux.HandleFunc("POST /api/db/purge", s.handleDBPurge)
	s.mux.HandleFunc("POST /api/gallery/delete", s.handleGalleryDelete)
	s.mux.HandleFunc("POST /api/session/rename", s.handleSessionRename)
	s.mux.HandleFunc("POST /api/session/delete", s.handleSessionDelete)
	s.mux.HandleFunc("POST /api/clear-all", s.handleClearAll)
	s.mux.HandleFunc("GET /api/photos", s.handlePhotos)
	s.mux.HandleFunc("GET /api/photo", s.handlePhoto)
	s.mux.HandleFunc("POST /api/photo/bucket", s.handleBucket)
	s.mux.HandleFunc("POST /api/recalculate", s.handleRecalc)
	s.mux.HandleFunc("POST /api/rescore", s.handleRescore)
	s.mux.HandleFunc("POST /api/clean-cache", s.handleCleanCache)
	s.mux.HandleFunc("POST /api/dir-history/remove", s.handleRemoveDirHistory)
	s.mux.HandleFunc("POST /api/pick-dir", s.handlePickDir)
	s.mux.HandleFunc("POST /api/import", s.handleImport)
	s.mux.HandleFunc("POST /api/archive", s.handleArchive)
	s.mux.HandleFunc("GET /api/thumb", s.handleThumb)
	s.mux.HandleFunc("GET /api/report", s.handleReport)
	s.mux.HandleFunc("POST /api/open", s.handleOpen)
	s.mux.HandleFunc("GET /api/events", s.handleEvents)
	s.mux.HandleFunc("/", s.handleStatic)
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func decodeBody(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

// ---------- handlers ----------

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	eng := s.core.Engine()
	sum, _ := s.core.Summary(r.URL.Query().Get("session"))
	writeJSON(w, 200, map[string]interface{}{
		"config":       s.core.GetConfig(),
		"currentModel": s.core.GetCurrentModel(),
		"running":      eng.IsRunning(),
		"session":      eng.CurrentSession(),
		"queue":        eng.QueueStatus(),
		"summary":      sum,
		"version":      Version,
	})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.core.GetConfig())
}

func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	var req core.SaveConfigRequest
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	cfg, err := s.core.SaveConfig(req)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, cfg)
}

func (s *Server) handleTestConn(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.core.TestConnection())
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	ml, err := s.core.ListModels()
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, ml)
}

func (s *Server) handleSetModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.core.SetCurrentModel(req.ID); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]string{"current": s.core.GetCurrentModel()})
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir     string   `json:"dir"`
		Formats []string `json:"formats"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	items, estCost, err := s.core.Scan(req.Dir, req.Formats)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]interface{}{"items": items, "est_cost": estCost, "count": len(items)})
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	var req pipeline.StartOpts
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	res, err := s.core.Start(req)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"stopped": s.core.Stop()})
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	sum, err := s.core.Summary(r.URL.Query().Get("session"))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, sum)
}

// handleDBPurge 手动清理 N 天前的数据库记录
func (s *Server) handleDBPurge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Days int `json:"days"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	res, err := s.core.PurgeOldRecords(req.Days)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, res)
}

// handleGallery 图库列表
func (s *Server) handleGallery(w http.ResponseWriter, r *http.Request) {
	items, err := s.core.Gallery()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, items)
}

// handleGalleryDelete 批量删除照片源文件（危险操作）
func (s *Server) handleGalleryDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs       []int64 `json:"ids"`
		DeleteRaw bool    `json:"delete_raw"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	res, err := s.core.GalleryDelete(req.IDs, req.DeleteRaw)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, res)
}

// handleSessionRename 批次重命名
func (s *Server) handleSessionRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.core.RenameSession(req.ID, req.Name); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

// handleSessionDelete 删除批次
func (s *Server) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	freed, err := s.core.DeleteSession(req.ID)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]interface{}{"ok": "1", "freed_mb": float64(freed) / 1048576})
}

// handleImport 接收前端上传的照片（粘贴/拖入），归类到独立导入目录。
// 表单：files[] 文件，paths 为对应相对路径（JSON 数组，保留子目录结构）。
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(1 << 29); err != nil {
		writeErr(w, 400, fmt.Errorf("解析上传失败: %w", err))
		return
	}
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		writeErr(w, 400, errors.New("未收到文件"))
		return
	}
	var paths []string
	if v := r.MultipartForm.Value["paths"]; len(v) > 0 {
		json.Unmarshal([]byte(v[0]), &paths)
	}
	cfg := s.core.GetConfig()
	importsRoot, _ := filepath.Abs(filepath.Join(cfg.Paths.DataDir, "imports"))
	dir := ""
	// 追加模式：前端携带已有导入目录（必须在 imports 根内，防路径逃逸）
	if reqDir := strings.TrimSpace(r.FormValue("dir")); reqDir != "" {
		abs := filepath.Clean(reqDir)
		rel, relErr := filepath.Rel(importsRoot, abs)
		if relErr == nil && !strings.HasPrefix(rel, "..") && rel != "." {
			if st, stErr := os.Stat(abs); stErr == nil && st.IsDir() {
				dir = abs
			}
		}
	}
	if dir == "" {
		dir = filepath.Join(importsRoot, time.Now().Format("20060102_150405")+"_"+randomSuffix())
	}
	saved := 0
	for i, fh := range files {
		rel := fh.Filename
		if i < len(paths) && paths[i] != "" {
			rel = paths[i]
		}
		rel = sanitizeRelPath(rel)
		if rel == "" {
			continue
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			writeErr(w, 500, err)
			return
		}
		src, err := fh.Open()
		if err != nil {
			continue
		}
		out, err := os.Create(dst)
		if err != nil {
			src.Close()
			continue
		}
		io.Copy(out, src)
		out.Close()
		src.Close()
		saved++
	}
	if saved == 0 {
		writeErr(w, 400, errors.New("没有可保存的文件"))
		return
	}
	cfg.RememberDir(dir)
	cfg.Save()
	writeJSON(w, 200, map[string]interface{}{"dir": dir, "count": saved})
}

// sanitizeRelPath 清洗相对路径：去盘符/..，保留子目录结构
func sanitizeRelPath(rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	parts := strings.Split(rel, "/")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "." || p == ".." || strings.Contains(p, ":") {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, "/")
}

func randomSuffix() string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 4)
	rand.Read(b)
	for i := range b {
		b[i] = letters[int(b[i])%len(letters)]
	}
	return string(b)
}

// handlePickDir 弹出本机目录选择框；串行化防止多窗。
// 对话框运行在独立子进程（SnapRank.exe pickdir）中：COM 对话框的任何异常
// 都不会影响服务进程；子进程作为新 GUI 进程也能自然抢占前台。
var pickMu sync.Mutex

func (s *Server) handlePickDir(w http.ResponseWriter, r *http.Request) {
	if !pickMu.TryLock() {
		writeErr(w, 429, errors.New("目录选择窗口已打开，请先完成或取消"))
		return
	}
	defer pickMu.Unlock()

	exe, err := os.Executable()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	exeDir := filepath.Dir(exe)
	// 程序名恒为 SnapRank.exe（构建产物固定名），路径仅用于子进程定位
	cmd := &exec.Cmd{
		Path:        filepath.Join(exeDir, "SnapRank.exe"),
		Args:        []string{"SnapRank.exe", "pickdir"},
		Dir:         exeDir,
		Stderr:      os.Stderr, // 子进程崩溃详情进服务日志
		SysProcAttr: &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000},
	}
	out, outErr := cmd.Output()
	line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "ERROR: "))
	// 子进程崩溃/被杀也不影响服务：有结果就解析，否则返回错误
	if line == "" || (outErr != nil && !strings.HasPrefix(string(out), "ERROR: ") && !strings.HasPrefix(string(out), "CANCELLED")) {
		msg := "目录选择进程异常退出"
		if outErr != nil {
			msg += ": " + outErr.Error()
		}
		writeErr(w, 500, errors.New(msg))
		return
	}
	switch {
	case line == "CANCELLED" || line == "":
		writeJSON(w, 200, map[string]string{"dir": ""})
	case strings.HasPrefix(strings.TrimSpace(string(out)), "ERROR: "):
		writeErr(w, 500, errors.New(line))
	default:
		writeJSON(w, 200, map[string]string{"dir": line})
	}
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.core.ListSessions()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, list)
}

// handleClearAll 一键清空全部业务数据（危险操作，前端需二次确认）
func (s *Server) handleClearAll(w http.ResponseWriter, r *http.Request) {
	freed, err := s.core.ClearAllData()
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]interface{}{"ok": "1", "freed_mb": float64(freed) / 1048576})
}

func (s *Server) handlePhotos(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("page_size"))
	pp, err := s.core.ListPhotos(q.Get("session"), q.Get("status"), page, size,
		q.Get("sort"), q.Get("order"), q.Get("model"), q.Get("source"), q.Get("band"))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, pp)
}

func (s *Server) handlePhoto(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	p, err := s.core.GetPhoto(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) handleBucket(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     int64  `json:"id"`
		Bucket string `json:"bucket"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.core.SetPhotoBucket(req.ID, req.Bucket); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

func (s *Server) handleRecalc(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Session string `json:"session"`
	}
	decodeBody(r, &req)
	n, err := s.core.Recalculate(req.Session)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]int{"recalculated": n})
}

// handleRemoveDirHistory 删除一条目录历史
func (s *Server) handleRemoveDirHistory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir string `json:"dir"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.core.RemoveDirHistory(req.Dir); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

// handleCleanCache 清理压缩图缓存
func (s *Server) handleCleanCache(w http.ResponseWriter, r *http.Request) {
	freed, err := s.core.CleanWorkCache()
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]interface{}{"freed_bytes": freed, "freed_mb": float64(freed) / 1048576})
}

// handleRescore 复检重评：单张/多张（force=true 忽略缓存）或全部待复检（all=true）
func (s *Server) handleRescore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs   []int64 `json:"ids"`
		Force bool    `json:"force"`
		All   bool    `json:"all"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if req.All {
		sessID, n, err := s.core.RescoreAllFailed()
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]interface{}{"session_id": sessID, "count": n})
		return
	}
	sessID, err := s.core.Rescore(req.IDs, req.Force)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]interface{}{"session_id": sessID, "count": len(req.IDs)})
}

func (s *Server) handleArchive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode    string `json:"mode"`
		Session string `json:"session"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	sum, err := s.core.Archive(req.Mode, req.Session)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, sum)
}

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	p, err := s.core.ThumbPath(id)
	if err != nil {
		http.Error(w, "no thumb", 404)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "max-age=3600")
	http.ServeFile(w, r, p)
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	p, err := s.core.ReportPath()
	if err != nil {
		writeErr(w, 404, errors.New("尚未导出报告（归档后生成 report.csv）"))
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=report.csv")
	http.ServeFile(w, r, p)
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.core.OpenFolder(req.Path); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

// handleEvents SSE 进度流
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := s.core.Engine().Bus.Subscribe()
	defer s.core.Engine().Bus.Unsubscribe(ch)

	// 连接即推一次当前状态，避免前端漏听 stage
	eng := s.core.Engine()
	writeSSE(w, fl, pipeline.Event{Type: "state", Data: map[string]interface{}{
		"running": eng.IsRunning(), "session": eng.CurrentSession(), "queue": eng.QueueStatus(),
	}})

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case ev := <-ch:
			writeSSE(w, fl, ev)
			if ev.Type == "done" {
				return
			}
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func writeSSE(w http.ResponseWriter, fl http.Flusher, ev pipeline.Event) {
	// data 字段只放载荷本体（类型已在 event: 行中），
	// 前端 JSON.parse 后即为 payload；若序列化整个信封会导致前端读到双层包装。
	data, err := json.Marshal(ev.Data)
	if err != nil {
		data = []byte("{}")
	}
	io.WriteString(w, "event: "+ev.Type+"\ndata: "+string(data)+"\n\n")
	fl.Flush()
}

// handleStatic 内嵌前端托管（SPA 回退 index.html）
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeErr(w, 404, errors.New("not found"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	f := web.Dist()
	if st, err := fs.Stat(f, path); err != nil || st.IsDir() {
		path = "index.html"
	}
	data, err := fs.ReadFile(f, path)
	if err != nil {
		http.Error(w, "frontend not built", 500)
		return
	}
	if path == "index.html" {
		w.Header().Set("Cache-Control", "no-cache") // 前端发版后浏览器立取新版
	}
	w.Header().Set("Content-Type", mimeOf(path))
	w.Write(data)
}

func mimeOf(path string) string {
	switch {
	case strings.HasSuffix(path, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(path, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(path, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(path, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(path, ".png"):
		return "image/png"
	case strings.HasSuffix(path, ".ico"):
		return "image/x-icon"
	case strings.HasSuffix(path, ".json"):
		return "application/json; charset=utf-8"
	case strings.HasSuffix(path, ".woff2"):
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}

// Version 版本号（构建时可注入）
var Version = "v1.2.0"
