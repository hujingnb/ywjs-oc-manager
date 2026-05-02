package channel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// DockerClientResolver 与 runtime 包同名接口形态保持一致：根据 nodeID 取出 docker SDK client。
// 这里不直接 import runtime 包是为了避免 channel ↔ runtime 的循环依赖；
// cmd/server 装配阶段同时把 manager 端的 docker resolver 实现传给两侧。
type DockerClientResolver interface {
	DockerClient(ctx context.Context, nodeID string) (*client.Client, error)
}

// AppContainerLookup 把 appID 映射为目标节点上的 container_id。
// 微信 runner 不直接读 PostgreSQL，由调用方在装配阶段注入 lookup，便于测试解耦。
type AppContainerLookup interface {
	LookupContainer(ctx context.Context, appID string) (string, error)
}

// ContainerExecutor 抽象 "在指定节点 + 容器内 exec 一段命令并返回 multiplexed stdout/stderr 流" 的能力。
//
// 这一层抽象把 docker SDK 的 hijack 协议隔离到生产实现里：
//   - 测试用内存版直接产出帧，不依赖真实 docker；
//   - 生产实现 dockerExecutor 包 ContainerExecCreate + ContainerExecAttach；
//   - reader 必须遵循 docker stdcopy 协议（stream_type + size + payload）；
//   - close 用于在 ctx 取消或 reader 走完时释放底层连接；
//   - nodeID 让 executor 在多节点部署时按节点取对应 docker client。
type ContainerExecutor interface {
	Exec(ctx context.Context, nodeID, containerID string, cmd []string) (reader io.Reader, close func(), err error)
}

// DockerCommandRunner 通过 ContainerExecutor 在 OpenClaw 容器内执行 channels login 命令，
// 把 stdout 行式输出回送给 channel.WeChatAdapter 解析。
type DockerCommandRunner struct {
	executor ContainerExecutor
	lookup   AppContainerLookup
}

// NewDockerCommandRunner 构造 runner。两个依赖都不能为 nil，
// 缺失会让 runner 在第一次调用时直接报错（构造阶段不强制 panic 是为了让 cmd/server 装配更灵活）。
func NewDockerCommandRunner(executor ContainerExecutor, lookup AppContainerLookup) *DockerCommandRunner {
	return &DockerCommandRunner{executor: executor, lookup: lookup}
}

// StreamWeChatLogin 启动 OpenClaw 容器内的 `openclaw channels login --channel openclaw-weixin --verbose`
// 命令并把 stdout 按行回 channel。返回的 channel 在命令结束时由 runner 自动关闭。
//
// Sprint 0 POC 验证：上游 OpenClaw `channels login` 不支持 `--json` 标志，
// stdout 输出为中文提示 + ASCII QR + 回退 URL（详见 docs/superpowers/poc/.../06-qrcode-format.md）。
// parser 负责从文本流抓回退 URL 作为 QRCode payload，前端用 URL 重生 QR 图。
func (r *DockerCommandRunner) StreamWeChatLogin(ctx context.Context, input AuthInput) (<-chan string, error) {
	if r.executor == nil {
		return nil, errors.New("container executor 未配置")
	}
	if r.lookup == nil {
		return nil, errors.New("container lookup 未配置")
	}
	if input.NodeID == "" {
		return nil, errors.New("AuthInput.NodeID 不能为空")
	}
	containerID := input.ContainerID
	if containerID == "" {
		looked, err := r.lookup.LookupContainer(ctx, input.AppID)
		if err != nil {
			return nil, fmt.Errorf("查找容器失败: %w", err)
		}
		containerID = looked
	}
	if containerID == "" {
		return nil, fmt.Errorf("应用 %s 没有可用容器", input.AppID)
	}
	reader, closer, err := r.executor.Exec(ctx, input.NodeID, containerID, []string{"openclaw", "channels", "login", "--channel", "openclaw-weixin", "--verbose"})
	if err != nil {
		return nil, err
	}
	out := make(chan string, 16)
	go pumpExecStream(ctx, reader, closer, out)
	return out, nil
}

// pumpExecStream 把 multiplexed exec stream 拆成 stdout 行送进 channel。
// 即使 stderr 有内容也只丢到 io.Discard：channels login --verbose 把二维码 / 状态文本写到 stdout，
// stderr 仅用于 plugin warnings 与调试日志，对 manager parser 无意义。
//
// closer 用于在 ctx 取消或 reader 走完时统一释放底层 net.Conn，避免文件描述符泄漏。
// closer 内部需要幂等，因为 ctx 取消监听器与 defer 都可能触发它。
func pumpExecStream(ctx context.Context, reader io.Reader, closer func(), out chan<- string) {
	defer close(out)
	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			if closer != nil {
				closer()
			}
		})
	}
	defer closeAll()

	stdoutReader, stdoutWriter := io.Pipe()
	demuxDone := make(chan struct{})
	go func() {
		defer close(demuxDone)
		_, _ = stdcopy.StdCopy(stdoutWriter, io.Discard, reader)
		_ = stdoutWriter.Close()
	}()

	// ctx 取消时主动 closer：让底层 reader 立刻 EOF，stdcopy goroutine 才能退出。
	go func() {
		<-ctx.Done()
		closeAll()
		_ = stdoutReader.Close()
	}()

	scanner := bufio.NewScanner(stdoutReader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		select {
		case <-ctx.Done():
			closeAll()
			_ = stdoutReader.Close()
			<-demuxDone
			return
		case out <- line:
		}
	}
	<-demuxDone
}

// NewDockerExecutor 包装一个 DockerClientResolver 提供生产可用的 ContainerExecutor。
// 实现按 nodeID 实时取 docker client，让同一个 executor 实例可被多个节点共享。
func NewDockerExecutor(resolver DockerClientResolver) ContainerExecutor {
	return &dockerExecutor{resolver: resolver}
}

type dockerExecutor struct {
	resolver DockerClientResolver
}

func (d *dockerExecutor) Exec(ctx context.Context, nodeID, containerID string, cmd []string) (io.Reader, func(), error) {
	cli, err := d.resolver.DockerClient(ctx, nodeID)
	if err != nil {
		return nil, nil, fmt.Errorf("取 docker client 失败: %w", err)
	}
	exec, err := cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("ContainerExecCreate 失败: %w", err)
	}
	attach, err := cli.ContainerExecAttach(ctx, exec.ID, container.ExecStartOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("ContainerExecAttach 失败: %w", err)
	}
	return attach.Reader, attach.Close, nil
}
