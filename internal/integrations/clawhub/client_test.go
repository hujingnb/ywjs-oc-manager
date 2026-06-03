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
		_, _ = w.Write([]byte(`{"skills":[{"slug":"weather","name":"weather","description":"天气","version":"1.2","downloads":100}],"next_cursor":"c2"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 0)
	res, err := c.Search(context.Background(), "weather", "")
	require.NoError(t, err)
	require.Len(t, res.Skills, 1)
	// 验证 slug 与游标正确解析
	assert.Equal(t, "weather", res.Skills[0].Slug)
	assert.Equal(t, "c2", res.NextCursor)
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
