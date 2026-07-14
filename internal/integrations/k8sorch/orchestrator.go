// Package k8sorch 提供 k8s 原生的 app 编排抽象，替代 docker 形状的 runtime.Adapter。
// 普通 app = 一个 Deployment(replicas=1, Recreate) + Service(oc-ops) + Secret(control-token)；
// AICC 额外有 HPA，并使用可用性优先的滚动更新。manager 按 appID 确定性命名寻址。
package k8sorch

import (
	"context"
	"strings"
	"time"

	"oc-manager/internal/domain"
)

// Orchestrator 是 k8s 原生 app 编排接口。
type Orchestrator interface {
	// EnsureApp 渲染并幂等 apply Deployment + Service + Secret（create-or-update）。
	EnsureApp(ctx context.Context, spec AppSpec) error
	// WaitReady 等待 app 的 pod Ready（timeout 仅作防永久挂起的宽松硬上限）。pod 在调度 /
	// 拉镜像 / PodInitializing 等正常启动过程中持续等待，不因耗时长而失败；仅当 pod 进入
	// 确定性坏态（见 IsTerminalBad）才快速失败返回 error。onPoll 非 nil 时每轮轮询回调当前
	// 状态，供调用方做心跳（如刷新 app.updated_at，让 reaper 区分"在等"与"孤儿"）；可为 nil。
	WaitReady(ctx context.Context, appID string, timeout time.Duration, onPoll func(AppStatus)) error
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
	// AppType 保留数据库应用类型，仅用于选择独立 namespace，不写入 Pod。
	AppType domain.AppType
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
	// FeishuAppID 是飞书应用 App ID（明文，未绑定为空）。
	FeishuAppID string
	// FeishuAppSecret 是飞书 App Secret 明文（buildAppSpec 从 DB 密文解密后填入，引擎需明文；未绑定为空）。
	FeishuAppSecret string
	// FeishuDomain 是飞书 domain：feishu（国内）/ lark（国际），未绑定为空。
	FeishuDomain string
	// WorkWeChatBotID 是企业微信智能机器人 Bot ID（明文，未绑定为空）。
	WorkWeChatBotID string
	// WorkWeChatSecret 是企业微信机器人 Secret 明文（buildAppSpec 从 DB 密文解密后填入，引擎需明文；未绑定为空）。
	WorkWeChatSecret string
	// DingtalkClientID 是钉钉应用 Client ID（即 AppKey，明文，未绑定为空）。
	DingtalkClientID string
	// DingtalkClientSecret 是钉钉 Client Secret 明文（即 AppSecret，buildAppSpec 从 DB 密文解密后填入，引擎需明文；未绑定为空）。
	DingtalkClientSecret string
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
	// Ready 表示 hermes 且 oc-ops 容器都 Ready（pod 可对外服务）。
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

// terminalRestartThreshold 是判定 hermes 容器「反复崩溃」的重启次数阈值。
// 启动过程中的偶发单次重启不算坏态，累计达到该次数才视为确定性失败。
const terminalRestartThreshold = 3

// IsTerminalBad 判断 pod 是否处于确定性坏态——即 k8s 不会自动让它恢复成 Ready 的状态，
// 应快速失败 / 推 error；而非启动过程中的正常瞬态（调度中、拉镜像、PodInitializing）。
// WaitReady 用它决定是否提前失败，AppStatusReconciler 用它决定 running→error，
// 两处共用同一口径，避免「一处认为坏、另一处认为正常」的判定漂移。
func IsTerminalBad(st AppStatus) bool {
	// Deployment/pod 真消失或 pod 进入 Failed 终相：k8s 不会再把它拉回 Ready。
	if st.Phase == "NotFound" || st.Phase == "Failed" {
		return true
	}
	// 容器反复崩溃退避：业务可见的持续异常，不是启动瞬态。
	if strings.Contains(st.Message, "CrashLoopBackOff") {
		return true
	}
	// hermes 容器重启次数达阈值：同样是反复崩溃的确定信号。
	if st.RestartCount >= terminalRestartThreshold {
		return true
	}
	return false
}
