// Package channel 是渠道适配层，把 worker handler 与具体 runtime 实现解耦。
// 微信扫码登录当前委托给 internal/integrations/hermes/wechat_runner.go 的
// WeixinRunner，后者经 oc-ops HTTP SSE 触发登录并翻译事件；保持向后兼容的
// type 名 DockerCommandRunner 和 method StreamWeChatLogin。
package channel

import (
	"context"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"

	"oc-manager/internal/integrations/hermes"
)

// DockerClientResolver 与 runtime 包同名接口形态保持一致：根据 nodeID 取出 docker SDK client。
// 这里不直接 import runtime 包是为了避免 channel ↔ runtime 的循环依赖；
// cmd/server 装配阶段同时把 manager 端的 docker resolver 实现传给两侧。
//
// 微信扫码绑定身份已改走 oc-ops ChannelStatus（spec-A2a），不再依赖 docker exec 链路；
// 当前 docker exec 通道无生产消费者，待 spec-A2b 节点摘除时统一清理。
type DockerClientResolver interface {
	DockerClient(ctx context.Context, nodeID string) (*client.Client, error)
}

// ContainerExecutor 抽象"在指定节点 + 容器内 exec 命令并返回 multiplexed stdout/stderr 流"的能力。
// nodeID 参数让 executor 在多节点部署时按节点取对应 docker client。
//
// NOTE：spec-A2a 后微信绑定身份已改走 oc-ops ChannelStatus，此 docker exec 通道
// 当前无生产消费者，保留待 spec-A2b 节点摘除时统一清理。
type ContainerExecutor interface {
	Exec(ctx context.Context, nodeID, containerID string, cmd []string) (reader io.Reader, close func(), err error)
}

// AppContainerLookup 把 appID 映射为目标节点上的 container_id。
// Hermes 时代 DockerCommandRunner 直接从 AuthInput 取，不再回查；
// 保留类型声明维持向后兼容。
type AppContainerLookup interface {
	LookupContainer(ctx context.Context, appID string) (string, error)
}

// DockerCommandRunner 是渠道适配层对外暴露的类型，委托给 hermes.WeixinRunner。
// 保持 type 名避免修改所有 caller；登录传输已从 docker exec 切换为 oc-ops HTTP SSE。
type DockerCommandRunner struct {
	// streamer 触发 oc-ops 渠道登录并订阅 SSE 事件流，生产实现为 *ocops.Client。
	streamer hermes.ChannelLoginStreamer
}

// NewDockerCommandRunner 工厂。
// streamer 满足 oc-ops SSE 登录能力（*ocops.Client）；每次 StreamWeChatLogin
// 调用时按 AuthInput.Endpoint 构造临时 hermes.WeixinRunner，把目标 app 实例坐标
// 注入，确保登录请求打到正确的 oc-ops 实例。
func NewDockerCommandRunner(streamer hermes.ChannelLoginStreamer) *DockerCommandRunner {
	return &DockerCommandRunner{streamer: streamer}
}

// StreamWeChatLogin 委托给 hermes.WeixinRunner，经 oc-ops HTTP SSE 触发微信登录。
// 返回 <-chan hermes.WeixinEvent，由上游 WeChatAdapter 消费。
func (r *DockerCommandRunner) StreamWeChatLogin(ctx context.Context, input AuthInput) (<-chan hermes.WeixinEvent, error) {
	// per-call 构造 runner，把 input.Endpoint（目标 app 实例的 oc-ops 坐标）注入，
	// 确保多 app 部署下登录请求路由到正确实例。
	runner := hermes.NewWeixinRunner(r.streamer, input.Endpoint)
	return runner.StreamWeChatLogin(ctx)
}

// NewDockerExecutor 包装一个 DockerClientResolver 提供生产可用的 ContainerExecutor。
// 实现按 nodeID 实时取 docker client，让同一个 executor 实例可被多个节点共享。
// spec-A2a 后微信绑定身份改走 oc-ops，此通道当前无生产消费者，待 spec-A2b 统一清理。
func NewDockerExecutor(resolver DockerClientResolver) ContainerExecutor {
	return &dockerExecutor{resolver: resolver}
}

// dockerExecutor 生产实现 ContainerExecutor，通过 docker SDK exec + attach 返回 multiplexed 流。
type dockerExecutor struct {
	resolver DockerClientResolver
}

// Exec 在指定 runtime node 的容器内启动命令，并返回可读输出流与清理函数。
// 返回的 reader 是 docker stdcopy multiplex 流（8 字节 header + payload），
// 调用方负责用 stdcopy.StdCopy 解包（wechat_identity.go 的 execCat 已自行解包）。
func (d *dockerExecutor) Exec(ctx context.Context, nodeID, containerID string, cmd []string) (io.Reader, func(), error) {
	cli, err := d.resolver.DockerClient(ctx, nodeID)
	if err != nil {
		return nil, nil, err
	}
	exec, err := cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, nil, err
	}
	attach, err := cli.ContainerExecAttach(ctx, exec.ID, container.ExecStartOptions{})
	if err != nil {
		return nil, nil, err
	}
	return attach.Reader, attach.Close, nil
}

// 保留 docker client 引用，避免 module-level import 未使用警告。
var _ = client.NewClientWithOpts
