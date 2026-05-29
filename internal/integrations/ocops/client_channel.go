// client_channel.go — ocops 包 info / doctor / channel 的非流式类型化客户端方法。
//
// 这 4 个方法补全 Phase 7 未单列的 info/doctor/channel-status/unbind 客户端能力，
// 供 service 侧 channelOps 接口与 Task 21 重构消费。内部统一调 c.DoJSON，
// channel 名走 path 时用 url.PathEscape 转义，防止含特殊字符时路径越界。
// channel 登录（SSE 流式）见 client_sse.go 的 ChannelLogin。
package ocops

import (
	"context"
	"net/http"
	"net/url"
)

// Info 查询实例镜像身份信息。
// GET /oc/info
func (c *Client) Info(ctx context.Context, ep Endpoint) (Info, error) {
	var out Info
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/info", nil, &out)
	return out, err
}

// Doctor 触发实例健康自检并返回结果。
// GET /oc/doctor
func (c *Client) Doctor(ctx context.Context, ep Endpoint) (Doctor, error) {
	var out Doctor
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/doctor", nil, &out)
	return out, err
}

// ChannelStatus 查询指定渠道的绑定状态。
// GET /oc/channels/{channel}/status
func (c *Client) ChannelStatus(ctx context.Context, ep Endpoint, channel string) (ChannelStatus, error) {
	var out ChannelStatus
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/channels/"+url.PathEscape(channel)+"/status", nil, &out)
	return out, err
}

// ChannelUnbind 解绑指定渠道，返回操作结果。
// POST /oc/channels/{channel}/unbind
func (c *Client) ChannelUnbind(ctx context.Context, ep Endpoint, channel string) (ChannelResult, error) {
	var out ChannelResult
	err := c.DoJSON(ctx, ep, http.MethodPost, "/oc/channels/"+url.PathEscape(channel)+"/unbind", nil, &out)
	return out, err
}
