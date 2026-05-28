// progress_reporter_test.go 覆盖 progressReporter 的节流落库逻辑:
// 首条无条件 flush、1s 时间窗节流、5% 大跳跃跳节流、阶段切换 FlushReset 写 NULL/NULL。
package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ocredis "oc-manager/internal/redis"
	"oc-manager/internal/store/sqlc"
)

// fakeProgressStore 记录每次写库参数,供断言。
// 实现 ProgressStore 接口,worker handler 的 AppInitializeStore 也满足同方法。
// SetAppProgress 迁移后仅返回 error（:exec 语义）。
type fakeProgressStore struct {
	calls []sqlc.SetAppProgressParams
}

// SetAppProgress 仅把调用参数追加到切片,不真实写库；:exec 语义仅返回 error。
func (f *fakeProgressStore) SetAppProgress(_ context.Context, p sqlc.SetAppProgressParams) error {
	f.calls = append(f.calls, p)
	return nil
}

// newReporter 创建一个带固定时钟的 reporter,供测试控制节流边界。
// appID 迁移为 string（MySQL uuid），此处用空字符串表示测试用的零值 ID。
func newReporter(store *fakeProgressStore, now func() time.Time) *progressReporter {
	r := newProgressReporter("", store)
	r.now = now
	return r
}

// TestProgressReporter_FirstEventFlushes 第一条事件无论间隔都立即 flush,
// 让前端立刻看到进度,避免"空白几秒钟"的视觉。
func TestProgressReporter_FirstEventFlushes(t *testing.T) {
	store := &fakeProgressStore{}
	t0 := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	r := newReporter(store, func() time.Time { return t0 })
	r.Receive(context.Background(), ocredis.ProgressEvent{Current: 100, Total: 1000})
	require.Len(t, store.calls, 1)
}

// TestProgressReporter_ThrottlesByTime 1s 内的后续小增量事件被节流,不写库。
func TestProgressReporter_ThrottlesByTime(t *testing.T) {
	store := &fakeProgressStore{}
	t0 := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	now := t0
	r := newReporter(store, func() time.Time { return now })
	r.Receive(context.Background(), ocredis.ProgressEvent{Current: 100, Total: 1000})
	now = t0.Add(500 * time.Millisecond)
	r.Receive(context.Background(), ocredis.ProgressEvent{Current: 110, Total: 1000})
	assert.Len(t, store.calls, 1, "1s 内的小增量应被节流")
}

// TestProgressReporter_FlushOnLargeJump 增量 ≥ total*5% 立即 flush 不等 1s。
func TestProgressReporter_FlushOnLargeJump(t *testing.T) {
	store := &fakeProgressStore{}
	t0 := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	now := t0
	r := newReporter(store, func() time.Time { return now })
	r.Receive(context.Background(), ocredis.ProgressEvent{Current: 100, Total: 1000})
	now = t0.Add(200 * time.Millisecond)
	r.Receive(context.Background(), ocredis.ProgressEvent{Current: 200, Total: 1000}) // +10%
	assert.Len(t, store.calls, 2, "10% 增量应跳过节流")
}

// TestProgressReporter_FlushReset transitionTo 调用时强制 flush 一条 NULL/NULL,
// 让前端立刻看到新阶段从 0 开始(progress_total=NULL 等价不定进度)。
func TestProgressReporter_FlushReset(t *testing.T) {
	store := &fakeProgressStore{}
	t0 := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	r := newReporter(store, func() time.Time { return t0 })
	r.FlushReset(context.Background())
	require.Len(t, store.calls, 1)
	// 阶段切换写入应该是 NULL/NULL,即 pgtype.Int8.Valid=false
	assert.False(t, store.calls[0].ProgressCurrent.Valid)
	assert.False(t, store.calls[0].ProgressTotal.Valid)
}
