//go:build integration

package redis

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestRedisQueue_LiveEnqueueAndReserve 通过真实 Redis 验证 ZSET 读写。
func TestRedisQueue_LiveEnqueueAndReserve(t *testing.T) {
	addr := os.Getenv("INTEGRATION_REDIS_ADDR")
	if addr == "" {
		t.Skip("缺 INTEGRATION_REDIS_ADDR")
	}
	queue := NewRedisQueue(Config{Addr: addr, QueueKey: "ocm:integration:test"})
	defer queue.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	jobID := "00000000-0000-0000-0000-000000000fff"
	if err := queue.Enqueue(ctx, jobID); err != nil {
		t.Fatalf("Enqueue err = %v", err)
	}
	got, err := queue.Reserve(ctx, 10)
	if err != nil {
		t.Fatalf("Reserve err = %v", err)
	}
	found := false
	for _, id := range got {
		if id == jobID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Reserve 未返回 jobID，实际 %+v", got)
	}
}
