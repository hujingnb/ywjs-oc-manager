// Package redis 的 queue_test 覆盖内存队列与 Redis 队列兼容的入队、预留和重试语义。
package redis

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// TestMemoryQueueEnqueueAndReserve 验证当前用户接口mory队列Enqueue并Reserve的预期行为场景。
func TestMemoryQueueEnqueueAndReserve(t *testing.T) {
	queue := NewMemoryQueue()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	queue.SetClock(func() time.Time { return now })

	err := queue.Enqueue(context.Background(), "job-1")
	require.NoError(t, err)
	err = queue.Enqueue(context.Background(), "job-2")
	require.NoError(t, err)

	reserved, err := queue.Reserve(context.Background(), 10)
	require.NoError(t, err)
	if len(reserved) != 2 || reserved[0] != "job-1" || reserved[1] != "job-2" {
		t.Fatalf("reserved = %+v, want [job-1 job-2]", reserved)
	}
	require.Empty(t, queue.Pending())
}

// TestMemoryQueueDelayedEntriesNotVisibleUntilDue 验证当前用户接口mory队列DelayedEntries未VisibleUntilDue的预期行为场景。
func TestMemoryQueueDelayedEntriesNotVisibleUntilDue(t *testing.T) {
	queue := NewMemoryQueue()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	queue.SetClock(func() time.Time { return now })

	err := queue.EnqueueDelayed(context.Background(), "job-future", now.Add(time.Hour))
	require.NoError(t, err)

	reserved, err := queue.Reserve(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, reserved, 0)

	queue.SetClock(func() time.Time { return now.Add(2 * time.Hour) })
	reserved, err = queue.Reserve(context.Background(), 10)
	require.NoError(t, err)
	if len(reserved) != 1 || reserved[0] != "job-future" {
		t.Fatalf("reserved = %+v, want [job-future]", reserved)
	}
}

// TestMemoryQueueDeduplicatesEnqueue 验证当前用户接口mory队列去重Enqueue的特殊分支或幂等场景。
func TestMemoryQueueDeduplicatesEnqueue(t *testing.T) {
	queue := NewMemoryQueue()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	queue.SetClock(func() time.Time { return now })

	for i := 0; i < 3; i++ {
		err := queue.Enqueue(context.Background(), "job-1")
		require.NoError(t, err)
	}
	reserved, err := queue.Reserve(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, reserved, 1)
}

// TestMemoryQueueRespectsLimit 验证当前用户接口mory队列遵守Limit的预期行为场景。
func TestMemoryQueueRespectsLimit(t *testing.T) {
	queue := NewMemoryQueue()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	queue.SetClock(func() time.Time { return now })

	for i := 0; i < 5; i++ {
		err := queue.Enqueue(context.Background(), idForIndex(i))
		require.NoError(t, err)
	}

	first, err := queue.Reserve(context.Background(), 2)
	require.NoError(t, err)
	require.Len(t, first, 2)
	second, err := queue.Reserve(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, second, 3)
}

// TestRedisQueueEnqueueRequiresClient 验证Redis队列Enqueue要求客户端的预期行为场景。
func TestRedisQueueEnqueueRequiresClient(t *testing.T) {
	q := &RedisQueue{}
	err := q.Enqueue(context.Background(), "job-1")
	require.Error(t, err)
}

func idForIndex(i int) string {
	return "job-" + string(rune('a'+i))
}
