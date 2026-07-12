package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVisitorRequestContextSurvivesScheduleDeadline 覆盖压测收尾：已开始的访客请求不应被总时限取消。
func TestVisitorRequestContextSurvivesScheduleDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	assert.NoError(t, visitorRequestContext(ctx).Err())
}

// TestForwardedIPForVisitor 覆盖独立访客限流隔离：不同访客不能复用同一来源 IP。
func TestForwardedIPForVisitor(t *testing.T) {
	first := forwardedIPForVisitor(7, "visitor-a")
	second := forwardedIPForVisitor(7, "visitor-b")

	assert.NotEqual(t, first, second)
	assert.Equal(t, first, forwardedIPForVisitor(7, "visitor-a"))
}

// TestClientMessageIDForVisitor 覆盖压测消息幂等键：同一访客重试必须复用 UUIDv4，不同访客不能碰撞。
func TestClientMessageIDForVisitor(t *testing.T) {
	first := clientMessageIDForVisitor("visitor-a")
	second := clientMessageIDForVisitor("visitor-b")

	assert.Equal(t, first, clientMessageIDForVisitor("visitor-a"))
	assert.NotEqual(t, first, second)
	assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, first)
}

// TestLoadMessageForVisitor 覆盖容量场景文本：使用明确客服问候，避免随机串被智能体误判为工具任务。
func TestLoadMessageForVisitor(t *testing.T) {
	message := loadMessageForVisitor("visitor-a")

	assert.Equal(t, "你好，请只回复收到。访客标识：visitor-a", message)
	assert.NotEqual(t, message, loadMessageForVisitor("visitor-b"))
}

// TestNewLoadHTTPClientBypassesProxyForLocalOCM 覆盖本地压测：ocm.localhost 不得经过宿主机代理。
func TestNewLoadHTTPClientBypassesProxyForLocalOCM(t *testing.T) {
	client := newLoadHTTPClient(Config{BaseURL: "http://ocm.localhost", Timeout: defaultTimeout})
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Nil(t, transport.Proxy)
}

// TestQuantile 覆盖空样本、奇数样本和非整分位数的延迟统计边界。
func TestQuantile(t *testing.T) {
	tests := []struct {
		name       string
		samples    []int64
		percentile float64
		want       int64
	}{
		// 空样本时没有可用延迟，必须返回零值。
		{name: "空样本", samples: nil, percentile: 0.95, want: 0},
		// 中位数应选取排序后的中间样本。
		{name: "中位数", samples: []int64{30, 10, 20}, percentile: 0.50, want: 20},
		// P95 使用最近秩，避免低估接近尾部的请求延迟。
		{name: "P95 最近秩", samples: []int64{10, 20, 30, 40, 50}, percentile: 0.95, want: 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 验证负载报告使用稳定且可解释的延迟分位数算法。
			assert.Equal(t, tt.want, quantile(tt.samples, tt.percentile))
		})
	}
}

// TestSuccessRate 覆盖全成功、部分失败和零请求时的成功率定义。
func TestSuccessRate(t *testing.T) {
	tests := []struct {
		name    string
		total   int64
		success int64
		want    float64
	}{
		// 没有产生请求时成功率为零，不能误报为百分之百。
		{name: "零请求", total: 0, success: 0, want: 0},
		// 全部成功时成功率应为百分之百。
		{name: "全部成功", total: 8, success: 8, want: 100},
		// 部分失败时应输出精确百分比。
		{name: "部分失败", total: 8, success: 7, want: 87.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 验证成功率以全部已完成请求作为分母。
			assert.Equal(t, tt.want, successRate(tt.total, tt.success))
		})
	}
}

// TestValidateSessionResponse 覆盖公开接口返回的会话 token 隔离校验。
func TestValidateSessionResponse(t *testing.T) {
	tests := []struct {
		name          string
		expectedToken string
		responseToken string
		wantMismatch  bool
	}{
		// 服务端返回当前访客 token 时，不能计为串写。
		{name: "同一会话", expectedToken: "visitor-a", responseToken: "visitor-a", wantMismatch: false},
		// 服务端返回其他访客 token 时，必须报告 session 串写。
		{name: "其他会话", expectedToken: "visitor-a", responseToken: "visitor-b", wantMismatch: true},
		// 缺失 token 无法证明会话归属，也必须报告异常。
		{name: "缺失会话", expectedToken: "visitor-a", responseToken: "", wantMismatch: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 验证每个虚拟访客只接受自己创建的 session token。
			assert.Equal(t, tt.wantMismatch, validateSessionResponse(tt.expectedToken, tt.responseToken))
		})
	}
}
