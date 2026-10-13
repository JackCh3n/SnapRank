package pipeline

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"snaprank/internal/logutil"
	"snaprank/internal/provider"
	"snaprank/internal/store"
)

// poolConsecFailLimit 单池连续失败阈值：达到即判定该池额度不足/平台故障并停止该池
// （只停本池，不影响其它模型池与任务）
const poolConsecFailLimit = 10

// ---------- 可追加的压缩投料队列 ----------

// workQueue 压缩投料队列（运行中可追加）：新目录并入当前批次时把照片推进来，
// 压缩池接着压、各模型池接着评——模型不再空等下一个目录。
type workQueue struct {
	mu        sync.Mutex
	items     []*store.Photo
	ids       map[int64]bool // 已入队照片 ID：重复提交同一目录时不重复入队
	head      int
	done      bool
	appending int // 正在扫描/登记新目录并入：此期间队列暂空也不算收尾
}

func newWorkQueue(ps []*store.Photo) *workQueue {
	q := &workQueue{ids: map[int64]bool{}}
	q.push(ps...)
	return q
}

// push 入队（按照片 ID 去重），返回实际入队数量
func (q *workQueue) push(ps ...*store.Photo) int {
	if len(ps) == 0 {
		return 0
	}
	added := 0
	q.mu.Lock()
	for _, p := range ps {
		if q.ids[p.ID] {
			continue
		}
		q.ids[p.ID] = true
		q.items = append(q.items, p)
		added++
	}
	q.mu.Unlock()
	return added
}

// beginAppend / endAppend 标记「正在并入新目录」：期间即使队列为空也不判收尾
// （目录扫描+登记要几秒，避免批次提前收尾导致新目录落到另一个批次）
func (q *workQueue) beginAppend() {
	q.mu.Lock()
	q.appending++
	q.mu.Unlock()
}

func (q *workQueue) endAppend() {
	q.mu.Lock()
	if q.appending > 0 {
		q.appending--
	}
	q.mu.Unlock()
}

// finish 没有更多照片了（压缩投料收尾）
func (q *workQueue) finish() {
	q.mu.Lock()
	q.done = true
	q.mu.Unlock()
}

// next 取下一张（尚未追加则轮询等待）。队列结束、收到收尾信号、被停止或 ctx
// 取消时返回 (nil,false)。
func (q *workQueue) next(ctx context.Context, stopped *atomic.Bool, quit <-chan struct{}) (*store.Photo, bool) {
	for {
		select {
		case <-quit:
			return nil, false
		default:
		}
		q.mu.Lock()
		if q.head < len(q.items) {
			p := q.items[q.head]
			q.head++
			q.mu.Unlock()
			return p, true
		}
		done := q.done
		q.mu.Unlock()
		if done || ctx.Err() != nil || (stopped != nil && stopped.Load()) {
			return nil, false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// empty 队列已取尽且没有正在并入的目录
func (q *workQueue) empty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.head >= len(q.items) && q.appending == 0
}

// total 已推入总数（含已取走的）
func (q *workQueue) total() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// ---------- 共享压缩产出清单 ----------

// readyList 压缩池产出、各模型池按各自游标消费的共享清单。
// 内容寻址的压缩缓存可复用：运行中新增的模型池从游标 0 起评，
// 不必重新压缩（这正是「换模型继续/追加模型」能即刻开始的原因）。
type readyList struct {
	mu   sync.Mutex
	list []*store.Photo
	done bool // 压缩全部结束（含被停止）
}

func newReadyList() *readyList { return &readyList{} }

func (r *readyList) push(p *store.Photo) {
	r.mu.Lock()
	r.list = append(r.list, p)
	r.mu.Unlock()
}

// finish 压缩结束：等待者遇越界取数立即失败退出
func (r *readyList) finish() {
	r.mu.Lock()
	r.done = true
	r.mu.Unlock()
}

func (r *readyList) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.list)
}

func (r *readyList) finished() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.done
}

// at 取第 i 张（压缩尚未产出则轮询等待）。压缩结束且越界返回 (nil,false)。
// 轮询而非 condvar：需要能被 ctx 取消与单池停止打断。
func (r *readyList) at(ctx context.Context, i int, stopped *atomic.Bool) (*store.Photo, bool) {
	for {
		r.mu.Lock()
		if i < len(r.list) {
			p := r.list[i]
			r.mu.Unlock()
			return p, true
		}
		fin := r.done
		r.mu.Unlock()
		if fin || ctx.Err() != nil || stopped.Load() {
			return nil, false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ---------- 单模型评分池 ----------

// modelPool 一个模型的评分池：独立游标消费共享清单、独立并发与生命周期。
// 额度耗尽/模型不存在等致命错误只停本池（fatal），其它池继续跑。
type modelPool struct {
	prov  provider.Provider
	model string
	label string // 入库标签：同平台=模型 ID；跨平台="预设/模型"

	primary bool
	started time.Time
	endAt   atomic.Int64 // 收尾时刻（UnixNano，0=未结束）：速度按此冻结

	cursor   atomic.Int64 // 下一个待取索引
	done     atomic.Int64
	fail     atomic.Int64
	inflight atomic.Int64
	consec   atomic.Int64 // 连续失败计数
	scale    atomic.Int64 // 本池 429 退避倍率（池间隔离）
	workers  atomic.Int64

	paused  atomic.Bool
	stopped atomic.Bool
	fatal   atomic.Bool
	errMsg  atomic.Value // string
}

func (m *modelPool) status() string {
	switch {
	case m.fatal.Load():
		return "error"
	case m.stopped.Load():
		return "stopped"
	case m.paused.Load():
		return "paused"
	default:
		return "running"
	}
}

// halted 该池是否已不再参与：照片完成度判定时视为已满足（不必等它）
func (m *modelPool) halted() bool { return m.stopped.Load() || m.fatal.Load() }

func (m *modelPool) errText() string {
	v, _ := m.errMsg.Load().(string)
	return v
}

func (m *modelPool) failFatal(reason string) {
	if m.fatal.Swap(true) {
		return // 只上报一次
	}
	m.errMsg.Store(reason)
	logutil.Error("模型池 %s 已停止（不影响其它池）：%s", m.label, reason)
}

// ---------- 运行中池集合 ----------

// runPools 运行中任务的池集合：供运行中暂停/停止单池、追加模型池。
// 锁序：调用方持 photoState.mu 时再取 rp.mu（st.mu → rp.mu），反向不得嵌套。
type runPools struct {
	mu           sync.Mutex
	pools        []*modelPool
	ready        *readyList
	work         *workQueue // 压缩投料队列（运行中可追加新目录的照片）
	planned      int64
	primaryLabel string
	closed       bool             // 收尾中/已收尾：拒绝再追加池
	spawn        func(*modelPool) bool // 由 streamPools 注入；false=任务已收尾
	compress     compressState
}

// AddPhotosToRunning 把新目录的照片并入运行中的批次（压缩池接着压、各模型池接着评）。
// 已压缩过的走内容寻址缓存直接命中，不重新压缩。
func (e *Engine) AddPhotosToRunning(photos []*store.Photo) error {
	rp := e.pools.Load()
	if rp == nil {
		return fmt.Errorf("当前没有运行中的任务")
	}
	if len(photos) == 0 {
		return nil
	}
	rp.mu.Lock()
	if rp.closed {
		rp.mu.Unlock()
		return fmt.Errorf("任务正在收尾，无法并入新目录")
	}
	rp.mu.Unlock()
	added := rp.work.push(photos...)
	if added == 0 {
		return nil // 都已在队列里（重复提交同一目录）
	}
	rp.mu.Lock()
	rp.planned += int64(added)
	rp.mu.Unlock()
	rp.compress.total.Add(int64(added))
	logutil.Info("并入当前批次 %d 张（模型池接着评，压缩缓存复用）", added)
	e.publishPools()
	return nil
}

// compressState 压缩池进度
type compressState struct {
	done     atomic.Int64
	total    atomic.Int64
	finished atomic.Bool
}

// PoolState 单个池对外状态（前端模型池面板）
type PoolState struct {
	Label    string  `json:"label"`
	Model    string  `json:"model"`
	Kind     string  `json:"kind"` // compress | model
	Primary  bool    `json:"primary"`
	Done     int64   `json:"done"`
	Fail     int64   `json:"fail"`
	Total    int64   `json:"total"`
	Inflight int64   `json:"inflight"`
	Speed    float64 `json:"speed"`  // 张/分
	Status   string  `json:"status"` // running | paused | stopped | error | done
	Error    string  `json:"error,omitempty"`
}

// PoolsState 运行中任务的各池状态（无运行任务返回 nil）
func (e *Engine) PoolsState() []PoolState {
	rp := e.pools.Load()
	if rp == nil {
		return nil
	}
	rp.mu.Lock()
	pools := append([]*modelPool(nil), rp.pools...)
	planned := rp.planned
	rp.mu.Unlock()

	cs := PoolState{Label: "压缩", Kind: "compress",
		Done: rp.compress.done.Load(), Total: rp.compress.total.Load()}
	if rp.compress.finished.Load() {
		cs.Status = "done"
	} else {
		cs.Status = "running"
	}
	out := []PoolState{cs}

	readyDone := rp.ready.finished()
	readySize := int64(rp.ready.size())
	for _, p := range pools {
		st := PoolState{
			Label: p.label, Model: p.model, Kind: "model", Primary: p.primary,
			Done: p.done.Load(), Fail: p.fail.Load(), Total: planned,
			Inflight: p.inflight.Load(), Status: p.status(), Error: p.errText(),
		}
		// 清单取尽且无在途 → 该池已完成本轮
		if st.Status == "running" && readyDone && st.Inflight == 0 && p.cursor.Load() >= readySize {
			st.Status = "done"
		}
		end := time.Now()
		if v := p.endAt.Load(); v > 0 {
			end = time.Unix(0, v)
		}
		if el := end.Sub(p.started).Minutes(); el > 0 {
			st.Speed = float64(st.Done) / el
		}
		out = append(out, st)
	}
	return out
}

// publishPools 推送各池状态（前端模型池面板据此刷新）
func (e *Engine) publishPools() {
	if st := e.PoolsState(); st != nil {
		e.Bus.Publish(Event{Type: "pools", Data: st})
	}
}

// ControlPool 控制运行中任务的单个模型池（pause | resume | stop）
func (e *Engine) ControlPool(label, action string) error {
	rp := e.pools.Load()
	if rp == nil {
		return fmt.Errorf("当前没有运行中的任务")
	}
	rp.mu.Lock()
	var target *modelPool
	for _, p := range rp.pools {
		if p.label == label {
			target = p
			break
		}
	}
	rp.mu.Unlock()
	if target == nil {
		return fmt.Errorf("模型池不存在: %s", label)
	}
	switch action {
	case "pause":
		if target.halted() {
			return fmt.Errorf("该池已停止，无法暂停")
		}
		target.paused.Store(true)
		logutil.Info("模型池已暂停: %s（在途请求跑完即挂起，其它池不受影响）", label)
	case "resume":
		target.paused.Store(false)
		logutil.Info("模型池已继续: %s", label)
	case "stop":
		target.stopped.Store(true)
		logutil.Info("模型池已停止: %s（其余池继续；它未评的照片由其它池的结果定稿）", label)
	default:
		return fmt.Errorf("未知操作: %s", action)
	}
	e.publishPools()
	return nil
}

// AddModelToRunning 运行中追加模型池：复用已压缩缓存，从第一张开始评。
// preset 为空=当前平台；非空=跨平台预设（标签 "预设/模型" 防同名冲突）。
func (e *Engine) AddModelToRunning(preset, model string, conc int) error {
	rp := e.pools.Load()
	if rp == nil {
		return fmt.Errorf("当前没有运行中的任务")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return fmt.Errorf("模型 ID 不能为空")
	}
	label := model
	var prov provider.Provider
	if preset != "" {
		p, err := e.providerForPreset(preset)
		if err != nil {
			return err
		}
		prov = p
		label = preset + "/" + model
	} else {
		p, err := provider.New(e.cfgFn())
		if err != nil {
			return err
		}
		prov = p
	}
	if conc <= 0 {
		conc = e.cfgFn().Pipeline.ScoreConcurrency
	}
	rp.mu.Lock()
	if rp.closed {
		rp.mu.Unlock()
		return fmt.Errorf("任务正在收尾，无法追加模型")
	}
	for _, p := range rp.pools {
		if p.label == label {
			rp.mu.Unlock()
			return fmt.Errorf("该模型已在池中: %s", label)
		}
	}
	pool := &modelPool{prov: prov, model: model, label: label, started: time.Now()}
	pool.scale.Store(1)
	rp.pools = append(rp.pools, pool)
	spawn := rp.spawn
	// 持 rp.mu 期间就起 worker：收尾判定（同持 rp.mu）不会把刚追加的池漏算成 0 worker
	if spawn != nil {
		for i := 0; i < conc; i++ {
			if !spawn(pool) {
				break
			}
		}
	}
	rp.mu.Unlock()

	logutil.Info("运行中追加模型池: %s（复用压缩缓存，从第一张开始，并发 %d）", label, conc)
	e.publishPools()
	return nil
}
