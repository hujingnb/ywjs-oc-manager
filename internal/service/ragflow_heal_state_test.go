package service

import (
	"context"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestHealRedis 连接本地 manager-redis，缺失时 Skip。
// 使用 DB 13，与其它测试包（DB 11: redis包、DB 12: imagecoord包）隔离，
// 避免 FlushDB 清测试数据时互相干扰。
func newTestHealRedis(t *testing.T) (*redis.Client, func()) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379", Password: "123456", DB: 13})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skip("本地 Redis 不可用，跳过 HealState 集成测试: " + err.Error())
	}
	cleanup := func() {
		_ = client.FlushDB(context.Background()).Err()
		_ = client.Close()
	}
	return client, cleanup
}

// TestHealState 验证 HealState 自愈状态簿记的核心语义：
// 初始状态、首次尝试、累计计数、放弃标记。
func TestHealState(t *testing.T) {
	client, cleanup := newTestHealRedis(t)
	defer cleanup()

	ctx := context.Background()

	// 构造 HealState：前缀 "ocm:test:"，TTL 较短方便复测不残留
	hs := NewHealState(client, "ocm:test:", HealStateTTL{
		Attempts: 6 * time.Hour,
		Giveup:   7 * 24 * time.Hour,
	})

	docID := "doc-heal-test-001"

	// 全新文档：GivenUp 和 InCooldown 均应为 false（无任何 Redis key）
	givenUp, err := hs.GivenUp(ctx, docID)
	require.NoError(t, err)
	assert.False(t, givenUp, "全新文档不应处于 giveup 状态")

	inCooldown, err := hs.InCooldown(ctx, docID)
	require.NoError(t, err)
	assert.False(t, inCooldown, "全新文档不应处于 cooldown 冷却状态")

	// 首次 RecordAttempt：仅计数（拆分后不再有冷却副作用），计数应为 1，且此时仍不在 cooldown
	count, err := hs.RecordAttempt(ctx, docID)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "首次尝试后计数应为 1")

	inCooldown, err = hs.InCooldown(ctx, docID)
	require.NoError(t, err)
	assert.False(t, inCooldown, "RecordAttempt 本身不再设置 cooldown，未调用 SetCooldown 前不应处于冷却")

	// 调用 SetCooldown 后才进入 cooldown：验证计数与冷却已解耦
	err = hs.SetCooldown(ctx, docID, 10*time.Minute)
	require.NoError(t, err)

	inCooldown, err = hs.InCooldown(ctx, docID)
	require.NoError(t, err)
	assert.True(t, inCooldown, "调用 SetCooldown（d>0）后应处于 cooldown")

	// 第二次 RecordAttempt：计数累加为 2
	count, err = hs.RecordAttempt(ctx, docID)
	require.NoError(t, err)
	assert.Equal(t, 2, count, "第二次尝试后计数应为 2")

	// 第三次 RecordAttempt：计数累加为 3
	count, err = hs.RecordAttempt(ctx, docID)
	require.NoError(t, err)
	assert.Equal(t, 3, count, "第三次尝试后计数应为 3")

	// MarkGivenUp 后 GivenUp 应返回 true
	err = hs.MarkGivenUp(ctx, docID)
	require.NoError(t, err)

	givenUp, err = hs.GivenUp(ctx, docID)
	require.NoError(t, err)
	assert.True(t, givenUp, "MarkGivenUp 后 GivenUp 应返回 true")
}

// TestHealState_RecordAttemptNoCooldown 验证 RecordAttempt 自身不产生 cooldown 副作用。
// 拆分后冷却由 SetCooldown 单独负责，自愈逻辑在达到上限直接放弃时可只计数、不设冷却。
func TestHealState_RecordAttemptNoCooldown(t *testing.T) {
	client, cleanup := newTestHealRedis(t)
	defer cleanup()

	ctx := context.Background()
	hs := NewHealState(client, "ocm:test:", HealStateTTL{
		Attempts: 6 * time.Hour,
		Giveup:   7 * 24 * time.Hour,
	})

	docID := "doc-heal-test-nocooldown"

	// 只调用 RecordAttempt：计数从 1 开始，但不应设 cooldown
	count, err := hs.RecordAttempt(ctx, docID)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "RecordAttempt 计数应从 1 开始")

	inCooldown, err := hs.InCooldown(ctx, docID)
	require.NoError(t, err)
	assert.False(t, inCooldown, "仅 RecordAttempt 不应设置 cooldown key")
}
