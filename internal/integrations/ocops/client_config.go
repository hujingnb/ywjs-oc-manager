// client_config.go — ocops 实例运行配置查询。
//
// 提供 Config 方法，实时查询实例当前运行的 display.language，
// 供 manager 在需要时（如 locale 传播）从实例侧获取真实配置，
// 不依赖 manager 本地 DB 快照。
package ocops

import (
	"context"
	"net/http"
)

// OcConfig 是 /oc/config 的响应：实例当前运行配置中 manager 关心的字段。
type OcConfig struct {
	// DisplayLanguage 对应实例 display.language 配置项，取值 "zh" 或 "en"。
	DisplayLanguage string `json:"display_language"`
}

// Config 查询实例当前运行的 display.language（实时，不依赖 manager DB 快照）。
// GET /oc/config
func (c *Client) Config(ctx context.Context, ep Endpoint) (OcConfig, error) {
	var out OcConfig
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/config", nil, &out)
	return out, err
}
