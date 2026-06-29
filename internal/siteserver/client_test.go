package siteserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHTTPClientParsesSites 覆盖：客户端带鉴权 header 调端点，正确解析 sites 数组。
func TestHTTPClientParsesSites(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "secret-token", r.Header.Get("X-OC-Site-Sync-Token"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sites":[{"host":"blog.example.com","site_id":"s1","s3_prefix":"published-sites/s1/v1/","status":"active"}]}`))
	}))
	defer srv.Close()

	c := NewHTTPSiteListClient(srv.URL, "secret-token")
	sites, err := c.ListActiveSites(context.Background())
	require.NoError(t, err)
	require.Len(t, sites, 1)
	assert.Equal(t, "blog.example.com", sites[0].Host)
	assert.Equal(t, "published-sites/s1/v1/", sites[0].S3Prefix)
}

// TestHTTPClientNon200 覆盖：端点非 200（如 manager 未就绪）返回错误，syncer 据此保留旧快照。
func TestHTTPClientNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := NewHTTPSiteListClient(srv.URL, "t")
	_, err := c.ListActiveSites(context.Background())
	require.Error(t, err)
}
