// client_feishu_test.go — 飞书渠道客户端方法（注册 SSE）的单元测试。
package ocops_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/ocops"
)

// TestFeishuRegisterParsesEvents 验证 SSE 客户端把 qrcode/credentials 事件解析为事件流。
// 业务场景：oc-ops /oc/channels/feishu/register 扫码注册先推 qrcode（二维码 URL），
// 扫码授权成功后推 credentials（app_id/app_secret 等凭证），客户端须逐条解析投递。
func TestFeishuRegisterParsesEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// 先推二维码事件，再推凭证事件，模拟扫码授权完成的完整链路
		fmt.Fprint(w, "data: {\"event\":\"qrcode\",\"url\":\"https://open.feishu.cn/qr/x\"}\n\n")
		fmt.Fprint(w, "data: {\"event\":\"credentials\",\"app_id\":\"cli_x\",\"app_secret\":\"sec\",\"domain\":\"feishu\",\"bot_name\":\"Bot\"}\n\n")
		w.(http.Flusher).Flush()
	}))
	defer srv.Close()

	c := ocops.NewClient(srv.Client())
	ep := ocops.Endpoint{BaseURL: srv.URL, Token: "t"}
	events, err := c.FeishuRegister(context.Background(), ep, "feishu")
	require.NoError(t, err)

	// 读尽 channel，按到达顺序收集事件
	var got []ocops.FeishuRegisterEvent
	for ev := range events {
		got = append(got, ev)
	}

	require.Len(t, got, 2)
	// 第一条：qrcode 事件携带二维码 URL
	assert.Equal(t, "qrcode", got[0].Event)
	assert.Equal(t, "https://open.feishu.cn/qr/x", got[0].URL)
	// 第二条：credentials 事件携带应用凭证
	assert.Equal(t, "credentials", got[1].Event)
	assert.Equal(t, "cli_x", got[1].AppID)
	assert.Equal(t, "sec", got[1].AppSecret)
	assert.Equal(t, "feishu", got[1].Domain)
	assert.Equal(t, "Bot", got[1].BotName)
}

// TestFeishuRegisterNon2xxReturnsError 验证非 2xx 响应不建流、直接返回错误。
// 异常路径：oc-ops 不可用或鉴权失败时，客户端不应返回可用 channel。
func TestFeishuRegisterNon2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 401 表示鉴权失败，客户端应映射为哨兵错误且 channel 为 nil
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := ocops.NewClient(srv.Client())
	ep := ocops.Endpoint{BaseURL: srv.URL, Token: "t"}
	events, err := c.FeishuRegister(context.Background(), ep, "feishu")
	require.Error(t, err)
	assert.Nil(t, events)
}
