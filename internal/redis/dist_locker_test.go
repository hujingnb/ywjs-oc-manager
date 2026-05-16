package redis

import (
	"context"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestLocker 连接本地 manager-redis (localhost:6379 / password=123456)。
// 没有 redis 时 Skip 而非 Fail,与 redis_integration_test.go 风格一致。
// 每次 cleanup 调 FlushDB 清测试数据。
func newTestLocker(t *testing.T) (*RedisDistLocker, func()) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379", Password: "123456"})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skip("本地 Redis 不可用,跳过 DistLocker 集成测试: " + err.Error())
	}
	locker := NewRedisDistLocker(client)
	cleanup := func() {
		_ = client.FlushDB(context.Background()).Err()
		_ = client.Close()
	}
	return locker, cleanup
}

// TestDistLocker_TryAcquire_NewKey 第一个抢锁的进程拿到锁。
func TestDistLocker_TryAcquire_NewKey(t *testing.T) {
	locker, cleanup := newTestLocker(t)
	defer cleanup()
	ok, err := locker.TryAcquire(context.Background(), "ocm:test:key", "tok-A", 5*time.Second)
	require.NoError(t, err)
	assert.True(t, ok)
}

// TestDistLocker_TryAcquire_Conflict 已经被别人持有时第二个返回 false。
func TestDistLocker_TryAcquire_Conflict(t *testing.T) {
	locker, cleanup := newTestLocker(t)
	defer cleanup()
	ok, err := locker.TryAcquire(context.Background(), "ocm:test:key", "tok-A", 5*time.Second)
	require.NoError(t, err)
	require.True(t, ok)
	ok2, err := locker.TryAcquire(context.Background(), "ocm:test:key", "tok-B", 5*time.Second)
	require.NoError(t, err)
	assert.False(t, ok2)
}

// TestDistLocker_Release_TokenMatch 自己的 token 才能释放;别人 token 释放无效。
// 防止 leader 超时丢锁后,误删别人新拿到的锁。
func TestDistLocker_Release_TokenMatch(t *testing.T) {
	locker, cleanup := newTestLocker(t)
	defer cleanup()
	_, _ = locker.TryAcquire(context.Background(), "ocm:test:key", "tok-A", 5*time.Second)

	// tok-B 误尝试释放,锁仍然属于 tok-A
	require.NoError(t, locker.Release(context.Background(), "ocm:test:key", "tok-B"))
	exists, err := locker.Exists(context.Background(), "ocm:test:key")
	require.NoError(t, err)
	assert.True(t, exists)

	// tok-A 释放成功后 key 消失
	require.NoError(t, locker.Release(context.Background(), "ocm:test:key", "tok-A"))
	exists, err = locker.Exists(context.Background(), "ocm:test:key")
	require.NoError(t, err)
	assert.False(t, exists)
}

// TestDistLocker_Renew_TokenMatch token 一致才能续期;过期后续期不复活。
func TestDistLocker_Renew_TokenMatch(t *testing.T) {
	locker, cleanup := newTestLocker(t)
	defer cleanup()
	_, _ = locker.TryAcquire(context.Background(), "ocm:test:key", "tok-A", 1*time.Second)

	// 续期到 5 秒,等 1.5s 验证锁还在(若没续期早就过期)
	require.NoError(t, locker.Renew(context.Background(), "ocm:test:key", "tok-A", 5*time.Second))
	time.Sleep(1500 * time.Millisecond)
	exists, err := locker.Exists(context.Background(), "ocm:test:key")
	require.NoError(t, err)
	assert.True(t, exists)

	// 别人 token 续期不动 TTL 也不报错
	require.NoError(t, locker.Renew(context.Background(), "ocm:test:key", "tok-B", 60*time.Second))
}
