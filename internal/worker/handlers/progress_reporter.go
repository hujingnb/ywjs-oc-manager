// progress_reporter.go 实现 worker handler 侧的 apps.progress_* 节流落库。
// ImageCoordinator 在 pull / sync 过程中通过 ProgressBus 广播进度,handler 用
// progressReporter 把高频事件合并写库,避免 hot loop 把数据库压垮。
package handlers

import (
	"context"
	"time"

	null "github.com/guregu/null/v5"

	ocredis "oc-manager/internal/redis"
	"oc-manager/internal/store/sqlc"
)

// ProgressStore 是 progressReporter 写库需要的最小能力,
// 由 sqlc 生成的 SetAppProgress query 满足。AppInitializeStore 接口也已包含同方法。
type ProgressStore interface {
	SetAppProgress(ctx context.Context, arg sqlc.SetAppProgressParams) error
}

// progressReporter 节流 ImageCoordinator 来的 ProgressEvent 落库。
// 节流规则:距离上次 flush ≥ 1s 或 current 增量 ≥ total*5% 触发写库;
// 首条事件无条件 flush;阶段切换时 transitionTo → FlushReset 强制写一条 NULL/NULL。
//
// 不是线程安全:由 worker handler 在单 goroutine 顺序调用。
type progressReporter struct {
	// appID 对应 apps.id,所有写入都打在同一行。
	appID string
	// store 是写库出口,生产由 sqlc.Queries 实现。
	store ProgressStore
	// lastFlush 上次真正写库的时间;零值表示尚未发生过 flush。
	lastFlush time.Time
	// lastCurrent 上次写库时的 Current 值,用于计算大跳跃判定。
	lastCurrent int64
	// now 提供当前时间,生产用 time.Now,测试可替换为固定时钟。
	now func() time.Time
}

// newProgressReporter 创建实例。生产用 time.Now,测试可替换。
func newProgressReporter(appID string, store ProgressStore) *progressReporter {
	return &progressReporter{appID: appID, store: store, now: time.Now}
}

// Receive 收到 ProgressEvent 后判断是否落库;失败仅记日志(由调用方处理),
// 不阻塞主流程。ctx 已取消时直接跳过,避免向已死的连接发请求。
func (r *progressReporter) Receive(ctx context.Context, ev ocredis.ProgressEvent) {
	if ctx.Err() != nil {
		return
	}
	now := r.now()
	if r.lastFlush.IsZero() {
		// 首条事件无条件写库,让前端立刻看到进度
		r.flush(ctx, ev, now)
		return
	}
	timeOK := now.Sub(r.lastFlush) >= time.Second
	// 5% 阈值:current 增量 ≥ total/20。total=0 时 jumpOK=false,只靠时间节流。
	jumpOK := ev.Total > 0 && ev.Current-r.lastCurrent >= ev.Total/20
	if timeOK || jumpOK {
		r.flush(ctx, ev, now)
	}
}

// FlushReset 由 transitionTo 在阶段切换时调用,写入 NULL/NULL 让进度归零。
// 新阶段从 0 开始展示,且首条 Receive 也会无条件 flush。
func (r *progressReporter) FlushReset(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	_ = r.store.SetAppProgress(ctx, sqlc.SetAppProgressParams{
		ID:              r.appID,
		ProgressCurrent: null.Int{},
		ProgressTotal:   null.Int{},
	})
	r.lastFlush = time.Time{}
	r.lastCurrent = 0
}

// flush 真正写库,更新内部节流状态。
// progress_current=0 / total=0 视为"不定进度"(Valid=false 让 SQL 写 NULL)。
func (r *progressReporter) flush(ctx context.Context, ev ocredis.ProgressEvent, now time.Time) {
	_ = r.store.SetAppProgress(ctx, sqlc.SetAppProgressParams{
		ID:              r.appID,
		ProgressCurrent: null.NewInt(ev.Current, ev.Current > 0 || ev.Total > 0),
		ProgressTotal:   null.NewInt(ev.Total, ev.Total > 0),
	})
	r.lastFlush = now
	r.lastCurrent = ev.Current
}
