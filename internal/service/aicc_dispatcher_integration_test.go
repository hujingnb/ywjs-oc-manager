package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAICCDispatcherObservabilityIntegration 覆盖异步消息调度的关键运行路径：
// 多并发成功、429/503/超时重试、确定性失败、熔断以及重启后的租约回收都必须产生脱敏事件。
func TestAICCDispatcherObservabilityIntegration(t *testing.T) {
	observer := &aiccDispatchObservationRecorder{}
	newDispatcher := func(chat aiccDispatcherChatFake) (*AICCDispatcher, *aiccDispatcherStoreFake) {
		store := newAICCDispatcherStoreFake()
		dispatcher := NewAICCDispatcher(store, aiccDispatcherTxFake{store}, chat, nil)
		dispatcher.SetObserver(observer)
		return dispatcher, store
	}

	// 并发成功场景：多个独立任务完成后均记录 completed，供在飞数与完成量关联分析。
	var workers sync.WaitGroup
	completed := make(chan error, 4)
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			dispatcher, store := newDispatcher(aiccDispatcherChatFake{reply: "答复"})
			completed <- dispatcher.Dispatch(context.Background(), store.task)
		}()
	}
	workers.Wait()
	close(completed)
	for err := range completed {
		require.NoError(t, err)
	}

	// 上游暂态错误场景：429、503 与超时均进入 retry，标签只能描述上游类别和结果。
	for _, upstreamErr := range []error{
		&AICCUpstreamStatusError{StatusCode: 429}, // 上游限流。
		&AICCUpstreamStatusError{StatusCode: 503}, // 上游服务暂不可用。
		context.DeadlineExceeded,                  // 上游调用超时。
	} {
		dispatcher, store := newDispatcher(aiccDispatcherChatFake{err: upstreamErr})
		require.NoError(t, dispatcher.Dispatch(context.Background(), store.task))
	}

	// 确定性失败场景：不可重试错误必须记录 failed，避免监控误计为上游重试。
	failedDispatcher, failedStore := newDispatcher(aiccDispatcherChatFake{err: errors.New("invalid runtime request")})
	require.NoError(t, failedDispatcher.Dispatch(context.Background(), failedStore.task))

	// 熔断场景：连续五次 503 后，下一条任务被拒绝并记录 circuit_open。
	circuitDispatcher, circuitStore := newDispatcher(aiccDispatcherChatFake{err: &AICCUpstreamStatusError{StatusCode: 503}})
	for range 5 {
		require.NoError(t, circuitDispatcher.Dispatch(context.Background(), circuitStore.task))
	}
	require.NoError(t, circuitDispatcher.Dispatch(context.Background(), circuitStore.task))

	// 重启恢复场景：新 dispatcher 回收遗留租约后必须记录 lease_recovered，证明 MySQL 仍是任务事实来源。
	recoveryDispatcher, recoveryStore := newDispatcher(aiccDispatcherChatFake{})
	recoveryStore.recover = 1
	recovered, err := recoveryDispatcher.RecoverExpiredLeases(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), recovered)

	events := observer.events()
	assert.GreaterOrEqual(t, countAICCDispatchEvents(events, "completed"), 4)
	assert.GreaterOrEqual(t, countAICCDispatchEvents(events, "retry"), 8)
	assert.Equal(t, 1, countAICCDispatchEvents(events, "failed"))
	assert.Equal(t, 1, countAICCDispatchEvents(events, "circuit_open"))
	assert.Equal(t, 1, countAICCDispatchEvents(events, "lease_recovered"))
	assert.True(t, hasAICCDispatchResult(events, "retry_http_429"))
	assert.True(t, hasAICCDispatchResult(events, "retry_http_503"))
	assert.True(t, hasAICCDispatchResult(events, "retry_timeout"))
	for _, event := range events {
		assert.Equal(t, "hermes", event.Upstream())
		if event.Event() != "lease_recovered" {
			assert.NotEmpty(t, event.AgentID())
			assert.NotEmpty(t, event.OrgID())
		}
	}
}

// hasAICCDispatchResult 判断稳定结果枚举是否被观测到，避免测试依赖动态错误文本。
func hasAICCDispatchResult(events []AICCDispatchObservation, result string) bool {
	for _, event := range events {
		if event.Result() == result {
			return true
		}
	}
	return false
}

// aiccDispatchObservationRecorder 并发安全地收集 dispatcher 事件，模拟日志/指标接收端。
type aiccDispatchObservationRecorder struct {
	mu       sync.Mutex
	observed []AICCDispatchObservation
}

// Observe 实现 AICCDispatchObserver，供并发 dispatcher 写入测试观测记录。
func (r *aiccDispatchObservationRecorder) Observe(_ context.Context, event AICCDispatchObservation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observed = append(r.observed, event)
}

// events 返回记录快照，避免断言与后台 goroutine 竞争同一切片。
func (r *aiccDispatchObservationRecorder) events() []AICCDispatchObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]AICCDispatchObservation(nil), r.observed...)
}

// countAICCDispatchEvents 按事件名统计记录数，保持集成用例的断言聚焦在可观测契约。
func countAICCDispatchEvents(events []AICCDispatchObservation, name string) int {
	count := 0
	for _, event := range events {
		if event.Event() == name {
			count++
		}
	}
	return count
}
