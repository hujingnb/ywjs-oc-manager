// types_channel.go — ocops 包 channel / info / doctor 端点的 DTO。
//
// 这些类型镜像 internal/integrations/hermes/commands.go 同名结构体（字段与
// JSON tag 一致，连注释一并搬来），并由 ocops 包作为契约属主：oc-* 全面改走
// oc-ops HTTP 后，hermes 包对应类型将退化为薄封装或迁走（见 Task 21）。
package ocops

// Info 是 GET /oc/info 的响应体，描述运行实例的镜像身份。
// 字段镜像 hermes.Info（原 oc-info 命令的 stdout JSON）。
type Info struct {
	Variant             string `json:"variant"`
	HermesUpstreamRef   string `json:"hermes_upstream_ref"`
	OCEntrypointVersion string `json:"oc_entrypoint_version"`
	BuiltAt             string `json:"built_at"`
}

// Doctor 是 GET /oc/doctor 的响应体，描述实例健康自检结果。
// 字段镜像 hermes.Doctor（原 oc-doctor 命令的 stdout JSON）。
type Doctor struct {
	Variant        string   `json:"variant"`
	LastRenderAt   string   `json:"last_render_at"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	HermesPID      int      `json:"hermes_pid"`
	HermesStatus   string   `json:"hermes_status"`
	Issues         []string `json:"issues"`
}

// ChannelStatus 是 GET /oc/channels/{channel}/status 的响应体。
// 字段镜像 hermes.ChannelStatus（原 oc-channel-status 命令的 stdout JSON）。
type ChannelStatus struct {
	Channel   string `json:"channel"`
	Bound     bool   `json:"bound"`
	AccountID string `json:"account_id,omitempty"`
}

// ChannelResult 是 POST /oc/channels/{channel}/unbind 的响应体。
// 字段镜像 hermes.ChannelResult（原 oc-channel-login / oc-channel-unbind 的 stdout JSON）。
type ChannelResult struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}
