package agent

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCachedClient 是缓存单测用的占位 client，用指针身份判断是否复用同一实例。
type fakeCachedClient struct{ id int }

// TestClientCacheReusesSameFingerprint 覆盖「指纹不变」场景：
// 同一 key 第二次取应返回同一实例、build 只构造一次、不触发 closeFn。
func TestClientCacheReusesSameFingerprint(t *testing.T) {
	var built int                  // 记录 build 被调用次数
	var closed []*fakeCachedClient // 记录被回收的旧实例
	cache := NewClientCache(func(c *fakeCachedClient) { closed = append(closed, c) })
	build := func() (*fakeCachedClient, error) { built++; return &fakeCachedClient{id: built}, nil }

	c1, err := cache.Get("node-1", "fp-a", build)
	require.NoError(t, err)
	c2, err := cache.Get("node-1", "fp-a", build) // 同 key 同指纹再次获取
	require.NoError(t, err)

	assert.Same(t, c1, c2)    // 同指纹复用同一实例
	assert.Equal(t, 1, built) // 只构造一次
	assert.Empty(t, closed)   // 未触发回收
}

// TestClientCacheRebuildsOnFingerprintChange 覆盖「配置变更（指纹变化）」场景：
// 应重建新实例、构造两次、并把旧实例交给 closeFn 回收。
func TestClientCacheRebuildsOnFingerprintChange(t *testing.T) {
	var built int
	var closed []*fakeCachedClient
	cache := NewClientCache(func(c *fakeCachedClient) { closed = append(closed, c) })
	build := func() (*fakeCachedClient, error) { built++; return &fakeCachedClient{id: built}, nil }

	c1, err := cache.Get("node-1", "fp-a", build)
	require.NoError(t, err)
	c2, err := cache.Get("node-1", "fp-b", build) // 指纹变化，模拟 token/endpoint/CA 变更
	require.NoError(t, err)

	assert.NotSame(t, c1, c2)     // 指纹变化重建新实例
	assert.Equal(t, 2, built)     // 构造两次
	require.Len(t, closed, 1)     // 旧实例被回收一次
	assert.Same(t, c1, closed[0]) // 回收的正是旧实例
}

// TestClientCacheBuildErrorNotCached 覆盖「构造失败」场景：
// 错误应原样透传、且失败结果不写入缓存，后续以相同 key/指纹重试可成功构造。
func TestClientCacheBuildErrorNotCached(t *testing.T) {
	cache := NewClientCache(func(c *fakeCachedClient) {})

	_, err := cache.Get("node-1", "fp-a", func() (*fakeCachedClient, error) {
		return nil, errors.New("boom") // 首次构造失败
	})
	require.Error(t, err) // 错误透传

	var built int
	c, err := cache.Get("node-1", "fp-a", func() (*fakeCachedClient, error) {
		built++
		return &fakeCachedClient{id: built}, nil
	})
	require.NoError(t, err)
	assert.NotNil(t, c)       // 失败未污染缓存，重试可成功
	assert.Equal(t, 1, built) // 第二次确实重新构造
}

// TestClientCacheIsolatesKeys 覆盖「不同 key 互不影响」场景：
// 不同 nodeID 各自缓存独立实例，不应相互覆盖或串用。
func TestClientCacheIsolatesKeys(t *testing.T) {
	var built int
	cache := NewClientCache(func(c *fakeCachedClient) {})
	build := func() (*fakeCachedClient, error) { built++; return &fakeCachedClient{id: built}, nil }

	a, err := cache.Get("node-a", "fp", build)
	require.NoError(t, err)
	b, err := cache.Get("node-b", "fp", build) // 不同 key，即便指纹相同也应各自构造
	require.NoError(t, err)

	assert.NotSame(t, a, b)   // 不同 key 是不同实例
	assert.Equal(t, 2, built) // 各构造一次
}

// TestFingerprint 覆盖指纹的稳定性与区分度：
// 相同输入稳定一致、任一字段不同则指纹不同、且分隔避免拼接歧义。
func TestFingerprint(t *testing.T) {
	assert.Equal(t, Fingerprint("a", "b"), Fingerprint("a", "b"))    // 相同输入指纹稳定
	assert.NotEqual(t, Fingerprint("a", "b"), Fingerprint("a", "c")) // token 不同则指纹不同
	assert.NotEqual(t, Fingerprint("ab", ""), Fingerprint("a", "b")) // \x00 分隔避免 "ab" 与 "a"+"b" 撞车
}
