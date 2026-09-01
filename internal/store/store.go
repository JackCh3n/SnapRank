// Package store 基于 SQLite（modernc.org/sqlite，纯 Go 无 CGO）持久化
// 会话、照片明细、跨会话评分缓存与费用记录。表结构见设计方案 7.3。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// 照片状态
const (
	StatusPending     = "pending"     // 待处理
	StatusCompressed  = "compressed"  // 已压缩待评分
	StatusScored      = "scored"      // 评分成功
	StatusParseFail   = "parse_fail"  // 解析失败（待复检）
	StatusFailed      = "failed"      // 模型调用失败
	StatusBadImage    = "bad_image"   // 解码失败
	StatusUnsupported = "unsupported" // 格式不支持
	StatusDuplicate   = "duplicate"   // 批内重复（跳过）
)

// 会话状态
const (
	SessionRunning   = "running"
	SessionCompleted = "completed"
	SessionStopped   = "stopped"
)

// ErrNotFound 未找到记录
var ErrNotFound = errors.New("record not found")

// Dims 四维分数
type Dims struct {
	Technique   float64 `json:"technique"`
	Composition float64 `json:"composition"`
	Content     float64 `json:"content"`
	Color       float64 `json:"color"`
}

// Photo 一条照片明细
type Photo struct {
	ID             int64    `json:"id"`
	SessionID      string   `json:"session_id"`
	Fingerprint    string   `json:"fingerprint"`
	SrcPath        string   `json:"src_path"`
	Filename       string   `json:"filename"`
	RelPath        string   `json:"rel_path"`
	Size           int64    `json:"size"`
	Status         string   `json:"status"`
	Error          string   `json:"error,omitempty"`
	Score          float64  `json:"score"`
	Dims           *Dims    `json:"dims,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Strength       string   `json:"strength,omitempty"`
	Weakness       string   `json:"weakness,omitempty"`
	Model          string   `json:"model,omitempty"`
	PromptVersion  string   `json:"prompt_version,omitempty"`
	Clamped        bool     `json:"clamped,omitempty"`
	Source         string   `json:"source,omitempty"` // api | cache
	OverrideBucket string   `json:"override_bucket,omitempty"`
	ArchivedPath   string   `json:"archived_path,omitempty"`
	CompressedPath string   `json:"compressed_path,omitempty"`
	DurationMs     int64    `json:"duration_ms"`
	UpdatedAt      string   `json:"updated_at"`
}

// Session 一次评分会话
type Session struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	CreatedAt     string `json:"created_at"`
	SourceDir     string `json:"source_dir"`
	Model         string `json:"model"`
	PromptVersion string `json:"prompt_version"`
	Status        string `json:"status"`
	Total         int    `json:"total"`
	Done          int    `json:"done"`
}

// Store SQLite 存储句柄
type Store struct {
	db *sql.DB
}

// Open 打开（必要时创建）数据库并初始化表结构
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// modernc/sqlite 写并发需串行化
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  created_at TEXT NOT NULL,
  source_dir TEXT NOT NULL,
  model TEXT NOT NULL,
  prompt_version TEXT NOT NULL,
  status TEXT NOT NULL,
  total INTEGER NOT NULL DEFAULT 0,
  done INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS photos (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  fingerprint TEXT NOT NULL,
  src_path TEXT NOT NULL,
  filename TEXT NOT NULL,
  rel_path TEXT NOT NULL,
  size INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'pending',
  error TEXT NOT NULL DEFAULT '',
  score REAL NOT NULL DEFAULT 0,
  dims TEXT,
  tags TEXT,
  strength TEXT NOT NULL DEFAULT '',
  weakness TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  prompt_version TEXT NOT NULL DEFAULT '',
  clamped INTEGER NOT NULL DEFAULT 0,
  source TEXT NOT NULL DEFAULT '',
  override_bucket TEXT NOT NULL DEFAULT '',
  archived_path TEXT NOT NULL DEFAULT '',
  compressed_path TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_photos_sess_src ON photos(session_id, src_path);
CREATE INDEX IF NOT EXISTS idx_photos_sess ON photos(session_id);
CREATE TABLE IF NOT EXISTS score_cache (
  fingerprint TEXT NOT NULL,
  model TEXT NOT NULL,
  prompt_version TEXT NOT NULL,
  dims TEXT NOT NULL,
  tags TEXT NOT NULL,
  strength TEXT NOT NULL DEFAULT '',
  weakness TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  PRIMARY KEY (fingerprint, model, prompt_version)
);
CREATE TABLE IF NOT EXISTS spend_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  day TEXT NOT NULL,
  model TEXT NOT NULL,
  photos INTEGER NOT NULL,
  est_cost REAL NOT NULL
);`)
	// 旧库迁移：批次备注列
	if _, err := s.db.Exec(`ALTER TABLE sessions ADD COLUMN name TEXT NOT NULL DEFAULT ''`); err != nil {
		// 列已存在，忽略
	}
	return err
}

// Close 关闭数据库
func (s *Store) Close() error { return s.db.Close() }

func now() string { return time.Now().Format("2006-01-02 15:04:05") }

// ---------- session ----------

// CreateSession 新建会话
func (s *Store) CreateSession(id, sourceDir, model, promptVersion string) error {
	_, err := s.db.Exec(`INSERT INTO sessions(id, created_at, source_dir, model, prompt_version, status) VALUES(?,?,?,?,?,?)`,
		id, now(), sourceDir, model, promptVersion, SessionRunning)
	return err
}

// UpdateSessionStatus 更新会话状态与进度
func (s *Store) UpdateSessionStatus(id, status string, total, done int) error {
	_, err := s.db.Exec(`UPDATE sessions SET status=?, total=?, done=? WHERE id=?`, status, total, done, id)
	return err
}

// GetSession 查询会话
func (s *Store) GetSession(id string) (*Session, error) {
	row := s.db.QueryRow(`SELECT id, name, created_at, source_dir, model, prompt_version, status, total, done FROM sessions WHERE id=?`, id)
	var se Session
	if err := row.Scan(&se.ID, &se.Name, &se.CreatedAt, &se.SourceDir, &se.Model, &se.PromptVersion, &se.Status, &se.Total, &se.Done); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &se, nil
}

// FindResumableSession 查找同源目录未完成的会话（断点续跑）
func (s *Store) FindResumableSession(sourceDir string) (*Session, error) {
	row := s.db.QueryRow(`SELECT id, name, created_at, source_dir, model, prompt_version, status, total, done
		FROM sessions WHERE source_dir=? AND status=? ORDER BY created_at DESC LIMIT 1`, sourceDir, SessionRunning)
	var se Session
	if err := row.Scan(&se.ID, &se.Name, &se.CreatedAt, &se.SourceDir, &se.Model, &se.PromptVersion, &se.Status, &se.Total, &se.Done); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &se, nil
}

// ListSessions 全部会话（新→旧）
func (s *Store) ListSessions() ([]*Session, error) {
	rows, err := s.db.Query(`SELECT id, name, created_at, source_dir, model, prompt_version, status, total, done
		FROM sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*Session
	for rows.Next() {
		var se Session
		if err := rows.Scan(&se.ID, &se.Name, &se.CreatedAt, &se.SourceDir, &se.Model, &se.PromptVersion, &se.Status, &se.Total, &se.Done); err != nil {
			return nil, err
		}
		list = append(list, &se)
	}
	return list, rows.Err()
}

// RenameSession 重命名批次（备注）
func (s *Store) RenameSession(id, name string) error {
	_, err := s.db.Exec(`UPDATE sessions SET name=? WHERE id=?`, name, id)
	return err
}

// DeleteSession 删除批次及其全部照片明细（不删压缩缓存与归档文件）
func (s *Store) DeleteSession(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM photos WHERE session_id=?`, id); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE id=?`, id); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ClearAllData 清空业务数据（会话/明细/评分缓存/费用记录），保留配置
func (s *Store) ClearAllData() error {
	_, err := s.db.Exec(`DELETE FROM photos; DELETE FROM sessions; DELETE FROM score_cache; DELETE FROM spend_log;`)
	return err
}

// LastSession 最近一次会话
func (s *Store) LastSession() (*Session, error) {
	row := s.db.QueryRow(`SELECT id, name, created_at, source_dir, model, prompt_version, status, total, done
		FROM sessions ORDER BY created_at DESC LIMIT 1`)
	var se Session
	if err := row.Scan(&se.ID, &se.Name, &se.CreatedAt, &se.SourceDir, &se.Model, &se.PromptVersion, &se.Status, &se.Total, &se.Done); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &se, nil
}

// ---------- photo ----------

// UpsertPhoto 按 (session_id, src_path) 插入或更新照片基础信息，返回自增 ID
func (s *Store) UpsertPhoto(p *Photo) (int64, error) {
	p.UpdatedAt = now()
	res, err := s.db.Exec(`INSERT INTO photos(session_id, fingerprint, src_path, filename, rel_path, size, status, updated_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(session_id, src_path) DO UPDATE SET
		  fingerprint=excluded.fingerprint, size=excluded.size, updated_at=excluded.updated_at`,
		p.SessionID, p.Fingerprint, p.SrcPath, p.Filename, p.RelPath, p.Size, p.Status, p.UpdatedAt)
	if err != nil {
		return 0, err
	}
	if id, err := res.LastInsertId(); err == nil && id > 0 {
		return id, nil
	}
	// ON CONFLICT 更新路径下取回既有 ID
	var id int64
	err = s.db.QueryRow(`SELECT id FROM photos WHERE session_id=? AND src_path=?`, p.SessionID, p.SrcPath).Scan(&id)
	return id, err
}

// SetPhotoCompressed 标记压缩完成
func (s *Store) SetPhotoCompressed(id int64, compressedPath string) error {
	_, err := s.db.Exec(`UPDATE photos SET status=?, compressed_path=?, updated_at=? WHERE id=?`,
		StatusCompressed, compressedPath, now(), id)
	return err
}

// SetPhotoResult 写入评分结果
func (s *Store) SetPhotoResult(id int64, status string, score float64, dims *Dims, tags []string, strength, weakness, model, promptVersion, source string, clamped bool, durationMs int64) error {
	tagsJSON, _ := json.Marshal(tags)
	dimsJSON, _ := json.Marshal(dims)
	cl := 0
	if clamped {
		cl = 1
	}
	_, err := s.db.Exec(`UPDATE photos SET status=?, score=?, dims=?, tags=?, strength=?, weakness=?, model=?, prompt_version=?, source=?, clamped=?, duration_ms=?, updated_at=? WHERE id=?`,
		status, score, string(dimsJSON), string(tagsJSON), strength, weakness, model, promptVersion, source, cl, durationMs, now(), id)
	return err
}

// SetPhotoStatus 写入失败类状态与原因
func (s *Store) SetPhotoStatus(id int64, status, errMsg string) error {
	_, err := s.db.Exec(`UPDATE photos SET status=?, error=?, updated_at=? WHERE id=?`, status, errMsg, now(), id)
	return err
}

// ResetPhoto 内容变更后重置评分相关字段（保留手动调档）
func (s *Store) ResetPhoto(id int64) error {
	_, err := s.db.Exec(`UPDATE photos SET status='pending', error='', score=0, dims=NULL, tags=NULL,
		strength='', weakness='', model='', prompt_version='', clamped=0, source='',
		archived_path='', compressed_path='', duration_ms=0, updated_at=? WHERE id=?`, now(), id)
	return err
}

// SetPhotoArchived 记录归档目标路径
func (s *Store) SetPhotoArchived(id int64, dest string) error {
	_, err := s.db.Exec(`UPDATE photos SET archived_path=?, updated_at=? WHERE id=?`, dest, now(), id)
	return err
}

// SetPhotoBucket 手动调档（override）
func (s *Store) SetPhotoBucket(id int64, bucket string) error {
	_, err := s.db.Exec(`UPDATE photos SET override_bucket=?, updated_at=? WHERE id=?`, bucket, now(), id)
	return err
}

func scanPhoto(scan func(dest ...interface{}) error) (*Photo, error) {
	var p Photo
	var dims, tags sql.NullString
	var clamped int
	if err := scan(&p.ID, &p.SessionID, &p.Fingerprint, &p.SrcPath, &p.Filename, &p.RelPath, &p.Size,
		&p.Status, &p.Error, &p.Score, &dims, &tags, &p.Strength, &p.Weakness, &p.Model, &p.PromptVersion,
		&clamped, &p.Source, &p.OverrideBucket, &p.ArchivedPath, &p.CompressedPath, &p.DurationMs, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.Clamped = clamped == 1
	if dims.Valid && dims.String != "" {
		var d Dims
		if json.Unmarshal([]byte(dims.String), &d) == nil {
			p.Dims = &d
		}
	}
	if tags.Valid && tags.String != "" {
		json.Unmarshal([]byte(tags.String), &p.Tags)
	}
	return &p, nil
}

const photoCols = `id, session_id, fingerprint, src_path, filename, rel_path, size, status, error, score,
 dims, tags, strength, weakness, model, prompt_version, clamped, source, override_bucket, archived_path,
 compressed_path, duration_ms, updated_at`

// GetPhoto 按 ID 查询
func (s *Store) GetPhoto(id int64) (*Photo, error) {
	row := s.db.QueryRow(`SELECT `+photoCols+` FROM photos WHERE id=?`, id)
	p, err := scanPhoto(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// ListPhotos 会话明细分页查询；status 为空时返回全部
func (s *Store) ListPhotos(sessionID, status string, offset, limit int) ([]*Photo, int, error) {
	where := `WHERE session_id=?`
	args := []interface{}{sessionID}
	if status != "" {
		where += ` AND status=?`
		args = append(args, status)
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM photos `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`SELECT `+photoCols+` FROM photos `+where+` ORDER BY id LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var list []*Photo
	for rows.Next() {
		p, err := scanPhoto(rows.Scan)
		if err != nil {
			return nil, 0, err
		}
		list = append(list, p)
	}
	return list, total, rows.Err()
}

// SessionStatusCounts 会话内各状态数量
func (s *Store) SessionStatusCounts(sessionID string) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM photos WHERE session_id=? GROUP BY status`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		m[st] = n
	}
	return m, rows.Err()
}

// ScoredPhotos 会话内全部已评出分数的照片（含 parse_fail 之外的失败类不返回）
func (s *Store) ScoredPhotos(sessionID string) ([]*Photo, error) {
	rows, err := s.db.Query(`SELECT `+photoCols+` FROM photos WHERE session_id=? AND status IN (?,?) ORDER BY score DESC`,
		sessionID, StatusScored, StatusParseFail)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*Photo
	for rows.Next() {
		p, err := scanPhoto(rows.Scan)
		if err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

// FailedPhotos 会话内失败类明细（bad_image/unsupported/failed/duplicate）
func (s *Store) FailedPhotos(sessionID string) ([]*Photo, error) {
	rows, err := s.db.Query(`SELECT `+photoCols+` FROM photos WHERE session_id=? AND status IN (?,?,?,?) ORDER BY id`,
		sessionID, StatusFailed, StatusBadImage, StatusUnsupported, StatusDuplicate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*Photo
	for rows.Next() {
		p, err := scanPhoto(rows.Scan)
		if err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

// UpdateScoresLocal 本地重算后批量回写总分
func (s *Store) UpdateScoresLocal(scores map[int64]float64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for id, score := range scores {
		if _, err := tx.Exec(`UPDATE photos SET score=?, updated_at=? WHERE id=?`, score, now(), id); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ---------- score_cache ----------

// CacheEntry 缓存的评分结果
type CacheEntry struct {
	Dims     Dims
	Tags     []string
	Strength string
	Weakness string
}

// CacheGet 命中跨会话评分缓存
func (s *Store) CacheGet(fingerprint, model, promptVersion string) (*CacheEntry, error) {
	row := s.db.QueryRow(`SELECT dims, tags, strength, weakness FROM score_cache WHERE fingerprint=? AND model=? AND prompt_version=?`,
		fingerprint, model, promptVersion)
	var dimsJSON, tagsJSON string
	var e CacheEntry
	if err := row.Scan(&dimsJSON, &tagsJSON, &e.Strength, &e.Weakness); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	json.Unmarshal([]byte(dimsJSON), &e.Dims)
	json.Unmarshal([]byte(tagsJSON), &e.Tags)
	return &e, nil
}

// CachePut 写入评分缓存
func (s *Store) CachePut(fingerprint, model, promptVersion string, dims Dims, tags []string, strength, weakness string) error {
	dimsJSON, _ := json.Marshal(dims)
	tagsJSON, _ := json.Marshal(tags)
	_, err := s.db.Exec(`INSERT OR REPLACE INTO score_cache(fingerprint, model, prompt_version, dims, tags, strength, weakness, created_at)
		VALUES(?,?,?,?,?,?,?,?)`, fingerprint, model, promptVersion, string(dimsJSON), string(tagsJSON), strength, weakness, now())
	return err
}

// CacheDelete 删除缓存条目（复检强制重调 API 时用）
func (s *Store) CacheDelete(fingerprint, model, promptVersion string) error {
	_, err := s.db.Exec(`DELETE FROM score_cache WHERE fingerprint=? AND model=? AND prompt_version=?`,
		fingerprint, model, promptVersion)
	return err
}

// ---------- spend_log ----------

// SpendToday 当日累计预估费用
func (s *Store) SpendToday(day string) (float64, error) {
	var total float64
	err := s.db.QueryRow(`SELECT COALESCE(SUM(est_cost),0) FROM spend_log WHERE day=?`, day).Scan(&total)
	return total, err
}

// SpendAdd 记录费用消耗
func (s *Store) SpendAdd(day, model string, photos int, estCost float64) error {
	_, err := s.db.Exec(`INSERT INTO spend_log(day, model, photos, est_cost) VALUES(?,?,?,?)`, day, model, photos, estCost)
	return err
}
