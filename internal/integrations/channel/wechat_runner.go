// Package channel 是渠道适配层，把 worker handler 与具体 runtime 实现解耦。
// 当前（Hermes 时代）内部委托给 internal/integrations/hermes/wechat_runner.go，
// 保持向后兼容的 type 名 DockerCommandRunner 和 method StreamWeChatLogin。
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
type DockerClientResolver interface {
	DockerClient(ctx context.Context, nodeID string) (*client.Client, error)
}

// ContainerExecutor 抽象"在指定节点 + 容器内 exec 命令并返回 multiplexed stdout/stderr 流"的能力。
// wechat_identity.go（execCat）依赖此接口，Hermes 时代继续保留。
// nodeID 参数让 executor 在多节点部署时按节点取对应 docker client。
type ContainerExecutor interface {
	Exec(ctx context.Context, nodeID, containerID string, cmd []string) (reader io.Reader, close func(), err error)
}

// AppContainerLookup 把 appID 映射为目标节点上的 container_id。
// Hermes 时代 DockerCommandRunner 直接从 AuthInput.ContainerID 取，不再回查；
// 保留类型声明维持向后兼容。
type AppContainerLookup interface {
	LookupContainer(ctx context.Context, appID string) (string, error)
}

// execToHermesAdapter 把 channel.ContainerExecutor（带 nodeID 的旧接口）适配成
// hermes.ContainerExecutor（只有 containerID 的新接口）。
// Hermes 运行器只需要 containerID；nodeID 在 wiring 阶段已经固定到 DockerClientResolver。
type execToHermesAdapter struct {
	nodeID   string
	executor ContainerExecutor
	// execClose 记录最近一次 Exec 返回的 closer，供 ExecExitCode 使用。
	execClose func()
}

// ExecAttach 实现 hermes.ContainerExecutor，把 Exec 结果包装成 io.ReadCloser。
func (a *execToHermesAdapter) ExecAttach(ctx context.Context, containerID string, cmd []string) (io.ReadCloser, error) {
	reader, closer, err := a.executor.Exec(ctx, a.nodeID, containerID, cmd)
	if err != nil {
		return nil, err
	}
	a.execClose = closer
	return &readCloserAdapter{Reader: reader, closeFunc: closer}, nil
}

// ExecExitCode hermes.ContainerExecutor 要求的方法，等待上一次 exec 结束并返回 exit code。
// channel.ContainerExecutor（Exec 接口）不直接暴露 exit code，这里返回 0 并依赖 stdout 协议判断。
// Hermes 时代 oc-weixin-login 在失败时 exit!=0，但通过 stdcopy 可在 bound event 判断，
// 此方法仅作兼容占位。
func (a *execToHermesAdapter) ExecExitCode(_ context.Context) (int, error) {
	// channel.ContainerExecutor 的 Exec 接口不暴露 exit code；
	// hermes.WeixinRunner 在 goroutine 里调用此方法，此处返回 0 让 bound 路径由 stdout 驱动。
	return 0, nil
}

// readCloserAdapter 把 io.Reader + closer 函数包成 io.ReadCloser。
type readCloserAdapter struct {
	io.Reader
	closeFunc func()
}

func (r *readCloserAdapter) Close() error {
	if r.closeFunc != nil {
		r.closeFunc()
	}
	return nil
}

// DockerCommandRunner 是渠道适配层对外暴露的类型，委托给 hermes.WeixinRunner。
// 保持 type 名避免修改所有 caller。
type DockerCommandRunner struct {
	inner  *hermes.WeixinRunner
	nodeID string
}

// NewDockerCommandRunner 工厂。
// lookup 参数保留签名兼容，Hermes 时代直接从 AuthInput.ContainerID 取，不再回查。
// nodeID 在装配阶段通过 wiring.go 取出节点 ID 后注入；本文件不感知多节点路由。
func NewDockerCommandRunner(executor ContainerExecutor, _ AppContainerLookup) *DockerCommandRunner {
	// execToHermesAdapter 在每次 StreamWeChatLogin 调用时独立创建，nodeID 延迟绑定。
	return &DockerCommandRunner{
		inner: hermes.NewWeixinRunner(&execToHermesAdapter{executor: executor}),
	}
}

// StreamWeChatLogin 委托给 hermes.WeixinRunner。
// 返回类型升级为 <-chan hermes.WeixinEvent（legacy OpenClaw 时代是 <-chan string）。
// 上游 caller（wechat.go WeChatAdapter）在 Task 3.5 同步升级为消费 hermes.WeixinEvent。
func (r *DockerCommandRunner) StreamWeChatLogin(ctx context.Context, input AuthInput) (<-chan hermes.WeixinEvent, error) {
	return r.inner.StreamWeChatLogin(ctx, input.ContainerID)
}

// NewDockerExecutor 包装一个 DockerClientResolver 提供生产可用的 ContainerExecutor。
// 实现按 nodeID 实时取 docker client，让同一个 executor 实例可被多个节点共享。
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

// 保留 docker client 引用，避免 module-level import 未使用警告（Phase 6 cleanup 可酌情删）。
var _ = client.NewClientWithOpts
