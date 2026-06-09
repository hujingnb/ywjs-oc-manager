// Package service 的 leader_elector_test 覆盖 LeaderElector 基于 Redis 锁的单 leader 选举逻辑。
// 用内存版 fakeLeaderLocker 替代真实 Redis,验证:抢锁后持续续租保持 leader、
// 第二个副本在 leader 在位期间始终非 leader、以及 holder 被清空(模拟 leader 崩溃)后他人接管。
package service

import (
	"context"
	"log/slog"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLeaderLocker 是 leaderLocker 的内存实现:用单个 holder 字符串表示当前持锁 token。
// 不模拟 TTL 过期(单元测试用 dropHolder 主动清空来模拟 leader 崩溃/租约到期)。
type fakeLeaderLocker struct {
	mu     sync.Mutex
	holder string
}

// TryAcquire 仅当 holder 为空时抢锁成功,把 holder 置为 token。
func (f *fakeLeaderLocker) TryAcquire(_ context.Context, _, token string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.holder == "" {
		f.holder = token
		return true, nil
	}
	return false, nil
}

// Refresh 仅当 holder 等于 token 时视为续租成功;否则(被别人持有或已被清空)返回 false。
func (f *fakeLeaderLocker) Refresh(_ context.Context, _, token string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.holder == token, nil
}

// Release 仅当 holder 等于 token 时清空 holder,防止误删他人锁。
func (f *fakeLeaderLocker) Release(_ context.Context, _, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.holder == token {
		f.holder = ""
	}
	return nil
}

// dropHolder 主动清空 holder,用于在测试中模拟 leader 副本崩溃 / 租约到期后锁被释放。
func (f *fakeLeaderLocker) dropHolder() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holder = ""
}

// testLogger 返回一个丢弃输出的 slog.Logger,避免测试噪声。
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestLeaderElector 汇总 LeaderElector 的选举行为用例。
func TestLeaderElector(t *testing.T) {
	// 选用宽松计时:lease 60ms / interval 20ms,既保证测试快,又给续租留足容错。
	const (
		lease    = 60 * time.Millisecond
		interval = 20 * time.Millisecond
	)

	// AcquiresAndHolds:单个 elector 应抢到 leader,并在多个 interval 内持续续租保持 leader。
	t.Run("AcquiresAndHolds", func(t *testing.T) {
		locker := &fakeLeaderLocker{}
		e := NewLeaderElector(locker, "leader/key", "token-1", lease, interval)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = e.Run(ctx, testLogger()) }()

		// 等待初次当选。
		require.Eventually(t, e.IsLeader, time.Second, 5*time.Millisecond, "elector 应当成功抢到 leader")

		// 跨越多个续租周期后仍应保持 leader(Refresh 持续成功)。
		time.Sleep(5 * interval)
		assert.True(t, e.IsLeader(), "elector 应在多个 interval 内持续保持 leader")
	})

	// SecondInstanceNotLeader:elector1 持锁期间,针对同一 fake 启动的 elector2 始终不应成为 leader。
	t.Run("SecondInstanceNotLeader", func(t *testing.T) {
		locker := &fakeLeaderLocker{}
		e1 := NewLeaderElector(locker, "leader/key", "token-1", lease, interval)
		e2 := NewLeaderElector(locker, "leader/key", "token-2", lease, interval)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = e1.Run(ctx, testLogger()) }()
		// 先让 e1 稳定当选,再启动 e2,避免抢锁竞态导致谁先谁后不确定。
		require.Eventually(t, e1.IsLeader, time.Second, 5*time.Millisecond, "elector1 应先成为 leader")

		go func() { _ = e2.Run(ctx, testLogger()) }()

		// 观察多个 interval,期间 e2 始终不应当选(e1 一直续租持锁)。
		require.Never(t, e2.IsLeader, 6*interval, 5*time.Millisecond, "elector1 在位时 elector2 不应成为 leader")
		assert.True(t, e1.IsLeader(), "elector1 应始终保持 leader")
	})

	// TakeoverAfterLoss:模拟 leader 崩溃(清空 holder)后,另一个 elector 应最终接管成为 leader。
	t.Run("TakeoverAfterLoss", func(t *testing.T) {
		locker := &fakeLeaderLocker{}
		e1 := NewLeaderElector(locker, "leader/key", "token-1", lease, interval)
		e2 := NewLeaderElector(locker, "leader/key", "token-2", lease, interval)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = e1.Run(ctx, testLogger()) }()
		require.Eventually(t, e1.IsLeader, time.Second, 5*time.Millisecond, "elector1 应先成为 leader")

		// 启动 e2(此时应一直非 leader)。
		go func() { _ = e2.Run(ctx, testLogger()) }()

		// 模拟 e1 副本崩溃:锁被清空。e1 下一轮 Refresh 失败应丢 leader,e2 下一轮 TryAcquire 应接管。
		locker.dropHolder()

		require.Eventually(t, e2.IsLeader, time.Second, 5*time.Millisecond, "holder 清空后 elector2 应接管成为 leader")
	})
}
