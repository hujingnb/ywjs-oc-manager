// Package runtime 提供 manager 通过 runtime agent 操作远程 Docker 节点的抽象。
// 当前实现仅声明对外接口和数据结构；具体能力由 task 5.3、8.1 在 worker handler 中按需扩展。
package runtime

import (
	"context"
	"errors"
	"io"
	"time"

	"oc-manager/internal/integrations/agent"
	"oc-manager/internal/runtime/imagesync"
)

// ContainerSpec 描述需要在 runtime node 上启动的容器参数。
// 仅保留 manager 当前实际使用的字段，避免一开始就泄露完整 Docker API。
type ContainerSpec struct {
	// Name 是节点内容器名，由 manager 生成并用于后续 inspect/exec 排障。
	Name string
	// Image 是已同步到目标节点的 runtime 镜像引用。
	Image string
	// Env 是写入容器环境变量的键值对，不能包含明文长期密钥。
	Env map[string]string
	// Volumes 只允许本地 bind mount，路径合法性由调用方和 agent 共同约束。
	Volumes []VolumeMount
	// Networks 是容器加入的 Docker 网络名称列表；为空时使用 docker 默认网络。
	Networks []string
	// Resources 是可选资源上限，零值表示不限制。
	Resources Resources
	// Command 覆盖镜像默认启动命令；为空时沿用镜像 ENTRYPOINT/CMD。
	Command []string
}

// VolumeMount 描述容器卷挂载。
// 严格使用本地 bind mount 的语义，与项目“禁止 Docker named volume”的全局约束保持一致。
type VolumeMount struct {
	// HostPath 是目标节点上的绝对路径，不是 manager 本机路径。
	HostPath string
	// ContainerPath 是容器内挂载点。
	ContainerPath string
	// ReadOnly 为 true 时以只读方式挂载，适合知识库主副本。
	ReadOnly bool
}

// Resources 描述容器的资源约束。
// 0 值表示不限制；调用方需要显式表达上限，避免后续在 Docker API 层耦合。
type Resources struct {
	CPULimit    int64 // 单位：千分之一 CPU。
	MemoryBytes int64
}

// ContainerHealth 是 docker container HEALTHCHECK 的快照。
type ContainerHealth struct {
	// Status 是 docker inspect 返回的健康状态："healthy" / "unhealthy" / "starting" / ""（未配置 HEALTHCHECK）。
	Status string
	// Output 是最近一次 HEALTHCHECK 命令的 stdout/stderr 截断，失败时用于写入 health_state_json 排障。
	Output string
}

// ContainerInfo 是对外暴露的容器状态视图。
type ContainerInfo struct {
	// ID 是 docker 返回的容器 ID。
	ID string
	// Name 是容器名，可能带或不带 docker API 返回的前导斜杠。
	Name string
	// Image 是容器创建时使用的镜像引用。
	Image string
	// Status 是 docker inspect 的状态字符串，如 created/running/exited。
	Status string
	// Health 反映 docker HEALTHCHECK 当前状态；未配置 HEALTHCHECK 时各字段为零值。
	Health ContainerHealth
}

// ExecResult 是 ContainerExec 返回的命令执行结果。
// Stdout 截断到 4KB 避免 worker 内存爆炸；只用于排障的健康检查响应体。
type ExecResult struct {
	// ExitCode 是容器内命令退出码；0 通常表示健康检查成功。
	ExitCode int
	// Stdout 保存截断后的输出，供失败时写入 health_state_json。
	Stdout string
}

// ContainerStats 是 RuntimeAdapter.Stats 返回的归一化指标视图。
// 单位：CPU 百分比 (0-100*核数)；内存字节；网络字节累计（容器生命周期内）。
// Manager 不做秒级速率计算，前端展示绝对值即可，趋势由前端按时间序列差分。
type ContainerStats struct {
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryUsage uint64  `json:"memory_usage_bytes"`
	MemoryLimit uint64  `json:"memory_limit_bytes"`
	// DiskReadBytes 是 Docker blkio Read 累计字节；缺失 blkio 时保持 0。
	DiskReadBytes uint64 `json:"disk_read_bytes"`
	// DiskWriteBytes 是 Docker blkio Write 累计字节；缺失 blkio 时保持 0。
	DiskWriteBytes uint64 `json:"disk_write_bytes"`
	NetworkRxBytes uint64 `json:"network_rx_bytes"`
	NetworkTxBytes uint64 `json:"network_tx_bytes"`
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
	// ContainerStats 返回容器实时资源占用快照（CPU% / 内存 / 网络字节）。
	// 实现层用 docker StatsOneShot，避免长连接占用 worker 线程。
	ContainerStats(ctx context.Context, nodeID, containerID string) (ContainerStats, error)
	// ContainerExec 在容器内执行 cmd，返回 exit code 与 stdout（截断到 4KB）。
	ContainerExec(ctx context.Context, nodeID, containerID string, cmd []string) (ExecResult, error)
	// WaitContainerHealthy 阻塞至容器 docker HEALTHCHECK 报 healthy，或超时返回错误。
	// Hermes 镜像 HEALTHCHECK 内部跑 hermes gateway status，初始 start-period 60s。
	WaitContainerHealthy(ctx context.Context, nodeID, containerID string, timeout time.Duration) error

	ListFiles(ctx context.Context, nodeID, remotePath string) (FileListing, error)
	UploadFile(ctx context.Context, nodeID, remotePath string, content io.Reader) error
	DownloadFile(ctx context.Context, nodeID, remotePath string) (io.ReadCloser, error)
	ArchiveDirectory(ctx context.Context, nodeID, remotePath string) (io.ReadCloser, error)
	DeletePath(ctx context.Context, nodeID, remotePath string) error

	// 以下 scope-aware 方法直接对应 agent /v1/scopes/* 端点（Sprint 1 起就位）。
	// 与 generic 方法不同，调用方传业务标识（appID）与相对路径，由 adapter / agent 内部
	// 拼成最终路径，避免两端业务逻辑不一致。
	ListWorkspace(ctx context.Context, nodeID, appID, relPath string) (WorkspaceListing, error)
	DownloadWorkspaceFile(ctx context.Context, nodeID, appID, relPath string) (io.ReadCloser, error)
	StreamWorkspaceArchive(ctx context.Context, nodeID, appID, relPath string, w io.Writer) error
	ArchiveApp(ctx context.Context, nodeID, appID string) error
}

// WorkspaceListing 是 ListWorkspace 的标准化响应（agent /v1/scopes/.../workspace 输出）。
type WorkspaceListing struct {
	Path    string           `json:"path"`
	Entries []WorkspaceEntry `json:"entries"`
}

// WorkspaceEntry 描述 workspace 下的一个 entry（与 agent 端字段对齐）。
type WorkspaceEntry struct {
	Name       string `json:"name"`
	Type       string `json:"type"` // file | dir
	Size       int64  `json:"size,omitempty"`
	ModifiedAt string `json:"modified_at"`
}

// ErrUnimplemented 表示当前 adapter 暂未实现该能力。
// 后续 task 实现具体 worker 时会逐步替换此错误为真正的 docker proxy 调用。
var ErrUnimplemented = errors.New("runtime adapter 当前不支持该操作")
