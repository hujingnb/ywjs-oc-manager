package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// ContainerExecer 抽象 manager 端通过节点 agent docker proxy 反向代理执行
// 容器命令的能力，实现由 internal/integrations/runtime.AgentBackedAdapter
// （ContainerExec）提供。
type ContainerExecer interface {
	ContainerExec(ctx context.Context, nodeID, containerID string, cmd []string) (stdout, stderr io.Reader, err error)
}

// Info 是 oc-info 命令的 stdout JSON 解码结果。
type Info struct {
	Variant             string `json:"variant"`
	HermesUpstreamRef   string `json:"hermes_upstream_ref"`
	OCEntrypointVersion string `json:"oc_entrypoint_version"`
	BuiltAt             string `json:"built_at"`
}

// Doctor 是 oc-doctor 命令的 stdout JSON 解码结果。
type Doctor struct {
	Variant        string   `json:"variant"`
	LastRenderAt   string   `json:"last_render_at"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	HermesPID      int      `json:"hermes_pid"`
	HermesStatus   string   `json:"hermes_status"`
	Issues         []string `json:"issues"`
}

// ChannelStatus 是 oc-channel-status 命令的 stdout JSON 解码结果。
type ChannelStatus struct {
	Channel   string `json:"channel"`
	Bound     bool   `json:"bound"`
	AccountID string `json:"account_id,omitempty"`
}

// ChannelResult 是 oc-channel-login / oc-channel-unbind 的 stdout JSON 形态。
type ChannelResult struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// RunInfo 调用容器内 oc-info，解析镜像身份。
func RunInfo(ctx context.Context, exec ContainerExecer, nodeID, containerID string) (Info, error) {
	var info Info
	err := runJSONCmd(ctx, exec, nodeID, containerID, []string{"oc-info"}, &info)
	return info, err
}

// RunDoctor 调用容器内 oc-doctor。
func RunDoctor(ctx context.Context, exec ContainerExecer, nodeID, containerID string) (Doctor, error) {
	var d Doctor
	err := runJSONCmd(ctx, exec, nodeID, containerID, []string{"oc-doctor"}, &d)
	return d, err
}

// RunChannelStatus 调用容器内 oc-channel-status。
func RunChannelStatus(ctx context.Context, exec ContainerExecer, nodeID, containerID, channel string) (ChannelStatus, error) {
	var s ChannelStatus
	err := runJSONCmd(ctx, exec, nodeID, containerID, []string{"oc-channel-status", "--channel", channel}, &s)
	return s, err
}

// RunChannelLogin 调用容器内 oc-channel-login。
// 中间事件（含二维码 URL）由 stderr 上报，目前不在此函数透传给调用方；
// 后续若需要可加 stderrSink io.Writer 参数。
func RunChannelLogin(ctx context.Context, exec ContainerExecer, nodeID, containerID, channel string) (ChannelResult, error) {
	var r ChannelResult
	err := runJSONCmd(ctx, exec, nodeID, containerID, []string{"oc-channel-login", "--channel", channel}, &r)
	return r, err
}

// RunChannelUnbind 调用容器内 oc-channel-unbind。
func RunChannelUnbind(ctx context.Context, exec ContainerExecer, nodeID, containerID, channel string) (ChannelResult, error) {
	var r ChannelResult
	err := runJSONCmd(ctx, exec, nodeID, containerID, []string{"oc-channel-unbind", "--channel", channel}, &r)
	return r, err
}

// runJSONCmd 封装：执行命令、读尽 stdout、按行 JSON 解码到 out。
func runJSONCmd(ctx context.Context, exec ContainerExecer, nodeID, containerID string, cmd []string, out interface{}) error {
	stdout, _, err := exec.ContainerExec(ctx, nodeID, containerID, cmd)
	if err != nil {
		return fmt.Errorf("exec %s: %w", cmd[0], err)
	}
	data, err := io.ReadAll(stdout)
	if err != nil {
		return fmt.Errorf("read stdout %s: %w", cmd[0], err)
	}
	if err := json.Unmarshal(trim(data), out); err != nil {
		return fmt.Errorf("decode %s stdout: %w", cmd[0], err)
	}
	return nil
}

// trim 去掉末尾换行；oc-* 命令统一以 \n 结尾。
func trim(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}
