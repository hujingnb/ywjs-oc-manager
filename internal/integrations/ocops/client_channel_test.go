// client_channel_test.go — info/doctor/channel-status/unbind 4 个客户端方法的 httptest 单测。
//
// 每个测试用 httptest.Server 断言方法发出的 HTTP method / path 与契约一致，
// 并验证响应正确解码；另含一例错误码映射（404 → ocops.ErrNotFound）。
package ocops_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/ocops"
)

// TestInfo 验证 Info 发出 GET /oc/info 并把响应解码为 ocops.Info。
func TestInfo(t *testing.T) {
	// 正常路径：server 返回镜像身份，断言关键字段解码正确
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/info", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"variant":"hermes-v2026.5.16","hermes_upstream_ref":"v2026.5.16","oc_entrypoint_version":"1.2.3","built_at":"2026-05-29T00:00:00Z"}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	info, err := c.Info(context.Background(), ep)
	require.NoError(t, err)
	assert.Equal(t, "hermes-v2026.5.16", info.Variant)
	assert.Equal(t, "v2026.5.16", info.HermesUpstreamRef)
	assert.Equal(t, "1.2.3", info.OCEntrypointVersion)
}

// TestDoctor 验证 Doctor 发出 GET /oc/doctor 并解码健康自检结果（含 issues 切片）。
func TestDoctor(t *testing.T) {
	// 正常路径：server 返回自检结果，断言状态与 issues 列表解码正确
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/doctor", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"variant":"hermes-v2026.5.16","last_render_at":"2026-05-29T01:00:00Z","manifest_sha256":"abc","hermes_pid":42,"hermes_status":"running","issues":["w1","w2"]}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	doc, err := c.Doctor(context.Background(), ep)
	require.NoError(t, err)
	assert.Equal(t, 42, doc.HermesPID)
	assert.Equal(t, "running", doc.HermesStatus)
	assert.Equal(t, []string{"w1", "w2"}, doc.Issues)
}

// TestChannelStatus 验证 ChannelStatus 发出 GET /oc/channels/{channel}/status
// 并对 channel 名做 path 转义，响应解码为 ocops.ChannelStatus。
func TestChannelStatus(t *testing.T) {
	// 正常路径：channel 名含特殊字符需 PathEscape，断言 server 收到转义后的 path
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		// r.URL.Path 已被 server 解码回原始 channel 名
		assert.Equal(t, "/oc/channels/weixin/status", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"channel":"weixin","bound":true,"account_id":"acc-1"}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	st, err := c.ChannelStatus(context.Background(), ep, "weixin")
	require.NoError(t, err)
	assert.Equal(t, "weixin", st.Channel)
	assert.True(t, st.Bound)
	assert.Equal(t, "acc-1", st.AccountID)
}

// TestChannelUnbind 验证 ChannelUnbind 发出 POST /oc/channels/{channel}/unbind 并解码结果。
func TestChannelUnbind(t *testing.T) {
	// 正常路径：解绑成功，断言 method/path 与 status 字段解码
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/channels/weixin/unbind", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	res, err := c.ChannelUnbind(context.Background(), ep, "weixin")
	require.NoError(t, err)
	assert.Equal(t, "ok", res.Status)
}

// TestChannelStatusNotFound 验证非 2xx（404）经 statusToErr 映射为 ocops.ErrNotFound。
func TestChannelStatusNotFound(t *testing.T) {
	// 错误路径：server 返回 404 + 契约错误体，客户端应返回 ErrNotFound 哨兵错误
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"not_found","message":"channel not found"}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	_, err := c.ChannelStatus(context.Background(), ep, "weixin")
	require.ErrorIs(t, err, ocops.ErrNotFound)
}
