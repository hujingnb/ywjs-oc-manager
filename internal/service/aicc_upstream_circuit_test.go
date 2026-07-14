package service

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRedisAICCUpstreamCircuitClearsHalfOpenState 验证一个副本完成半开探测后，其他副本立即观察到关闭状态。
func TestRedisAICCUpstreamCircuitClearsHalfOpenState(t *testing.T) {
	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer clientA.Close()
	defer clientB.Close()
	circuitA := NewRedisAICCUpstreamCircuit(clientA, "test:", 1, time.Second, 100, 100*time.Millisecond)
	circuitB := NewRedisAICCUpstreamCircuit(clientB, "test:", 1, time.Second, 100, 100*time.Millisecond)
	require.NoError(t, circuitA.RecordOverload(context.Background(), "hermes"))
	allowed, err := circuitB.Allow(context.Background(), "hermes")
	require.NoError(t, err)
	assert.False(t, allowed)
	time.Sleep(120 * time.Millisecond)
	allowed, err = circuitB.Allow(context.Background(), "hermes")
	require.NoError(t, err)
	assert.True(t, allowed)
	require.NoError(t, circuitB.RecordSuccess(context.Background(), "hermes"))
	allowed, err = circuitA.Allow(context.Background(), "hermes")
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.False(t, mr.Exists("test:aicc:circuit:hermes:probe"))
}

// TestRedisAICCUpstreamCircuitReopenReleasesProbe 验证半开任务失败会重新冷却且清除旧探测令牌。
func TestRedisAICCUpstreamCircuitReopenReleasesProbe(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	circuit := NewRedisAICCUpstreamCircuit(client, "test:", 1, time.Second, 100, 100*time.Millisecond)
	require.NoError(t, circuit.RecordOverload(context.Background(), "hermes"))
	time.Sleep(120 * time.Millisecond)
	allowed, err := circuit.Allow(context.Background(), "hermes")
	require.NoError(t, err)
	require.True(t, allowed)
	require.NoError(t, circuit.Reopen(context.Background(), "hermes"))
	assert.False(t, mr.Exists("test:aicc:circuit:hermes:probe"))
	allowed, err = circuit.Allow(context.Background(), "hermes")
	require.NoError(t, err)
	assert.False(t, allowed)
}

// TestRedisAICCUpstreamCircuitUsesMixedOutcomeRate 验证失败率以全部上游结果为分母，单次失败不会提前熔断。
func TestRedisAICCUpstreamCircuitUsesMixedOutcomeRate(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	circuit := NewRedisAICCUpstreamCircuit(client, "test:", 5, time.Second, 30, time.Second)
	for i := 0; i < 9; i++ {
		require.NoError(t, circuit.RecordSuccess(context.Background(), "hermes"))
	}
	require.NoError(t, circuit.RecordOverload(context.Background(), "hermes"))
	allowed, err := circuit.Allow(context.Background(), "hermes")
	require.NoError(t, err)
	assert.True(t, allowed)
	for i := 0; i < 7; i++ {
		require.NoError(t, circuit.RecordSuccess(context.Background(), "other"))
	}
	for i := 0; i < 3; i++ {
		require.NoError(t, circuit.RecordOverload(context.Background(), "other"))
	}
	allowed, err = circuit.Allow(context.Background(), "other")
	require.NoError(t, err)
	assert.False(t, allowed)
}
