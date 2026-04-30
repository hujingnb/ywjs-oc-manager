// Package runtime 提供 manager 通过 runtime agent 操作远程 Docker 节点的抽象。
// 当前实现仅声明对外接口和数据结构；具体能力由 task 5.3、8.1 在 worker handler 中按需扩展。
package runtime

import (
	"context"
	"errors"
	"io"

	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/runtime/imagesync"
)

// ContainerSpec 描述需要在 runtime node 上启动的容器参数。
// 仅保留 manager 当前实际使用的字段，避免一开始就泄露完整 Docker API。
type ContainerSpec struct {
	Name      string
	Image     string
	Env       map[string]string
	Volumes   []VolumeMount
	Networks  []string
	Resources Resources
	Command   []string
}

// VolumeMount 描述容器卷挂载。
// 严格使用本地 bind mount 的语义，与项目“禁止 Docker named volume”的全局约束保持一致。
type VolumeMount struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

// Resources 描述容器的资源约束。
// 0 值表示不限制；调用方需要显式表达上限，避免后续在 Docker API 层耦合。
type Resources struct {
	CPULimit    int64 // 单位：千分之一 CPU。
	MemoryBytes int64
}

// ContainerInfo 是对外暴露的容器状态视图。
type ContainerInfo struct {
	ID     string
	Name   string
	Image  string
	Status string
}

// FileEntry 与 agent.FileEntry 等价，避免对调用方暴露 agent 包。
type FileEntry = agent.FileEntry

// FileListing 同上。
type FileListing = agent.FileListing

// Adapter 是 manager 调用 runtime agent 的统一入口。
// 它封装了 Docker 容器生命周期、文件操作和镜像同步三类能力，
// 便于上层 worker handler 依赖单一接口完成 app_initialize、运行操作和工作目录代理等任务。
type Adapter interface {
	EnsureImage(ctx context.Context, nodeID, image string) (imagesync.SyncResult, error)
	CreateContainer(ctx context.Context, nodeID string, spec ContainerSpec) (ContainerInfo, error)
	StartContainer(ctx context.Context, nodeID, containerID string) error
	StopContainer(ctx context.Context, nodeID, containerID string) error
	RestartContainer(ctx context.Context, nodeID, containerID string) error
	RemoveContainer(ctx context.Context, nodeID, containerID string) error
	InspectContainer(ctx context.Context, nodeID, containerID string) (ContainerInfo, error)

	ListFiles(ctx context.Context, nodeID, remotePath string) (FileListing, error)
	UploadFile(ctx context.Context, nodeID, remotePath string, content io.Reader) error
	DownloadFile(ctx context.Context, nodeID, remotePath string) (io.ReadCloser, error)
	ArchiveDirectory(ctx context.Context, nodeID, remotePath string) (io.ReadCloser, error)
	DeletePath(ctx context.Context, nodeID, remotePath string) error
}

// ErrUnimplemented 表示当前 adapter 暂未实现该能力。
// 后续 task 实现具体 worker 时会逐步替换此错误为真正的 docker proxy 调用。
var ErrUnimplemented = errors.New("runtime adapter 当前不支持该操作")
