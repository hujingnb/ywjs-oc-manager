package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"oc-manager/internal/integrations/agent"
)

// AgentResolver 根据 nodeID 取出 manager 需要的 agent 文件 client。
// 注册流程会把 agent 的 file/docker endpoint 与 token 写入 runtime_nodes，
// 调用方需在请求时按 nodeID 取出对应客户端，否则不同节点会被错误路由。
type AgentResolver interface {
	FileClient(ctx context.Context, nodeID string) (*agent.AgentFileClient, error)
}

// DockerClientResolver 根据 nodeID 取出对应节点的 docker SDK client。
// 实现负责处理 agent token 缓存、TLS CA 与 endpoint 解析；adapter 只调用接口，
// 不感知 manager 进程内的具体连接生命周期管理。
type DockerClientResolver interface {
	DockerClient(ctx context.Context, nodeID string) (*client.Client, error)
}

// ContainerInspector 是 WaitContainerHealthy 依赖的最小接口，便于测试注入 fake。
// 生产路径下由 AgentBackedAdapter 自身实现（调用 docker inspect）；
// 测试中传入 fakeInspector 可控序列，避免依赖真实 docker daemon。
type ContainerInspector interface {
	InspectContainer(ctx context.Context, nodeID, containerID string) (ContainerInfo, error)
}

// AgentBackedAdapter 通过 agent HTTP API 完成 runtime adapter 协议。
//
// 三个核心依赖：
//   - files：缺失时文件接口返回 ErrUnimplemented；
//   - docker：缺失时容器接口返回 ErrUnimplemented，避免未装配 docker proxy 时静默 panic；
//   - streamingDocker：可选，用于长连接 ExecAttach 场景（ContainerExecStream）；
//     nil 时回退到 docker，调用方需确保 docker resolver 返回无 timeout 的 client；
//   - inspector：nil 时 WaitContainerHealthy 使用自身的 InspectContainer，非 nil 时用于测试覆盖。
type AgentBackedAdapter struct {
	files  AgentResolver
	docker DockerClientResolver
	// streamingDocker 专供 ContainerExecStream 使用；nil 时回退到 docker。
	// 生产装配时注入 streamingDockerResolver（无 timeout），避免长连接被 30s 截断。
	streamingDocker DockerClientResolver
	// inspector 仅用于测试注入；nil 时 WaitContainerHealthy 使用自身的 InspectContainer 方法。
	inspector ContainerInspector
}

// NewAgentBackedAdapter 构造 adapter。参数为 nil 时对应能力降级为 ErrUnimplemented。
func NewAgentBackedAdapter(files AgentResolver, docker DockerClientResolver) *AgentBackedAdapter {
	return &AgentBackedAdapter{files: files, docker: docker}
}

// SetStreamingDocker 注入专供流式长连接（ContainerExecStream）使用的 docker client resolver。
// 该 resolver 应返回无 http.Client.Timeout 的 client，防止 hermes kanban watch 长连接被截断。
// 不调时 ContainerExecStream 回退到普通 docker resolver，调用方需自行保证超时配置兼容。
// 命名与项目现有 Set* setter（SetInspector、SetJobNotifier 等）保持一致，void 返回。
func (a *AgentBackedAdapter) SetStreamingDocker(streaming DockerClientResolver) {
	a.streamingDocker = streaming
}

// DockerClientForNode 返回指向目标节点 agent docker proxy 的 SDK client，
// 专供 AppInitializeHandler.phasePullRuntimeImage 拉取运行时镜像使用，不经过 Adapter 接口。
//
// 必须返回无 http.Client.Timeout 的 streaming 客户端：docker ImagePull 是流式接口，
// NDJSON 进度流会一直保持到整个镜像拉完，大镜像耗时远超普通 docker client 的 30s
// http.Client.Timeout。该 Timeout 是「含响应 body 读取」的整请求硬上限，请求 ctx
// 只能让它更短、无法延长；若走带 timeout 的客户端，拉取会在 30s 后被强制断流，
// 报 "context deadline exceeded (Client.Timeout ... while reading body)"。
// 与 ContainerExecStream 复用同一 streaming resolver；未注入时回退到普通 resolver。
func (a *AgentBackedAdapter) DockerClientForNode(ctx context.Context, nodeID string) (*client.Client, error) {
	return a.streamingDockerClient(ctx, nodeID)
}

// CreateContainer 通过 agent docker 代理在指定节点上创建容器。
//
// 流程：
//  1. resolver 取 docker SDK client（每次调用都重新取，便于 token 轮换后立即生效）；
//  2. 把 ContainerSpec 翻译成 container.Config / container.HostConfig / network.NetworkingConfig；
//  3. ContainerCreate 后立即 ContainerInspect 以拿到完整状态信息；
//  4. 不在此处调 ContainerStart——由 worker handler 在状态机推进时单独触发。
//
// 任何 docker 端错误都直接冒泡，避免 adapter 层吞错；调用方负责审计与状态机回写。
func (a *AgentBackedAdapter) CreateContainer(ctx context.Context, nodeID string, spec ContainerSpec) (ContainerInfo, error) {
	cli, err := a.dockerClient(ctx, nodeID)
	if err != nil {
		return ContainerInfo{}, err
	}
	containerCfg, hostCfg, networkCfg := translateSpec(spec)
	resp, err := cli.ContainerCreate(ctx, containerCfg, hostCfg, networkCfg, nil, spec.Name)
	if err != nil {
		return ContainerInfo{}, fmt.Errorf("创建容器失败: %w", err)
	}
	inspect, err := cli.ContainerInspect(ctx, resp.ID)
	if err != nil {
		return ContainerInfo{}, fmt.Errorf("inspect 容器失败: %w", err)
	}
	return inspectToContainerInfo(inspect), nil
}

// containerStopTimeout 是 stop 调用给容器的优雅退出窗口。
// 30s 是 docker CLI 默认值；超过后 docker 会发 SIGKILL 强行终止。
const containerStopTimeout = 30

// StartContainer 通过 agent docker 代理启动容器。
// docker SDK 对已经 running 的容器 ContainerStart 会返回 304/已运行错误，
// 这里不吞错——由 worker handler 在调用前 inspect 状态后做幂等判断。
func (a *AgentBackedAdapter) StartContainer(ctx context.Context, nodeID, containerID string) error {
	cli, err := a.dockerClient(ctx, nodeID)
	if err != nil {
		return err
	}
	if err := cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("启动容器失败: %w", err)
	}
	return nil
}

// StopContainer 通过 agent docker 代理停止容器，给 30s 优雅退出窗口。
func (a *AgentBackedAdapter) StopContainer(ctx context.Context, nodeID, containerID string) error {
	cli, err := a.dockerClient(ctx, nodeID)
	if err != nil {
		return err
	}
	timeout := containerStopTimeout
	if err := cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("停止容器失败: %w", err)
	}
	return nil
}

// RestartContainer 通过 agent docker 代理重启容器。
// docker 的 restart 等价于 stop + start，但调用更省一次往返。
func (a *AgentBackedAdapter) RestartContainer(ctx context.Context, nodeID, containerID string) error {
	cli, err := a.dockerClient(ctx, nodeID)
	if err != nil {
		return err
	}
	timeout := containerStopTimeout
	if err := cli.ContainerRestart(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("重启容器失败: %w", err)
	}
	return nil
}

// RemoveContainer 通过 agent docker 代理删除容器。
// 默认带 Force=true，确保 running 状态的容器也能被清理；调用方需要先调 StopContainer 才会
// 触发优雅退出，本方法本身只保证最终删除。
func (a *AgentBackedAdapter) RemoveContainer(ctx context.Context, nodeID, containerID string) error {
	cli, err := a.dockerClient(ctx, nodeID)
	if err != nil {
		return err
	}
	if err := cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("删除容器失败: %w", err)
	}
	return nil
}

// InspectContainer 通过 agent docker 代理 inspect 容器并返回状态视图。
func (a *AgentBackedAdapter) InspectContainer(ctx context.Context, nodeID, containerID string) (ContainerInfo, error) {
	cli, err := a.dockerClient(ctx, nodeID)
	if err != nil {
		return ContainerInfo{}, err
	}
	inspect, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return ContainerInfo{}, fmt.Errorf("inspect 容器失败: %w", err)
	}
	return inspectToContainerInfo(inspect), nil
}

// ContainerStats 通过 docker /containers/{id}/stats?stream=false 拿到一次性指标。
// CPU 用 docker 推荐公式：(cpu_delta / system_delta) * online_cpus * 100；首采样无 precpu 时返回 0。
// 网络对所有 interfaces 累加，避免遗漏 host 网络模式下的 lo。
func (a *AgentBackedAdapter) ContainerStats(ctx context.Context, nodeID, containerID string) (ContainerStats, error) {
	cli, err := a.dockerClient(ctx, nodeID)
	if err != nil {
		return ContainerStats{}, err
	}
	resp, err := cli.ContainerStatsOneShot(ctx, containerID)
	if err != nil {
		return ContainerStats{}, fmt.Errorf("拉取容器 stats 失败: %w", err)
	}
	defer resp.Body.Close()
	var raw container.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return ContainerStats{}, fmt.Errorf("解析容器 stats 失败: %w", err)
	}
	return statsResponseToContainerStats(raw), nil
}

// ContainerExec 在容器内 exec 一个一次性命令，等待结束后返回 exit code 与 stdout。
// 用于 app_health_check：cmd 通常是 ["sh","-c","curl -fsS http://127.0.0.1:18789/healthz"]。
// 实现注意：
//   - HijackResp.Reader 的 docker stream 协议带 8 字节 header（multiplexed stdout/stderr），
//     这里用 stdcopy.StdCopy 复制；不引入额外依赖时直接拼裸字节也能反映"是否非空"。
//   - 轮询 ContainerExecInspect 直到 Running=false；总等待上限 inspectPollMax * inspectPollInterval。
func (a *AgentBackedAdapter) ContainerExec(ctx context.Context, nodeID, containerID string, cmd []string) (ExecResult, error) {
	cli, err := a.dockerClient(ctx, nodeID)
	if err != nil {
		return ExecResult{}, err
	}
	resp, err := cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return ExecResult{}, fmt.Errorf("创建 exec 失败: %w", err)
	}
	att, err := cli.ContainerExecAttach(ctx, resp.ID, container.ExecStartOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("附加 exec 失败: %w", err)
	}
	defer att.Close()
	// docker stream 的 multiplexed 帧带 header，直接读全部字节做截断保留；后续如需精确分离 stdout/stderr 可换 stdcopy。
	limited := io.LimitReader(att.Reader, 4096)
	body, _ := io.ReadAll(limited)
	const inspectPollMax = 50
	for i := 0; i < inspectPollMax; i++ {
		insp, err := cli.ContainerExecInspect(ctx, resp.ID)
		if err != nil {
			return ExecResult{}, fmt.Errorf("inspect exec 失败: %w", err)
		}
		if !insp.Running {
			return ExecResult{ExitCode: insp.ExitCode, Stdout: string(body)}, nil
		}
		select {
		case <-ctx.Done():
			return ExecResult{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return ExecResult{}, fmt.Errorf("exec 超时")
}

// ContainerExecJSON 在容器内执行一次性命令，返回完整 stdout/stderr。
// 与 ContainerExec 区别：用 stdcopy 分离 stdout/stderr，stdout 不截断，
// 便于上层对 hermes kanban --json 输出做 JSON 解析。
func (a *AgentBackedAdapter) ContainerExecJSON(ctx context.Context, nodeID, containerID string, cmd []string) (ExecJSONResult, error) {
	cli, err := a.dockerClient(ctx, nodeID)
	if err != nil {
		return ExecJSONResult{}, err
	}
	resp, err := cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return ExecJSONResult{}, fmt.Errorf("创建 exec 失败: %w", err)
	}
	att, err := cli.ContainerExecAttach(ctx, resp.ID, container.ExecStartOptions{})
	if err != nil {
		return ExecJSONResult{}, fmt.Errorf("附加 exec 失败: %w", err)
	}
	defer att.Close()
	// docker multiplexed 流：用 stdcopy 拆成干净的 stdout / stderr 两段。
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, att.Reader); err != nil {
		return ExecJSONResult{}, fmt.Errorf("读取 exec 输出失败: %w", err)
	}
	// exec 结束后 inspect 拿退出码；attach 已读到 EOF，命令通常已退出，仍轮询保险。
	const inspectPollMax = 50
	for i := 0; i < inspectPollMax; i++ {
		insp, err := cli.ContainerExecInspect(ctx, resp.ID)
		if err != nil {
			return ExecJSONResult{}, fmt.Errorf("inspect exec 失败: %w", err)
		}
		if !insp.Running {
			return ExecJSONResult{
				ExitCode: insp.ExitCode,
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
			}, nil
		}
		select {
		case <-ctx.Done():
			return ExecJSONResult{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	// 超时返回时带上已读数据，便于排障时看到部分输出。
	return ExecJSONResult{Stdout: stdout.String(), Stderr: stderr.String()}, fmt.Errorf("exec 超时（已读 stdout %dB / stderr %dB）", stdout.Len(), stderr.Len())
}

// ContainerExecStream 在容器内执行流式命令，逐行投递 stdout。
// 用无 timeout 的 streaming docker client，避免 hermes kanban watch 长连接被掐断。
func (a *AgentBackedAdapter) ContainerExecStream(ctx context.Context, nodeID, containerID string, cmd []string) (ExecStreamHandle, error) {
	cli, err := a.streamingDockerClient(ctx, nodeID)
	if err != nil {
		return ExecStreamHandle{}, err
	}
	resp, err := cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return ExecStreamHandle{}, fmt.Errorf("创建 exec 失败: %w", err)
	}
	att, err := cli.ContainerExecAttach(ctx, resp.ID, container.ExecStartOptions{})
	if err != nil {
		return ExecStreamHandle{}, fmt.Errorf("附加 exec 失败: %w", err)
	}

	lines := make(chan string, 64)
	// errCh 带缓冲容量 1，goroutine 在 scanner 自然结束后写入一次；
	// 调用方通过 Err() 的 select default 无阻塞读取，消除 streamErr 裸变量的数据竞争。
	errCh := make(chan error, 1)
	streamCtx, cancel := context.WithCancel(ctx)

	go func() {
		defer close(lines)
		defer att.Close()
		// stdcopy 把 multiplexed 流拆出 stdout 写进 pipe，再按行扫描。
		pr, pw := io.Pipe()
		defer pr.Close()
		go func() {
			_, copyErr := stdcopy.StdCopy(pw, io.Discard, att.Reader)
			pw.CloseWithError(copyErr)
		}()
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			select {
			case <-streamCtx.Done():
				// 主动取消路径：不发 errCh，取消不算错误。
				return
			case lines <- scanner.Text():
			}
		}
		// scanner 自然结束路径：计算结果错误后写入 errCh（缓冲容量 1，不会阻塞）。
		var resultErr error
		if err := scanner.Err(); err != nil && streamCtx.Err() == nil {
			resultErr = err
		}
		errCh <- resultErr
	}()

	return ExecStreamHandle{
		Lines: lines,
		Err: func() error {
			select {
			case e := <-errCh:
				return e
			default:
				return nil
			}
		},
		Close: cancel,
	}, nil
}

// statsResponseToContainerStats 把 docker 原始 stats 响应转换为前端可直接展示的累计指标。
// 首次采样没有有效 delta 时 CPUPercent 保持 0，避免用不完整数据制造尖峰。
func statsResponseToContainerStats(raw container.StatsResponse) ContainerStats {
	out := ContainerStats{
		MemoryUsage: raw.MemoryStats.Usage,
		MemoryLimit: raw.MemoryStats.Limit,
	}
	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage) - float64(raw.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(raw.CPUStats.SystemUsage) - float64(raw.PreCPUStats.SystemUsage)
	if systemDelta > 0 && cpuDelta > 0 {
		online := float64(raw.CPUStats.OnlineCPUs)
		if online == 0 {
			online = float64(len(raw.CPUStats.CPUUsage.PercpuUsage))
		}
		if online == 0 {
			online = 1
		}
		out.CPUPercent = (cpuDelta / systemDelta) * online * 100
	}
	for _, n := range raw.Networks {
		out.NetworkRxBytes += n.RxBytes
		out.NetworkTxBytes += n.TxBytes
	}
	for _, entry := range raw.BlkioStats.IoServiceBytesRecursive {
		// Docker 在不同平台可能省略 blkio；缺失时保持 0，不把不可用指标当成采样错误。
		switch strings.ToLower(entry.Op) {
		case "read":
			out.DiskReadBytes += entry.Value
		case "write":
			out.DiskWriteBytes += entry.Value
		}
	}
	return out
}

// ListFiles 通过 agent file API 列出目录。
func (a *AgentBackedAdapter) ListFiles(ctx context.Context, nodeID, remotePath string) (FileListing, error) {
	client, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return FileListing{}, err
	}
	return client.List(ctx, remotePath)
}

// UploadFile 通过 agent file API 上传文件。
func (a *AgentBackedAdapter) UploadFile(ctx context.Context, nodeID, remotePath string, content io.Reader) error {
	client, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return err
	}
	return client.Upload(ctx, remotePath, content)
}

// DownloadFile 通过 agent file API 下载文件。
func (a *AgentBackedAdapter) DownloadFile(ctx context.Context, nodeID, remotePath string) (io.ReadCloser, error) {
	client, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return client.Download(ctx, remotePath)
}

// ArchiveDirectory 通过 agent file API 打包目录。
func (a *AgentBackedAdapter) ArchiveDirectory(ctx context.Context, nodeID, remotePath string) (io.ReadCloser, error) {
	client, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return client.Archive(ctx, remotePath)
}

// DeletePath 通过 agent file API 删除路径。
func (a *AgentBackedAdapter) DeletePath(ctx context.Context, nodeID, remotePath string) error {
	client, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return err
	}
	return client.Delete(ctx, remotePath)
}

// ============================================================================
// Sprint 1：scope-aware 包装方法。worker handler 用这些方法发出领域级请求，
// 不再让 handler 拼 "apps/<id>/knowledge/<rel>" 这种业务路径。
// ============================================================================

// InitAppDirs 让节点 agent 准备 apps/<appID>/{knowledge,workspace,state,logs}
// 4 个子目录。app_initialize handler 在 CreateContainer 之前调一次。
func (a *AgentBackedAdapter) InitAppDirs(ctx context.Context, nodeID, appID string) error {
	cli, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return err
	}
	return cli.InitAppDirs(ctx, appID)
}

// ListWorkspace 列举应用 workspace 下的内容。
func (a *AgentBackedAdapter) ListWorkspace(ctx context.Context, nodeID, appID, relPath string) (WorkspaceListing, error) {
	cli, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return WorkspaceListing{}, err
	}
	raw, err := cli.ListWorkspace(ctx, appID, relPath)
	if err != nil {
		return WorkspaceListing{}, err
	}
	out := WorkspaceListing{Path: raw.Path, Entries: make([]WorkspaceEntry, 0, len(raw.Entries))}
	for _, e := range raw.Entries {
		out.Entries = append(out.Entries, WorkspaceEntry{
			Name:       e.Name,
			Type:       e.Type,
			Size:       e.Size,
			ModifiedAt: e.ModifiedAt,
		})
	}
	return out, nil
}

// DownloadWorkspaceFile 流式下载应用 workspace 下的单文件。调用方负责 Close。
func (a *AgentBackedAdapter) DownloadWorkspaceFile(ctx context.Context, nodeID, appID, relPath string) (io.ReadCloser, error) {
	cli, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return cli.DownloadWorkspaceFile(ctx, appID, relPath)
}

// StreamWorkspaceArchive 把应用 workspace 下的指定目录流式 zip 写到 w。
func (a *AgentBackedAdapter) StreamWorkspaceArchive(ctx context.Context, nodeID, appID, relPath string, w io.Writer) error {
	cli, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return err
	}
	return cli.StreamWorkspaceArchive(ctx, appID, relPath, w)
}

// ArchiveApp 让 agent 把节点上 apps/<appID>/ 整目录归档到 archived/<id>-<ts>/。
func (a *AgentBackedAdapter) ArchiveApp(ctx context.Context, nodeID, appID string) error {
	cli, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return err
	}
	return cli.ArchiveApp(ctx, appID)
}

// WaitContainerHealthy 轮询 docker inspect 拿 .State.Health.Status，
// 等到 "healthy" 或 ctx 超时为止。
// 用于 app_initialize handler 在容器启动后等 Hermes HEALTHCHECK 通过。
// HEALTHCHECK 内部跑 hermes gateway status，初始 start-period 60s，
// 留 timeout（通常 120s）余量。
//
// 遇到 "unhealthy" 时快速失败：不再等 timeout，立即返回错误并附上最后一次 Output。
// 遇到其他状态（starting / ""）则按 step 轮询，直到 deadline。
func (a *AgentBackedAdapter) WaitContainerHealthy(ctx context.Context, nodeID, containerID string, timeout time.Duration) error {
	// 确定实际用于 inspect 的接口：nil 时走自身 InspectContainer。
	insp := a.inspector
	if insp == nil {
		insp = a
	}
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	const step = 3 * time.Second
	for {
		info, err := insp.InspectContainer(deadline, nodeID, containerID)
		if err != nil {
			return err
		}
		switch info.Health.Status {
		case "healthy":
			return nil
		case "unhealthy":
			// HEALTHCHECK 明确失败，不再轮询，立即返回带 Output 的错误。
			return fmt.Errorf("容器 %s HEALTHCHECK 返回 unhealthy: %s", containerID, info.Health.Output)
		}
		// "starting" 或 ""（未配置 HEALTHCHECK）继续等待。
		select {
		case <-deadline.Done():
			return fmt.Errorf("容器 %s 在 %s 内未达 healthy", containerID, timeout)
		case <-time.After(step):
		}
	}
}

// UploadAppInputFile 把 manager 渲染的 Hermes 输入资源
// (manifest.yaml / resources/* / skills/*)
// 上传到目标节点 apps/<appID>/input/<relPath>;容器启动时 oc-entrypoint 读取该目录并
// 完成 hermes 自有 schema 装配 (SOUL.md / config.yaml / .env / skills/<name>/SKILL.md)。
// hermes-agent-pull 切换完成后, 这是 manager 端写应用输入文件的唯一路径——
// 老的 runtime/file 与 knowledge/file 路由已随 agent T13 路由下线统一移除。
func (a *AgentBackedAdapter) UploadAppInputFile(ctx context.Context, nodeID, appID, relPath string, content io.Reader) error {
	cli, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return err
	}
	return cli.UploadAppInputFile(ctx, appID, relPath, content)
}

// DeleteAppInputFile 删除目标节点 apps/<appID>/input/<relPath> 下的文件 / 子目录。
// 当前主要用于清理 manager 渲染进 input 的 manifest/resources/skills 文件。
//
// 文件不存在视为成功(幂等)。
func (a *AgentBackedAdapter) DeleteAppInputFile(ctx context.Context, nodeID, appID, relPath string) error {
	cli, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return err
	}
	return cli.DeleteAppInputFile(ctx, appID, relPath)
}

// ClearAppSessions 清空节点上 apps/<appID>/.hermes/sessions/ 目录,
// 使 Hermes 重启后开新 session 时 snapshot 最新 SOUL.md。
// 调用方:配置变更类操作(改 model / persona / 知识库 / 重启)走完业务流程后
// 必须调一次,否则旧 session 的 system_prompt 已冻结、新配置不生效。
func (a *AgentBackedAdapter) ClearAppSessions(ctx context.Context, nodeID, appID string) error {
	cli, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return err
	}
	return cli.ClearAppSessions(ctx, appID)
}

func (a *AgentBackedAdapter) resolveFile(ctx context.Context, nodeID string) (*agent.AgentFileClient, error) {
	if a.files == nil {
		return nil, ErrUnimplemented
	}
	// nodeID 是文件 API 的隔离边界；resolver 必须返回该节点专属 endpoint/token 的 client。
	return a.files.FileClient(ctx, nodeID)
}

func (a *AgentBackedAdapter) dockerClient(ctx context.Context, nodeID string) (*client.Client, error) {
	if a.docker == nil {
		return nil, ErrUnimplemented
	}
	// docker client 绑定单个节点 agent 代理，不能跨节点缓存后复用。
	return a.docker.DockerClient(ctx, nodeID)
}

// streamingDockerClient 返回用于长连接 ExecAttach 的 docker client。
// 若已通过 SetStreamingDocker 注入专用 resolver 则优先使用（无 timeout），
// 否则回退到普通 docker resolver（调用方需自行保证超时配置兼容）。
func (a *AgentBackedAdapter) streamingDockerClient(ctx context.Context, nodeID string) (*client.Client, error) {
	if a.streamingDocker != nil {
		return a.streamingDocker.DockerClient(ctx, nodeID)
	}
	// 回退到普通 docker resolver；ContainerExecStream 建议通过 SetStreamingDocker 注入。
	return a.dockerClient(ctx, nodeID)
}

// translateSpec 把 ContainerSpec 翻译成 docker SDK 创建容器所需的三组配置。
//
// 翻译要点：
//   - Env 输出按 key 排序，方便审计与单元测试稳定比较；
//   - VolumeMount 以 bind mount 字符串表达（"host:container[:ro]"），与项目"禁止 named volume"约束一致；
//   - Resources.CPULimit 单位是千分之一 CPU，转换成 docker NanoCPUs（1 CPU = 1e9 NanoCPUs）；
//   - Networks 数量 ≤1 时直接挂在 HostConfig.NetworkMode；多个网络写入 NetworkingConfig 让 docker 在创建时全部连接。
func translateSpec(spec ContainerSpec) (*container.Config, *container.HostConfig, *network.NetworkingConfig) {
	containerCfg := &container.Config{
		Image:      spec.Image,
		Env:        envSlice(spec.Env),
		Cmd:        append([]string(nil), spec.Command...),
		WorkingDir: spec.WorkingDir,
	}
	hostCfg := &container.HostConfig{
		Binds: bindStrings(spec.Volumes),
		Resources: container.Resources{
			NanoCPUs: spec.Resources.CPULimit * 1_000_000,
			Memory:   spec.Resources.MemoryBytes,
		},
		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyMode(spec.RestartPolicy),
		},
	}
	if len(spec.Networks) == 1 {
		hostCfg.NetworkMode = container.NetworkMode(spec.Networks[0])
	}
	var networkCfg *network.NetworkingConfig
	if len(spec.Networks) > 1 {
		endpoints := make(map[string]*network.EndpointSettings, len(spec.Networks))
		for _, n := range spec.Networks {
			endpoints[n] = &network.EndpointSettings{}
		}
		networkCfg = &network.NetworkingConfig{EndpointsConfig: endpoints}
	}
	return containerCfg, hostCfg, networkCfg
}

// envSlice 把 map 顺序化为 docker 需要的 KEY=VALUE 列表。
func envSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// bindStrings 把 VolumeMount 翻译为 docker bind mount 表达式。
func bindStrings(volumes []VolumeMount) []string {
	if len(volumes) == 0 {
		return nil
	}
	out := make([]string, 0, len(volumes))
	for _, v := range volumes {
		entry := v.HostPath + ":" + v.ContainerPath
		if v.ReadOnly {
			entry += ":ro"
		}
		out = append(out, entry)
	}
	return out
}

// inspectToContainerInfo 把 docker SDK 的 inspect 结果裁成对外暴露的最小视图。
// Health 字段从 State.Health 映射：Status 取 docker inspect 的状态字符串，
// Output 取最近一次 HealthcheckResult 的输出，供失败时写入 health_state_json 排障。
func inspectToContainerInfo(inspect dockerInspectResponse) ContainerInfo {
	name := strings.TrimPrefix(inspect.Name, "/")
	status := ""
	if inspect.State != nil {
		status = inspect.State.Status
	}
	info := ContainerInfo{
		ID:     inspect.ID,
		Name:   name,
		Image:  inspect.Image,
		Status: status,
	}
	if inspect.State != nil && inspect.State.Health != nil {
		info.Health.Status = inspect.State.Health.Status
		if n := len(inspect.State.Health.Log); n > 0 {
			// 取最近一次 HealthcheckResult.Output（运行 healthcheck.sh 的 stdout/stderr）。
			info.Health.Output = inspect.State.Health.Log[n-1].Output
		}
	}
	return info
}

// dockerInspectResponse 是 docker SDK ContainerInspect 返回类型的别名。
// 抽出别名是为了让 inspectToContainerInfo 在不同 docker SDK 版本里保持稳定签名。
type dockerInspectResponse = dockertypes.ContainerJSON
