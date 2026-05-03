package runtime

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/runtime/imagesync"
)

// timeAfter / contextWithTimeout 是 healthcheck 重试用的小 helper，包级变量便于测试替换。
var (
	timeAfter = func(seconds int) <-chan time.Time {
		return time.After(time.Duration(seconds) * time.Second)
	}
	contextWithTimeout = func(parent context.Context, seconds int) (context.Context, context.CancelFunc) {
		return context.WithTimeout(parent, time.Duration(seconds)*time.Second)
	}
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

// ImageSyncer 是 imagesync.Service 的最小接口形态，便于在测试中替换为内存桩。
type ImageSyncer interface {
	SyncOpenClawImage(ctx context.Context, nodeID, image string) (imagesync.SyncResult, error)
}

// AgentBackedAdapter 通过 agent HTTP API 完成 runtime adapter 协议。
//
// 三个 resolver 可独立提供：
//   - files：缺失时文件接口返回 ErrUnimplemented，便于做"只跑容器、不跑文件"的精简部署；
//   - docker：缺失时容器接口返回 ErrUnimplemented，避免在未装配 docker proxy 时静默 panic；
//   - imageSync：缺失时 EnsureImage 返回 ErrUnimplemented。
//
// 这些缺失语义统一指向"adapter 装配不完整"，由上层启动期或 worker handler 决定如何降级。
type AgentBackedAdapter struct {
	files     AgentResolver
	docker    DockerClientResolver
	imageSync ImageSyncer
}

// NewAgentBackedAdapter 构造 adapter。
// 三个参数任意一个为 nil 时，对应能力降级为 ErrUnimplemented。
func NewAgentBackedAdapter(files AgentResolver, docker DockerClientResolver, imageSync ImageSyncer) *AgentBackedAdapter {
	return &AgentBackedAdapter{files: files, docker: docker, imageSync: imageSync}
}

// EnsureImage 将本地 OpenClaw 镜像分发到目标节点。
func (a *AgentBackedAdapter) EnsureImage(ctx context.Context, nodeID, image string) (imagesync.SyncResult, error) {
	if a.imageSync == nil {
		return imagesync.SyncResult{}, ErrUnimplemented
	}
	return a.imageSync.SyncOpenClawImage(ctx, nodeID, image)
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

// WaitForOpenClawHealthy 在 OpenClaw 容器内重试调 curl 直到 /healthz 返回 200。
//
// Sprint 0 实测：上游 OpenClaw 启动后约 10~12 秒才完成 plugin 加载并暴露 /healthz；
// 之前 plugin loading 期间 curl 返回 connection refused 或非 2xx。
//
// 重试策略：从 startWaitSeconds 开始等候首次探测，之后按 stepSeconds 递增间隔
// 直到 totalTimeout 或拿到 0 退出。每次 exec 单独 timeout 5s，避免单次 docker exec 阻塞。
//
// 返回 nil 表示 healthy；非 nil 表示在窗口内未达 healthy。该错误不视为致命，
// 调用方（如 app_initialize handler）可自行决定 retry 或推进到 binding_waiting。
func (a *AgentBackedAdapter) WaitForOpenClawHealthy(ctx context.Context, nodeID, containerID string) error {
	const (
		probeURL          = "http://127.0.0.1:18789/healthz"
		startWaitSeconds  = 8  // plugin loading 实测 ~11s，先等 8s 再开始探测
		probeStepSeconds  = 4
		probeMaxAttempts  = 10 // 8 + 4*9 = 44s 总窗口，覆盖 plugin loading 上限
		probeExecTimeoutS = 5
	)
	cli, err := a.dockerClient(ctx, nodeID)
	if err != nil {
		return err
	}

	// 等候 plugin loading 完成；ctx 取消立刻返回。
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timeAfter(startWaitSeconds):
	}

	for attempt := 0; attempt < probeMaxAttempts; attempt++ {
		probeCtx, cancel := contextWithTimeout(ctx, probeExecTimeoutS)
		exitCode, perr := execCurlExitCode(probeCtx, cli, containerID, probeURL)
		cancel()
		if perr == nil && exitCode == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeAfter(probeStepSeconds):
		}
	}
	return fmt.Errorf("OpenClaw 在 %s 内未通过 healthz 探活", containerID)
}

// execCurlExitCode 通过 docker SDK exec 在容器内跑 curl，等待退出后返回 exit code。
// 使用 -fsS（fail on HTTP error，silent，show errors）让非 2xx 响应直接 exit !=0。
func execCurlExitCode(ctx context.Context, cli *client.Client, containerID, url string) (int, error) {
	exec, err := cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          []string{"curl", "-fsS", "--max-time", "3", url},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return -1, fmt.Errorf("ContainerExecCreate 失败: %w", err)
	}
	attach, err := cli.ContainerExecAttach(ctx, exec.ID, container.ExecStartOptions{})
	if err != nil {
		return -1, fmt.Errorf("ContainerExecAttach 失败: %w", err)
	}
	defer attach.Close()
	// 排空 stream 确保命令执行结束。
	_, _ = io.Copy(io.Discard, attach.Reader)
	inspect, err := cli.ContainerExecInspect(ctx, exec.ID)
	if err != nil {
		return -1, fmt.Errorf("ContainerExecInspect 失败: %w", err)
	}
	return inspect.ExitCode, nil
}

// UploadOrgFile 把单文件上传到指定节点的组织级知识库。
func (a *AgentBackedAdapter) UploadOrgFile(ctx context.Context, nodeID, orgID, relPath string, content io.Reader) error {
	cli, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return err
	}
	return cli.UploadOrgKnowledgeFile(ctx, orgID, relPath, content)
}

// UploadAppFile 把单文件上传到指定节点的应用级知识库。
func (a *AgentBackedAdapter) UploadAppFile(ctx context.Context, nodeID, appID, relPath string, content io.Reader) error {
	cli, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return err
	}
	return cli.UploadAppKnowledgeFile(ctx, appID, relPath, content)
}

// DeleteOrgFile 删除节点上组织级知识库的指定文件 / 子目录。
func (a *AgentBackedAdapter) DeleteOrgFile(ctx context.Context, nodeID, orgID, relPath string) error {
	cli, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return err
	}
	return cli.DeleteOrgKnowledge(ctx, orgID, relPath)
}

// DeleteAppFile 删除节点上应用级知识库的指定文件 / 子目录。
func (a *AgentBackedAdapter) DeleteAppFile(ctx context.Context, nodeID, appID, relPath string) error {
	cli, err := a.resolveFile(ctx, nodeID)
	if err != nil {
		return err
	}
	return cli.DeleteAppKnowledge(ctx, appID, relPath)
}

func (a *AgentBackedAdapter) resolveFile(ctx context.Context, nodeID string) (*agent.AgentFileClient, error) {
	if a.files == nil {
		return nil, ErrUnimplemented
	}
	return a.files.FileClient(ctx, nodeID)
}

func (a *AgentBackedAdapter) dockerClient(ctx context.Context, nodeID string) (*client.Client, error) {
	if a.docker == nil {
		return nil, ErrUnimplemented
	}
	return a.docker.DockerClient(ctx, nodeID)
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
		Image: spec.Image,
		Env:   envSlice(spec.Env),
		Cmd:   append([]string(nil), spec.Command...),
	}
	hostCfg := &container.HostConfig{
		Binds: bindStrings(spec.Volumes),
		Resources: container.Resources{
			NanoCPUs: spec.Resources.CPULimit * 1_000_000,
			Memory:   spec.Resources.MemoryBytes,
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
func inspectToContainerInfo(inspect dockerInspectResponse) ContainerInfo {
	name := strings.TrimPrefix(inspect.Name, "/")
	status := ""
	if inspect.State != nil {
		status = inspect.State.Status
	}
	return ContainerInfo{
		ID:     inspect.ID,
		Name:   name,
		Image:  inspect.Image,
		Status: status,
	}
}

// dockerInspectResponse 是 docker SDK ContainerInspect 返回类型的别名。
// 抽出别名是为了让 inspectToContainerInfo 在不同 docker SDK 版本里保持稳定签名。
type dockerInspectResponse = dockertypes.ContainerJSON
