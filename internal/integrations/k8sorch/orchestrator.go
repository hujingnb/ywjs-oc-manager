// Package k8sorch 提供 k8s 原生的 app 编排抽象，替代 docker 形状的 runtime.Adapter。
// app = 一个 Deployment(replicas=1, Recreate) + Service(oc-ops) + Secret(control-token)，
// manager 按 appID 确定性命名（app-<id> / app-<id>-ocops / app-<id>-token）寻址。
package k8sorch

import (
	"context"
	"time"
)

// Orchestrator 是 k8s 原生 app 编排接口。
type Orchestrator interface {
	// EnsureApp 渲染并幂等 apply Deployment + Service + Secret（create-or-update）。
	EnsureApp(ctx context.Context, spec AppSpec) error
	// WaitReady 等待 app 的 pod Ready（带 timeout）。
	WaitReady(ctx context.Context, appID string, timeout time.Duration) error
	// Scale 伸缩 replicas（0=停，1=起）。
	Scale(ctx context.Context, appID string, replicas int32) error
	// UpdateImage patch Deployment 主容器（hermes/oc-ops 同镜像）镜像，触发 Recreate 重启。
	UpdateImage(ctx context.Context, appID, hermesImage string) error
	// Delete 删除 Deployment + Service + Secret（幂等，NotFound 视为成功）。
	Delete(ctx context.Context, appID string) error
	// Status 读 app 的 pod 状态。
	Status(ctx context.Context, appID string) (AppStatus, error)
	// RolloutRestart 触发 Deployment 滚动重启（patch pod template 注解），
	// 不改镜像/副本数，按 Recreate 策略重建 pod。渠道绑定后重载 hermes platform 用。
	RolloutRestart(ctx context.Context, appID string) error
}

// AppSpec 是渲染 app pod 资源所需的全部输入（k8s 形状，非 docker ContainerSpec）。
type AppSpec struct {
	// AppID 是资源命名与 label 基准。
	AppID string
	// HermesImage 是主容器镜像（版本 image_id 解析）；oc-ops 同镜像覆盖 CMD。
	HermesImage string
	// OpsImage 是 ops 镜像（spec-A1，initContainer/sidecar）。
	OpsImage string
	// ControlToken 是 per-app control token 明文，写入 Secret。
	ControlToken string
	// BootstrapURL 是 OC_BOOTSTRAP_URL，pod 调 manager bootstrap。
	BootstrapURL string
	// ImagePullSecret 是私有镜像拉取 Secret 名。
	ImagePullSecret string
	// Resources 是 pod 资源 requests/limits。
	Resources ResourceLimits
	// Labels 是附加 label。
	Labels map[string]string
	// Proxy 为需直连外网的容器（hermes 微信平台 / oc-ops 渠道登录）注入代理 env；
	// 各字段留空则不注入对应项（生产 pod 有外网出口时全空）。
	Proxy ProxyEnv
}

// ProxyEnv 是注入容器的代理环境变量（HTTP(S)_PROXY/NO_PROXY），留空不注入。
type ProxyEnv struct {
	HTTPProxy  string
	HTTPSProxy string
	NoProxy    string
}

// ResourceLimits 是 pod 资源 requests/limits（CPU/内存 quantity 字符串）。
type ResourceLimits struct {
	RequestsCPU    string
	RequestsMemory string
	LimitsCPU      string
	LimitsMemory   string
}

// AppStatus 是 pod 状态归一视图。
type AppStatus struct {
	// Phase 是 pod 相位：Pending/Running/Succeeded/Failed/Unknown/NotFound。
	Phase string
	// Ready 表示 hermes 容器是否 Ready。
	Ready bool
	// RestartCount 是 hermes 容器重启次数。
	RestartCount int32
	// ImageRef 是当前运行的 hermes 镜像。
	ImageRef string
	// Message 是异常原因（如镜像拉取失败、CrashLoopBackOff）。
	Message string
	// Raw 是 pod.Status 序列化，存入 runtime_snapshot_json。
	Raw []byte
}
