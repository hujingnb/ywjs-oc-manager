package clawhub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Search 调 /api/v1/search 并解析 skills 列表与游标。
func TestClawHubClient_Search(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 验证请求路径和查询参数符合 ClawHub API 约定
		assert.Equal(t, "/api/v1/search", r.URL.Path)
		assert.Equal(t, "weather", r.URL.Query().Get("q"))
		w.Header().Set("Content-Type", "application/json")
		// clawhubcn 真实 schema：items + displayName/summary/tags.latest/stats.downloads + nextCursor。
		_, _ = w.Write([]byte(`{"items":[{"slug":"weather","displayName":"Weather","summary":"天气","tags":{"latest":"1.2"},"stats":{"downloads":100}}],"nextCursor":"c2"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 0)
	res, err := c.Search(context.Background(), "weather", "")
	require.NoError(t, err)
	require.Len(t, res.Skills, 1)
	// 验证 clawhubcn 字段映射到扁平 Skill：displayName→Name、summary→Description、tags.latest→Version、stats.downloads→Downloads。
	assert.Equal(t, "weather", res.Skills[0].Slug)
	assert.Equal(t, "Weather", res.Skills[0].Name)
	assert.Equal(t, "天气", res.Skills[0].Description)
	assert.Equal(t, "1.2", res.Skills[0].Version)
	assert.Equal(t, int64(100), res.Skills[0].Downloads)
	assert.Equal(t, "c2", res.NextCursor)
}

// GetSkill 解包 clawhubcn 详情 {skill,latestVersion} 并映射字段。
func TestClawHubClient_GetSkill(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/skills/weather", r.URL.Path)
		_, _ = w.Write([]byte(`{"skill":{"slug":"weather","displayName":"Weather","summary":"天气","tags":{"latest":"2.0"},"stats":{"downloads":50}},"latestVersion":{"version":"2.0"}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 0)
	sk, err := c.GetSkill(context.Background(), "weather")
	require.NoError(t, err)
	assert.Equal(t, "weather", sk.Slug)
	assert.Equal(t, "Weather", sk.Name)
	assert.Equal(t, "2.0", sk.Version)
}

// ListVersions 解包 clawhubcn 的 {items:[{version}]} 为版本数组。
func TestClawHubClient_ListVersions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/skills/weather/versions", r.URL.Path)
		_, _ = w.Write([]byte(`{"items":[{"version":"2.0"},{"version":"1.0"}],"nextCursor":null}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 0)
	vs, err := c.ListVersions(context.Background(), "weather")
	require.NoError(t, err)
	require.Len(t, vs, 2)
	assert.Equal(t, "2.0", vs[0].Version)
}

// Download 调 /api/v1/download 返回归档原始字节与扩展名 zip。
func TestClawHubClient_Download(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 验证请求路径与参数，确保 slug 和 version 正确传入
		assert.Equal(t, "/api/v1/download", r.URL.Path)
		assert.Equal(t, "weather", r.URL.Query().Get("slug"))
		assert.Equal(t, "1.2", r.URL.Query().Get("version"))
		_, _ = w.Write([]byte("PK\x03\x04zip-bytes"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 0)
	data, err := c.Download(context.Background(), "weather", "1.2")
	require.NoError(t, err)
	// 验证下载到的字节与服务端写出的完全一致
	assert.Equal(t, []byte("PK\x03\x04zip-bytes"), data)
}
