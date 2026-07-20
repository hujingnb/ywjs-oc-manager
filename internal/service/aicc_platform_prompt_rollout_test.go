package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/config"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// TestAICCPlatformPromptRolloutCoordinatorCreatesOneJobForStaleAgents 验证存在提示词 hash 落后的活跃客服时，启动协调器仅创建一个独立任务并即时通知 worker。
func TestAICCPlatformPromptRolloutCoordinatorCreatesOneJobForStaleAgents(t *testing.T) {
	store := &fakePromptRolloutStore{hasStale: true}
	notifier := &fakePromptRolloutNotifier{}
	coordinator := newPromptRolloutCoordinatorForTest(store, notifier)

	require.NoError(t, coordinator.EnqueueIfNeeded(context.Background()))
	require.Len(t, store.jobs, 1)
	assert.Equal(t, domain.JobTypeAICCPlatformPromptRollout, store.jobs[0].Type)
	assert.Equal(t, int32(100), store.jobs[0].Priority)
	assert.Equal(t, int32(20), store.jobs[0].MaxAttempts)
	assert.JSONEq(t, `{"target_prompt_hash":"`+config.PlatformPromptHash(domain.AppTypeAICC)+`"}`, string(store.jobs[0].PayloadJson))
	assert.Equal(t, []string{store.jobs[0].ID}, notifier.jobIDs)
}

// TestAICCPlatformPromptRolloutCoordinatorSkipsWhenNoStaleAgents 验证所有活跃客服已使用当前提示词时，不创建无意义重启任务。
func TestAICCPlatformPromptRolloutCoordinatorSkipsWhenNoStaleAgents(t *testing.T) {
	store := &fakePromptRolloutStore{}
	coordinator := newPromptRolloutCoordinatorForTest(store, &fakePromptRolloutNotifier{})

	require.NoError(t, coordinator.EnqueueIfNeeded(context.Background()))
	assert.Empty(t, store.jobs)
}

// TestAICCPlatformPromptRolloutCoordinatorSkipsWhenActiveJobExists 验证已有 pending 或 running 同类任务时，启动协调器不重复创建全局 rollout。
func TestAICCPlatformPromptRolloutCoordinatorSkipsWhenActiveJobExists(t *testing.T) {
	for _, status := range []string{domain.JobStatusPending, domain.JobStatusRunning} {
		t.Run(status, func(t *testing.T) {
			// pending/running 都表示已有任务持有本轮平台提示词下发。
			store := &fakePromptRolloutStore{hasActive: true, hasStale: true}
			coordinator := newPromptRolloutCoordinatorForTest(store, &fakePromptRolloutNotifier{})

			require.NoError(t, coordinator.EnqueueIfNeeded(context.Background()))
			assert.Empty(t, store.jobs)
		})
	}
}

// TestAICCPlatformPromptRolloutCoordinatorCreatesSuccessorOnlyAfterOldJobSucceeded 验证后继调度的真实生命周期：
// 旧 hash job 仍 running 时 coordinator 必须跳过；worker 标记 succeeded 后，同一 singleton guard 才创建并通知一个新任务。
func TestAICCPlatformPromptRolloutCoordinatorCreatesSuccessorOnlyAfterOldJobSucceeded(t *testing.T) {
	store := &fakePromptRolloutStore{hasActive: true, hasStale: true}
	notifier := &fakePromptRolloutNotifier{}
	coordinator := newPromptRolloutCoordinatorForTest(store, notifier)

	// 场景：handler 完成但旧任务仍处于 running，不能过早或重复创建 successor。
	require.NoError(t, coordinator.EnqueueIfNeeded(context.Background()))
	assert.Empty(t, store.jobs)
	assert.Empty(t, notifier.jobIDs)

	// 场景：成功前 hook 排除当前 running 旧任务；当前 hash 仍有落后客服时恰好创建并通知一个 successor。
	store.hasActive = false
	require.NoError(t, coordinator.EnqueueIfNeededExcluding(context.Background(), "old-running-job"))
	require.Len(t, store.jobs, 1)
	assert.Equal(t, []string{store.jobs[0].ID}, notifier.jobIDs)

	// 场景：同一成功后回调被重复触发时，新任务已 active，singleton 语义不再重复创建。
	require.NoError(t, coordinator.EnqueueIfNeededExcluding(context.Background(), "old-running-job"))
	assert.Len(t, store.jobs, 1)
	assert.Len(t, notifier.jobIDs, 1)
}

// TestAICCPlatformPromptRolloutCoordinatorExcludingCurrentStillBlocksOtherActiveJob 验证成功前回调仅排除自己；
// 存在另一份 pending/running 提示词任务时，不能重复创建 successor。
func TestAICCPlatformPromptRolloutCoordinatorExcludingCurrentStillBlocksOtherActiveJob(t *testing.T) {
	store := &fakePromptRolloutStore{hasActive: true, hasStale: true}
	notifier := &fakePromptRolloutNotifier{}

	require.NoError(t, newPromptRolloutCoordinatorForTest(store, notifier).EnqueueIfNeededExcluding(context.Background(), "old-running-job"))
	assert.Empty(t, store.jobs)
	assert.Empty(t, notifier.jobIDs)
}

// TestAICCPlatformPromptRolloutCoordinatorPropagatesStoreErrors 验证检查或创建任务失败会阻止启动，不能静默遗漏提示词下发。
func TestAICCPlatformPromptRolloutCoordinatorPropagatesStoreErrors(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		store *fakePromptRolloutStore
		err   error
	}{
		// 活跃任务检查失败时无法安全判断重复创建，必须中止启动。
		{name: "检查活跃任务失败", err: errors.New("active query failed")},
		// 客服落后状态检查失败时不能误判为无需下发。
		{name: "检查落后客服失败", err: errors.New("stale query failed")},
		// 已确认需下发但写入任务失败时不能通知不存在的任务。
		{name: "创建任务失败", err: errors.New("create failed")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// 每一类 store 错误均需原样保留，以便启动日志能定位数据库阶段。
			store := testCase.store
			if store == nil {
				store = &fakePromptRolloutStore{}
			}
			switch testCase.name {
			case "检查活跃任务失败":
				store.activeErr = testCase.err
			case "检查落后客服失败":
				store.staleErr = testCase.err
			case "创建任务失败":
				store.hasStale = true
				store.createErr = testCase.err
			}
			notifier := &fakePromptRolloutNotifier{}
			coordinator := newPromptRolloutCoordinatorForTest(store, notifier)

			err := coordinator.EnqueueIfNeeded(context.Background())
			require.ErrorIs(t, err, testCase.err)
			assert.Empty(t, store.jobs)
			assert.Empty(t, notifier.jobIDs)
		})
	}
}

// TestAICCPlatformPromptRolloutCoordinatorSerializesConcurrentStarts 验证多个服务副本同时启动时，事务 guard 会串行化检查和创建，最终只保留一个任务。
func TestAICCPlatformPromptRolloutCoordinatorSerializesConcurrentStarts(t *testing.T) {
	store := &fakePromptRolloutStore{hasStale: true}
	runner := &fakePromptRolloutTxRunner{store: store}
	first := NewAICCPlatformPromptRolloutCoordinator(runner, &fakePromptRolloutNotifier{})
	second := NewAICCPlatformPromptRolloutCoordinator(runner, &fakePromptRolloutNotifier{})

	var waitGroup sync.WaitGroup
	errs := make(chan error, 2)
	for _, coordinator := range []*AICCPlatformPromptRolloutCoordinator{first, second} {
		// 两个协调器模拟两个 manager-api 副本在同一时刻进入启动阶段。
		waitGroup.Add(1)
		go func(coordinator *AICCPlatformPromptRolloutCoordinator) {
			defer waitGroup.Done()
			errs <- coordinator.EnqueueIfNeeded(context.Background())
		}(coordinator)
	}
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		// guard 成功串行化后，两次启动均可正常结束。
		require.NoError(t, err)
	}
	require.Len(t, store.jobs, 1)
}

// fakePromptRolloutStore 以最小内存状态记录协调器依赖的查询和写入。
type fakePromptRolloutStore struct {
	hasActive bool
	hasStale  bool
	activeErr error
	staleErr  error
	createErr error
	jobs      []sqlc.CreateJobParams
}

// HasActiveAICCPlatformPromptRolloutJob 返回测试预置的同类活跃任务状态。
func (s *fakePromptRolloutStore) HasActiveAICCPlatformPromptRolloutJob(context.Context) (bool, error) {
	return s.hasActive, s.activeErr
}

// HasOtherActiveAICCPlatformPromptRolloutJob 模拟排除当前 running 旧任务后的其它活跃任务判断。
func (s *fakePromptRolloutStore) HasOtherActiveAICCPlatformPromptRolloutJob(context.Context, string) (bool, error) {
	return s.hasActive, s.activeErr
}

// HasStaleAICCPlatformPromptAgents 返回测试预置的提示词落后客服状态。
func (s *fakePromptRolloutStore) HasStaleAICCPlatformPromptAgents(context.Context, string) (bool, error) {
	return s.hasStale, s.staleErr
}

// CreateJob 记录创建参数，或返回测试预置的数据库故障。
func (s *fakePromptRolloutStore) CreateJob(_ context.Context, arg sqlc.CreateJobParams) error {
	if s.createErr != nil {
		return s.createErr
	}
	s.jobs = append(s.jobs, arg)
	s.hasActive = true
	return nil
}

// fakePromptRolloutTxRunner 用互斥锁模拟 guard 行的 SELECT ... FOR UPDATE，使闭包内的检查与创建不可交错。
type fakePromptRolloutTxRunner struct {
	mu    sync.Mutex
	store *fakePromptRolloutStore
}

// WithAICCPlatformPromptRolloutTx 串行执行闭包，模拟数据库事务先锁定 singleton guard 行。
func (r *fakePromptRolloutTxRunner) WithAICCPlatformPromptRolloutTx(ctx context.Context, fn func(AICCPlatformPromptRolloutStore) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fn(r.store)
}

// newPromptRolloutCoordinatorForTest 为测试装配模拟事务 runner，确保每个断言走生产所需的原子边界。
func newPromptRolloutCoordinatorForTest(store *fakePromptRolloutStore, notifier JobNotifier) *AICCPlatformPromptRolloutCoordinator {
	return NewAICCPlatformPromptRolloutCoordinator(&fakePromptRolloutTxRunner{store: store}, notifier)
}

// fakePromptRolloutNotifier 记录成功创建后发往队列的任务 ID。
type fakePromptRolloutNotifier struct {
	jobIDs []string
}

// Enqueue 记录队列通知，供协调器测试断言。
func (n *fakePromptRolloutNotifier) Enqueue(_ context.Context, jobID string) error {
	n.jobIDs = append(n.jobIDs, jobID)
	return nil
}
