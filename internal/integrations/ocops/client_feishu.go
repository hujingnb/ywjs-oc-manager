// client_feishu.go — ocops 包飞书渠道的客户端方法（扫码注册 SSE）。
//
// 飞书渠道经扫码自动创建获取凭证：FeishuRegister 订阅 oc-ops 的 register SSE，
// 逐条拿到 qrcode（二维码 URL）与 credentials（回填的 app_id/app_secret 等）。
//
// 健康态状态查询复用 client_channel.go 的 ChannelStatus(ctx, ep, "feishu")，
// 其返回的 ChannelStatus 已含 PlatformState / ErrorMessage 供 worker 探测。
//
// SSE 消费复用 client_sse.go 的 openStream + scanSSE，与 ChannelLogin 保持同一模式。
package ocops

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// FeishuRegister 发起 oc-ops 飞书扫码注册 SSE：POST /oc/channels/feishu/register?domain=<domain>。
//
// 返回只读 channel，逐条投递 FeishuRegisterEvent（qrcode/credentials/failed）；
// 流正常结束、`event: error` 帧或 ctx 取消时关闭 channel。非 2xx 直接返回哨兵错误
// （不建流、channel 为 nil）。无法解析的帧静默跳过（容忍心跳 / 注释行）。
func (c *Client) FeishuRegister(ctx context.Context, ep Endpoint, domain string) (<-chan FeishuRegisterEvent, error) {
	// domain 走 query 传递；用 QueryEscape 防止含特殊字符时污染 URL
	resp, err := c.openStream(ctx, ep, http.MethodPost, "/oc/channels/feishu/register?domain="+url.QueryEscape(domain))
	if err != nil {
		return nil, err
	}

	ch := make(chan FeishuRegisterEvent, sseChanBuffer)
	go func() {
		// 统一在 goroutine 退出时关闭 channel 与响应体，避免连接泄漏、消费方能感知流结束
		defer close(ch)
		defer resp.Body.Close()

		scanSSE(ctx, resp.Body, func(data []byte) bool {
			var ev FeishuRegisterEvent
			if err := json.Unmarshal(data, &ev); err != nil {
				return true // 跳过无法解析的帧，继续读流
			}
			// 投递时尊重 ctx 取消，避免消费方退出后 goroutine 卡在写 channel
			select {
			case ch <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()
	return ch, nil
}
