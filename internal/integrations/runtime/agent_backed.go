package runtime

import (
	"context"
	"io"

	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/runtime/imagesync"
)

// AgentResolver 根据 nodeID 取出 manager 需要的 agent 客户端。
// 注册流程会把 agent 的 file/docker endpoint 与 token 写入 runtime_nodes，
// 调用方需在请求时按 nodeID 取出对应客户端，否则不同节点会被错误路由。
type AgentResolver interface {
	FileClient(ctx context.Context, nodeID string) (*agent.AgentFileClient, error)
}

// ImageSyncer 是 imagesync.Service 的最小接口形态，便于在测试中替换为内存桩。
type ImageSyncer interface {
	SyncOpenClawImage(ctx context.Context, nodeID, image string) (imagesync.SyncResult, error)
}

// AgentBackedAdapter 通过 agent HTTP API 完成 runtime adapter 协议。
// 容器相关接口暂未实现 docker proxy，调用 Container* 方法会返回 ErrUnimplemented；
// 这是为了让上层 worker handler 在 task 5.3 之前能基于稳定的接口先写测试。
type AgentBackedAdapter struct {
	resolver AgentResolver
	imageSync ImageSyncer
}

// NewAgentBackedAdapter 构造 adapter。
func NewAgentBackedAdapter(resolver AgentResolver, imageSync ImageSyncer) *AgentBackedAdapter {
	return &AgentBackedAdapter{resolver: resolver, imageSync: imageSync}
}

// EnsureImage 将本地 OpenClaw 镜像分发到目标节点。
func (a *AgentBackedAdapter) EnsureImage(ctx context.Context, nodeID, image string) (imagesync.SyncResult, error) {
	if a.imageSync == nil {
		return imagesync.SyncResult{}, ErrUnimplemented
	}
	return a.imageSync.SyncOpenClawImage(ctx, nodeID, image)
}

// CreateContainer 当前依赖 agent docker proxy；尚未实现。
func (a *AgentBackedAdapter) CreateContainer(ctx context.Context, nodeID string, spec ContainerSpec) (ContainerInfo, error) {
	return ContainerInfo{}, ErrUnimplemented
}

// StartContainer 暂未实现。
func (a *AgentBackedAdapter) StartContainer(ctx context.Context, nodeID, containerID string) error {
	return ErrUnimplemented
}

// StopContainer 暂未实现。
func (a *AgentBackedAdapter) StopContainer(ctx context.Context, nodeID, containerID string) error {
	return ErrUnimplemented
}

// RestartContainer 暂未实现。
func (a *AgentBackedAdapter) RestartContainer(ctx context.Context, nodeID, containerID string) error {
	return ErrUnimplemented
}

// RemoveContainer 暂未实现。
func (a *AgentBackedAdapter) RemoveContainer(ctx context.Context, nodeID, containerID string) error {
	return ErrUnimplemented
}

// InspectContainer 暂未实现。
func (a *AgentBackedAdapter) InspectContainer(ctx context.Context, nodeID, containerID string) (ContainerInfo, error) {
	return ContainerInfo{}, ErrUnimplemented
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

func (a *AgentBackedAdapter) resolveFile(ctx context.Context, nodeID string) (*agent.AgentFileClient, error) {
	if a.resolver == nil {
		return nil, ErrUnimplemented
	}
	return a.resolver.FileClient(ctx, nodeID)
}
