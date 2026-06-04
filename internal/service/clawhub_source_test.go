package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/clawhub"
)

// fakeClawHubAPI 是 ClawHubSearcher 的内存实现，记录调用次数以验证缓存命中效果。
// calls 字段在每次 Search 调用时自增，测试通过断言其值来确认是否发生了回源。
type fakeClawHubAPI struct {
	// result 是预设的搜索结果，每次调用都返回同样的数据。
	result clawhub.SearchResult
	// calls 记录 Search 被调用的总次数（验证缓存命中时回源次数应不变）。
	calls int
	// versions 是 ListVersions 的预设返回值。
	versions []clawhub.SkillVersion
	// detail 是 GetSkill 的预设返回值。
	detail clawhub.SkillDetail
	// archive 是 Download 的预设归档字节。
	archive []byte
}

// Search 实现 ClawHubSearcher 接口：每次调用将 calls 加一并返回预设结果。
func (f *fakeClawHubAPI) Search(_ context.Context, _, _ string) (clawhub.SearchResult, error) {
	f.calls++
	return f.result, nil
}

// GetSkill 实现 ClawHubSearcher 接口：返回预设的富详情。
func (f *fakeClawHubAPI) GetSkill(_ context.Context, _ string) (clawhub.SkillDetail, error) {
	return f.detail, nil
}

// ListVersions 实现 ClawHubSearcher 接口：返回预设的版本列表。
func (f *fakeClawHubAPI) ListVersions(_ context.Context, _ string) ([]clawhub.SkillVersion, error) {
	return f.versions, nil
}

// Download 实现 ClawHubSearcher 接口：返回预设的归档字节（默认空 zip 占位）。
func (f *fakeClawHubAPI) Download(_ context.Context, _, _ string) ([]byte, error) {
	return f.archive, nil
}

// fakeRedis 是 RedisCache 的内存实现，用 map 模拟 Redis GET/SET 语义。
// 满足 ClawHubSource 所需最小缓存接口，无需依赖真实 Redis 连接。
type fakeRedis struct {
	mu    sync.Mutex
	store map[string]string
}

// newFakeRedis 返回一个空的内存 Redis 替身。
func newFakeRedis() *fakeRedis {
	return &fakeRedis{store: make(map[string]string)}
}

// Get 实现 RedisCache.Get：key 存在时返回值，不存在时返回 redis.Nil 错误。
// 使用 redis.NewStringResult 构造返回值，与真实 *redis.Client 行为一致。
func (f *fakeRedis) Get(_ context.Context, key string) *redis.StringCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	val, ok := f.store[key]
	if !ok {
		// redis.Nil 是 go-redis 约定的"键不存在"哨兵错误，ClawHubSource 通过 errors.Is 判断。
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(val, nil)
}

// Set 实现 RedisCache.Set：将 value 序列化为字符串写入 map，忽略 TTL（内存无过期语义）。
// ttl 参数在 fake 中不做实际处理，仅保证接口兼容；测试关注的是数据是否写入，而非过期行为。
// 使用 redis.NewStatusResult 构造返回值，与真实 *redis.Client 行为一致。
func (f *fakeRedis) Set(_ context.Context, key string, value any, _ time.Duration) *redis.StatusCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	// value 可能是 []byte 或 string，统一转 string 存储（与 go-redis 实际行为一致）。
	switch v := value.(type) {
	case []byte:
		f.store[key] = string(v)
	case string:
		f.store[key] = v
	default:
		// 其它类型尝试 JSON 序列化后存储，确保通用性。
		if b, err := json.Marshal(v); err == nil {
			f.store[key] = string(b)
		}
	}
	return redis.NewStatusResult("OK", nil)
}

// TestClawHubSource_SearchCaches 验证搜索的两阶段缓存行为：
// ① 首次搜索：缓存未命中，回源 ClawHub API，写缓存，返回正确 SkillPage；
// ② 第二次相同参数搜索：命中缓存，不再调用 API（api.calls 不变），结果与首次一致。
func TestClawHubSource_SearchCaches(t *testing.T) {
	// 准备 fake API，预设一条搜索结果。
	api := &fakeClawHubAPI{result: clawhub.SearchResult{
		Skills: []clawhub.Skill{
			// 验证字段：Slug→SourceRef、Downloads→Downloads、Version→Version。
			{Slug: "weather", Name: "weather", Description: "天气查询", Version: "1.2", Downloads: 9},
		},
		NextCursor: "n1",
	}}
	rdb := newFakeRedis()
	src := NewClawHubSource(api, rdb, time.Minute)

	// 第一次搜索：缓存未命中，应回源 API，api.calls 从 0 变为 1。
	p1, err := src.Search(context.Background(), auth.Principal{}, "weather", "")
	require.NoError(t, err)
	// 验证返回条目数量与内容正确。
	require.Len(t, p1.Entries, 1)
	// source 字段应为 "clawhub"（来源标识）。
	assert.Equal(t, "clawhub", p1.Entries[0].Source)
	// source_ref 字段应等于 ClawHub 的 slug（回源标识）。
	assert.Equal(t, "weather", p1.Entries[0].SourceRef)
	// Downloads 应透传 ClawHub 返回值（9）。
	assert.EqualValues(t, 9, p1.Entries[0].Downloads)
	// NextCursor 应透传 ClawHub 返回的游标。
	assert.Equal(t, "n1", p1.NextCursor)
	// 第一次应调用了一次 API（回源）。
	assert.Equal(t, 1, api.calls)

	// 第二次搜索：相同参数（q="weather", cursor=""），应命中缓存，不再调用 API。
	p2, err := src.Search(context.Background(), auth.Principal{}, "weather", "")
	require.NoError(t, err)
	// 结果应与第一次完全一致（从缓存反序列化）。
	assert.Equal(t, p1, p2)
	// 关键断言：api.calls 仍为 1，说明第二次未发生回源，缓存命中生效。
	assert.Equal(t, 1, api.calls, "第二次搜索应命中缓存，api 调用次数不应增加")
}

// TestClawHubSource_DifferentQueryNotSharedCache 验证不同搜索参数使用独立缓存 key，
// 不同 q 的请求不共享缓存，各自独立回源。
func TestClawHubSource_DifferentQueryNotSharedCache(t *testing.T) {
	// 两次不同 q 的搜索应各自触发回源，api.calls 应为 2。
	api := &fakeClawHubAPI{result: clawhub.SearchResult{
		Skills: []clawhub.Skill{{Slug: "s", Name: "s", Version: "1.0"}},
	}}
	rdb := newFakeRedis()
	src := NewClawHubSource(api, rdb, time.Minute)

	// q="foo" 第一次，缓存未命中，回源。
	_, err := src.Search(context.Background(), auth.Principal{}, "foo", "")
	require.NoError(t, err)
	assert.Equal(t, 1, api.calls)

	// q="bar" 不同 q，缓存 key 不同，应再次回源。
	_, err = src.Search(context.Background(), auth.Principal{}, "bar", "")
	require.NoError(t, err)
	// 不同 q 独立缓存，各回源一次，共 2 次。
	assert.Equal(t, 2, api.calls, "不同 q 应各自独立回源，不共享缓存")

	// q="foo" 再次搜索，已有缓存，不再回源。
	_, err = src.Search(context.Background(), auth.Principal{}, "foo", "")
	require.NoError(t, err)
	// 第三次 q="foo" 命中缓存，总调用次数仍为 2。
	assert.Equal(t, 2, api.calls, "q=foo 第二次应命中缓存，总 api 调用次数不变")
}

// TestClawHubSource_Download 验证公共来源下载：回源 ClawHub 取归档字节，ext=zip。
func TestClawHubSource_Download(t *testing.T) {
	// 预设 fake API 的下载归档字节。
	api := &fakeClawHubAPI{archive: []byte("ZIP-ARCHIVE-BYTES")}
	src := NewClawHubSource(api, newFakeRedis(), time.Minute)

	got, ext, err := src.Download(context.Background(), "self-improving-agent", "3.0.21")
	require.NoError(t, err)
	assert.Equal(t, []byte("ZIP-ARCHIVE-BYTES"), got)
	// ClawHub 归档格式为 zip。
	assert.Equal(t, "zip", ext)
}
