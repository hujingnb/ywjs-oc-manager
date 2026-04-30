package redis

import (
	"context"
	"testing"
	"time"
)

func TestMemoryQueueEnqueueAndReserve(t *testing.T) {
	queue := NewMemoryQueue()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	queue.SetClock(func() time.Time { return now })

	if err := queue.Enqueue(context.Background(), "job-1"); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if err := queue.Enqueue(context.Background(), "job-2"); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	reserved, err := queue.Reserve(context.Background(), 10)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if len(reserved) != 2 || reserved[0] != "job-1" || reserved[1] != "job-2" {
		t.Fatalf("reserved = %+v, want [job-1 job-2]", reserved)
	}
	if pending := queue.Pending(); len(pending) != 0 {
		t.Fatalf("pending = %+v, want empty", pending)
	}
}

func TestMemoryQueueDelayedEntriesNotVisibleUntilDue(t *testing.T) {
	queue := NewMemoryQueue()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	queue.SetClock(func() time.Time { return now })

	if err := queue.EnqueueDelayed(context.Background(), "job-future", now.Add(time.Hour)); err != nil {
		t.Fatalf("EnqueueDelayed() error = %v", err)
	}

	reserved, err := queue.Reserve(context.Background(), 10)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if len(reserved) != 0 {
		t.Fatalf("expected no reserved before due time, got %+v", reserved)
	}

	queue.SetClock(func() time.Time { return now.Add(2 * time.Hour) })
	reserved, err = queue.Reserve(context.Background(), 10)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if len(reserved) != 1 || reserved[0] != "job-future" {
		t.Fatalf("reserved = %+v, want [job-future]", reserved)
	}
}

func TestMemoryQueueDeduplicatesEnqueue(t *testing.T) {
	queue := NewMemoryQueue()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	queue.SetClock(func() time.Time { return now })

	for i := 0; i < 3; i++ {
		if err := queue.Enqueue(context.Background(), "job-1"); err != nil {
			t.Fatalf("Enqueue() error = %v", err)
		}
	}
	reserved, err := queue.Reserve(context.Background(), 10)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if len(reserved) != 1 {
		t.Fatalf("expected dedup, got %+v", reserved)
	}
}

func TestMemoryQueueRespectsLimit(t *testing.T) {
	queue := NewMemoryQueue()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	queue.SetClock(func() time.Time { return now })

	for i := 0; i < 5; i++ {
		if err := queue.Enqueue(context.Background(), idForIndex(i)); err != nil {
			t.Fatalf("Enqueue() error = %v", err)
		}
	}

	first, err := queue.Reserve(context.Background(), 2)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("first batch len = %d, want 2", len(first))
	}
	second, err := queue.Reserve(context.Background(), 10)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if len(second) != 3 {
		t.Fatalf("second batch len = %d, want 3", len(second))
	}
}

func TestRedisQueueEnqueueRequiresClient(t *testing.T) {
	q := &RedisQueue{}
	if err := q.Enqueue(context.Background(), "job-1"); err == nil {
		t.Fatalf("expected error when client missing")
	}
}

func idForIndex(i int) string {
	return "job-" + string(rune('a'+i))
}
