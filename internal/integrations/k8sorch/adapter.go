package k8sorch

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"oc-manager/internal/domain"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

// KubernetesAdapter 用 client-go 实现 Orchestrator。
type KubernetesAdapter struct {
	client    kubernetes.Interface
	namespace string
	// aicc 表示该适配器专供 AICC namespace，负责管理 AICC 独有 HPA 生命周期。
	aicc            bool
	businessMetrics AICCBusinessMetricsConfig
}

// NewKubernetesAdapter 构造 adapter（client 可注入 fake 便于单测）。
func NewKubernetesAdapter(client kubernetes.Interface, namespace string) *KubernetesAdapter {
	return &KubernetesAdapter{client: client, namespace: namespace}
}

// NewAICCKubernetesAdapter 构造 AICC 专用 adapter；除基础资源外还会创建和删除 HPA。
func NewAICCKubernetesAdapter(client kubernetes.Interface, namespace string) *KubernetesAdapter {
	return &KubernetesAdapter{client: client, namespace: namespace, aicc: true}
}

// WithAICCBusinessMetrics 为 AICC adapter 注入已校验的 external metrics 合同。
// 此方法仅在 server 装配阶段调用；未调用时保持 CPU/内存 HPA，兼容没有 adapter 的集群。
func (a *KubernetesAdapter) WithAICCBusinessMetrics(metrics AICCBusinessMetricsConfig) *KubernetesAdapter {
	a.businessMetrics = metrics
	return a
}

var _ Orchestrator = (*KubernetesAdapter)(nil)

// EnsureApp 幂等 apply Secret → Service → Deployment；AICC adapter 在 Deployment 存在后再 apply HPA。
// EnsureApp 是全量 reconcile，会把 replicas 重置回 1（创建/换镜像重启用）；暂停 app 用 Scale(0) 而非 EnsureApp。
func (a *KubernetesAdapter) EnsureApp(ctx context.Context, spec AppSpec) error {
	if err := a.applySecret(ctx, RenderSecret(spec, a.namespace)); err != nil {
		return err
	}
	if err := a.applyService(ctx, RenderService(spec, a.namespace)); err != nil {
		return err
	}
	if a.aicc && domain.IsAICCAppType(spec.AppType) {
		// 在创建/更新客服 Pod 前先收敛默认拒绝 egress 策略，避免滚动更新窗口产生未受限副本。
		if err := a.applyNetworkPolicy(ctx, RenderAICCNetworkPolicy(spec, a.namespace)); err != nil {
			return err
		}
	}
	if err := a.applyDeployment(ctx, RenderDeployment(spec, a.namespace), a.aicc && domain.IsAICCAppType(spec.AppType)); err != nil {
		return err
	}
	if a.aicc && domain.IsAICCAppType(spec.AppType) {
		return a.applyHPA(ctx, RenderAICCHPA(spec, a.namespace, a.businessMetrics))
	}
	return nil
}

// applyNetworkPolicy 以 Get→Create/Update 方式幂等收敛 AICC 的逐应用 egress 策略。
func (a *KubernetesAdapter) applyNetworkPolicy(ctx context.Context, policy *networkingv1.NetworkPolicy) error {
	api := a.client.NetworkingV1().NetworkPolicies(a.namespace)
	existing, err := api.Get(ctx, policy.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, cerr := api.Create(ctx, policy, metav1.CreateOptions{})
		return wrapK8s("创建 AICC NetworkPolicy", cerr)
	}
	if err != nil {
		return wrapK8s("查询 AICC NetworkPolicy", err)
	}
	policy.ResourceVersion = existing.ResourceVersion
	_, uerr := api.Update(ctx, policy, metav1.UpdateOptions{})
	return wrapK8s("更新 AICC NetworkPolicy", uerr)
}

// applyHPA 以 Get→Create/Update 方式幂等收敛 HPA，保留 apiserver 分配的 ResourceVersion。
func (a *KubernetesAdapter) applyHPA(ctx context.Context, h *autoscalingv2.HorizontalPodAutoscaler) error {
	// 当前支持的 Kubernetes 集群只保证稳定版 autoscaling/v2；v2beta2 已在新版本中移除。
	api := a.client.AutoscalingV2().HorizontalPodAutoscalers(a.namespace)
	existing, err := api.Get(ctx, h.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, cerr := api.Create(ctx, h, metav1.CreateOptions{})
		return wrapK8s("创建 HPA", cerr)
	}
	if err != nil {
		return wrapK8s("查询 HPA", err)
	}
	h.ResourceVersion = existing.ResourceVersion
	_, uerr := api.Update(ctx, h, metav1.UpdateOptions{})
	return wrapK8s("更新 HPA", uerr)
}

// applyDeployment 全量收敛 Deployment 模板；AICC 由 HPA 管理副本数，更新时必须保留控制器当前值。
func (a *KubernetesAdapter) applyDeployment(ctx context.Context, d *appsv1.Deployment, preserveReplicas bool) error {
	api := a.client.AppsV1().Deployments(a.namespace)
	existing, err := api.Get(ctx, d.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, cerr := api.Create(ctx, d, metav1.CreateOptions{})
		return wrapK8s("创建 Deployment", cerr)
	}
	if err != nil {
		return wrapK8s("查询 Deployment", err)
	}
	d.ResourceVersion = existing.ResourceVersion
	if preserveReplicas {
		// HPA 的 scale 子资源会异步调节 replicas；业务配置变更不能抢回初始副本数。
		d.Spec.Replicas = existing.Spec.Replicas
	}
	_, uerr := api.Update(ctx, d, metav1.UpdateOptions{})
	return wrapK8s("更新 Deployment", uerr)
}

func (a *KubernetesAdapter) applyService(ctx context.Context, s *corev1.Service) error {
	api := a.client.CoreV1().Services(a.namespace)
	existing, err := api.Get(ctx, s.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, cerr := api.Create(ctx, s, metav1.CreateOptions{})
		return wrapK8s("创建 Service", cerr)
	}
	if err != nil {
		return wrapK8s("查询 Service", err)
	}
	s.ResourceVersion = existing.ResourceVersion
	s.Spec.ClusterIP = existing.Spec.ClusterIP
	_, uerr := api.Update(ctx, s, metav1.UpdateOptions{})
	return wrapK8s("更新 Service", uerr)
}

func (a *KubernetesAdapter) applySecret(ctx context.Context, s *corev1.Secret) error {
	api := a.client.CoreV1().Secrets(a.namespace)
	existing, err := api.Get(ctx, s.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, cerr := api.Create(ctx, s, metav1.CreateOptions{})
		return wrapK8s("创建 Secret", cerr)
	}
	if err != nil {
		return wrapK8s("查询 Secret", err)
	}
	s.ResourceVersion = existing.ResourceVersion
	_, uerr := api.Update(ctx, s, metav1.UpdateOptions{})
	return wrapK8s("更新 Secret", uerr)
}

// Scale 改 Deployment.Spec.Replicas。
func (a *KubernetesAdapter) Scale(ctx context.Context, appID string, replicas int32) error {
	api := a.client.AppsV1().Deployments(a.namespace)
	d, err := api.Get(ctx, deploymentName(appID), metav1.GetOptions{})
	if err != nil {
		return wrapK8s("查询 Deployment", err)
	}
	d.Spec.Replicas = &replicas
	_, uerr := api.Update(ctx, d, metav1.UpdateOptions{})
	return wrapK8s("伸缩 Deployment", uerr)
}

// Start 启动 app。先设置一个初始副本，成功后 AICC 再重建 HPA，避免缩放失败时 HPA 提前拉起实例。
func (a *KubernetesAdapter) Start(ctx context.Context, appID string) error {
	if err := a.Scale(ctx, appID, 1); err != nil {
		return err
	}
	if a.aicc {
		if err := a.applyHPA(ctx, RenderAICCHPA(AppSpec{AppID: appID}, a.namespace, a.businessMetrics)); err != nil {
			return err
		}
	}
	return nil
}

// Stop 停止 app。AICC 必须先删除 HPA，否则其 minReplicas=1 会立即撤销 Scale(0)。
func (a *KubernetesAdapter) Stop(ctx context.Context, appID string) error {
	if a.aicc {
		if err := a.deleteHPA(ctx, appID); err != nil {
			return err
		}
	}
	err := a.Scale(ctx, appID, 0)
	// Deployment 已被带外删除时仍视为停止成功；AICC HPA 已在上方清理，避免遗留控制器。
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// UpdateImage patch hermes + oc-ops 容器镜像（同镜像）。
func (a *KubernetesAdapter) UpdateImage(ctx context.Context, appID, hermesImage string) error {
	api := a.client.AppsV1().Deployments(a.namespace)
	d, err := api.Get(ctx, deploymentName(appID), metav1.GetOptions{})
	if err != nil {
		return wrapK8s("查询 Deployment", err)
	}
	for i := range d.Spec.Template.Spec.Containers {
		switch d.Spec.Template.Spec.Containers[i].Name {
		case "hermes", "oc-ops":
			d.Spec.Template.Spec.Containers[i].Image = hermesImage
		}
	}
	_, uerr := api.Update(ctx, d, metav1.UpdateOptions{})
	return wrapK8s("更新镜像", uerr)
}

// RolloutRestart 给 Deployment 的 pod template 注解写入当前时间戳，触发 Deployment 按其
// 当前策略更新 pod（普通应用为 Recreate，AICC 为 RollingUpdate，等价 kubectl rollout restart）。
// 用于渠道绑定后重载 hermes platform。
// 使用 retry.RetryOnConflict 处理 Get→Update 之间控制器并发修改导致的乐观锁冲突（409 Conflict），
// 每次重试重新 Get 最新版本再写入注解，避免 EnsureApp 后立即调用时 resourceVersion 不一致。
func (a *KubernetesAdapter) RolloutRestart(ctx context.Context, appID string) error {
	api := a.client.AppsV1().Deployments(a.namespace)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		d, err := api.Get(ctx, deploymentName(appID), metav1.GetOptions{})
		if err != nil {
			return err
		}
		if d.Spec.Template.Annotations == nil {
			d.Spec.Template.Annotations = map[string]string{}
		}
		// 写入 restartedAt 注解触发 Deployment 重建 pod，与 kubectl rollout restart 等价。
		d.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().UTC().Format(time.RFC3339)
		_, uerr := api.Update(ctx, d, metav1.UpdateOptions{})
		return uerr
	})
	return wrapK8s("滚动重启 Deployment", err)
}

// PatchSecretKeys 对 app-<id>-token Secret 增删指定 key，不动其他 key。
// set 写入/覆盖指定 key；del 删除指定 key。
// 用于渠道绑定时增加 feishu-* 凭证 key、解绑时删除这些 key，
// 同时保留 control-token 等已有 key 不受影响。
// 用 retry.RetryOnConflict 处理 Get→Update 间控制器并发修改导致的乐观锁冲突（409 Conflict），
// 每次重试重新 Get 最新版本再写入，避免并发场景下 resourceVersion 不一致导致更新失败。
func (a *KubernetesAdapter) PatchSecretKeys(ctx context.Context, appID string, set map[string]string, del []string) error {
	api := a.client.CoreV1().Secrets(a.namespace)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		s, err := api.Get(ctx, secretName(appID), metav1.GetOptions{})
		if err != nil {
			return err
		}
		if s.Data == nil {
			s.Data = map[string][]byte{}
		}
		// 写入/覆盖指定 key（绑定场景：写入飞书凭证）
		for k, v := range set {
			s.Data[k] = []byte(v)
		}
		// 删除指定 key（解绑场景：移除飞书凭证）
		for _, k := range del {
			delete(s.Data, k)
		}
		_, uerr := api.Update(ctx, s, metav1.UpdateOptions{})
		return uerr
	})
	return wrapK8s("patch Secret keys", err)
}

// Delete 删除 app 资源；AICC 专用 adapter 先删除 HPA，避免控制器继续写入正在删除的 Deployment。
func (a *KubernetesAdapter) Delete(ctx context.Context, appID string) error {
	del := metav1.DeleteOptions{}
	if a.aicc {
		if err := a.deleteHPA(ctx, appID); err != nil {
			return err
		}
		// 先以 foreground 级联删除 Deployment，并等待其 Pod 实际消失；否则异步终止窗口若
		// 先删策略会形成未受限 egress。确认没有任何同 app 标签 Pod 后才回收策略。
		if err := a.deleteAICCDeploymentAndWait(ctx, appID); err != nil {
			return err
		}
		if err := a.deleteAICCNetworkPolicy(ctx, appID); err != nil {
			return err
		}
		// Deployment 已在上方处理，下面只删除 Service 与 Secret。
		goto deleteSupportingResources
	}
	if err := a.client.AppsV1().Deployments(a.namespace).Delete(ctx, deploymentName(appID), del); err != nil && !apierrors.IsNotFound(err) {
		return wrapK8s("删除 Deployment", err)
	}

deleteSupportingResources:
	if err := a.client.CoreV1().Services(a.namespace).Delete(ctx, serviceName(appID), del); err != nil && !apierrors.IsNotFound(err) {
		return wrapK8s("删除 Service", err)
	}
	if err := a.client.CoreV1().Secrets(a.namespace).Delete(ctx, secretName(appID), del); err != nil && !apierrors.IsNotFound(err) {
		return wrapK8s("删除 Secret", err)
	}
	return nil
}

// deleteAICCDeploymentAndWait 前台删除 AICC Deployment 后持续确认同 app 标签的 Pod 已不存在。
// 不设置额外短超时，调用方 context 是唯一的生命周期边界，容纳客服 Pod 的优雅终止窗口。
func (a *KubernetesAdapter) deleteAICCDeploymentAndWait(ctx context.Context, appID string) error {
	foreground := metav1.DeletePropagationForeground
	err := a.client.AppsV1().Deployments(a.namespace).Delete(ctx, deploymentName(appID), metav1.DeleteOptions{PropagationPolicy: &foreground})
	if err != nil && !apierrors.IsNotFound(err) {
		return wrapK8s("删除 AICC Deployment", err)
	}
	return wait.PollUntilContextCancel(ctx, 250*time.Millisecond, true, func(ctx context.Context) (bool, error) {
		_, err := a.client.AppsV1().Deployments(a.namespace).Get(ctx, deploymentName(appID), metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return false, wrapK8s("确认 AICC Deployment 删除", err)
		}
		if err == nil {
			return false, nil
		}
		pods, err := a.client.CoreV1().Pods(a.namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + appID})
		if err != nil {
			return false, wrapK8s("确认 AICC Pod 删除", err)
		}
		return len(pods.Items) == 0, nil
	})
}

// deleteAICCNetworkPolicy 仅在 Deployment 与对应 Pod 都已消失后调用，回收逐应用 egress 策略。
func (a *KubernetesAdapter) deleteAICCNetworkPolicy(ctx context.Context, appID string) error {
	err := a.client.NetworkingV1().NetworkPolicies(a.namespace).Delete(ctx, aiccNetworkPolicyName(appID), metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return wrapK8s("删除 AICC NetworkPolicy", err)
}

// deleteHPA 幂等删除 AICC HPA；NotFound 表示已停止或已删除，按成功处理。
func (a *KubernetesAdapter) deleteHPA(ctx context.Context, appID string) error {
	err := a.client.AutoscalingV2().HorizontalPodAutoscalers(a.namespace).Delete(ctx, hpaName(appID), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return wrapK8s("删除 HPA", err)
	}
	return nil
}

// Status 取 app 的 pod（label app=<id>）归一为 AppStatus。
func (a *KubernetesAdapter) Status(ctx context.Context, appID string) (AppStatus, error) {
	pods, err := a.client.CoreV1().Pods(a.namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + appID})
	if err != nil {
		return AppStatus{}, wrapK8s("列举 pod", err)
	}
	if len(pods.Items) == 0 {
		// 无 pod：需区分「Deployment 不存在（app 真消失）」与「Deployment 在但 pod 尚未创建
		// （刚 EnsureApp 调度中 / Recreate 过渡）」。后者是瞬态正常，绝不能让 reconciler 误判崩溃。
		_, derr := a.client.AppsV1().Deployments(a.namespace).Get(ctx, deploymentName(appID), metav1.GetOptions{})
		if apierrors.IsNotFound(derr) {
			// Deployment 真不存在：app 已被删除 / 带外消失，下游据此判异常。
			return AppStatus{Phase: "NotFound"}, nil
		}
		if derr != nil {
			return AppStatus{}, wrapK8s("查询 Deployment", derr)
		}
		// Deployment 存在但暂无 pod：视为 Pending（pod 调度中或 Recreate 过渡 / 或被 Scale 到 0），
		// 避免 reconciler 把过渡窗口误判为崩溃。
		return AppStatus{Phase: "Pending", Message: "pod 尚未创建（调度中或 Recreate 过渡）"}, nil
	}
	// Recreate 过渡期可能短暂有 2 个 pod；取第一个（旧 Terminating 或新 Pending），状态最终一致由 reconciler 周期收敛。
	p := pods.Items[0]
	st := AppStatus{Phase: string(p.Status.Phase)}
	raw, _ := json.Marshal(p.Status)
	st.Raw = raw
	// pod 整体就绪需关键业务容器都 Ready：hermes(引擎)与 oc-ops(渠道登录/对话 API sidecar)。
	// 渠道登录实际打 oc-ops，仅 hermes Ready 会漏判 oc-ops 未起的 502 空窗。s3-sync 是数据
	// 持久化 sidecar、不在请求路径，不纳入就绪判定。RestartCount/ImageRef/Message 仍取 hermes
	// （主容器，IsTerminalBad 的重启阈值/镜像溯源沿用 hermes 口径）。
	var hermesReady, ocopsReady bool
	for _, cs := range p.Status.ContainerStatuses {
		switch cs.Name {
		case "hermes":
			hermesReady = cs.Ready
			st.RestartCount = cs.RestartCount
			st.ImageRef = cs.Image
			if cs.State.Waiting != nil {
				st.Message = cs.State.Waiting.Reason
			}
		case "oc-ops":
			ocopsReady = cs.Ready
		}
	}
	st.Ready = hermesReady && ocopsReady
	return st, nil
}

// waitReadyPollInterval 是 WaitReady 轮询 pod 状态的间隔。
var waitReadyPollInterval = 2 * time.Second

// WaitReady 轮询 Status 直到 hermes 容器 Ready、pod 进入确定性坏态、或 timeout/ctx 取消。
// 用 timeout 派生 tctx 控制硬上限，ticker 控制轮询节奏；
// Status 查询使用原始 ctx，避免 tctx 提前取消导致查询失败无法区分超时与查询错误。
//
// 关键语义：pod 在调度 / 拉镜像 / PodInitializing 等正常启动过程中**持续等待**，不因耗时
// 长而失败（镜像首拉可能数十分钟）；只有 IsTerminalBad 判定的确定坏态才提前失败。timeout
// 仅作防永久挂起的宽松硬上限。onPoll 非 nil 时每轮回调当前状态供调用方做心跳。
func (a *KubernetesAdapter) WaitReady(ctx context.Context, appID string, timeout time.Duration, onPoll func(AppStatus)) error {
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(waitReadyPollInterval)
	defer ticker.Stop()
	var phase, msg string
	for {
		st, err := a.Status(ctx, appID) // 用原 ctx 查询，tctx 仅控超时
		if err != nil {
			return err
		}
		// 心跳：每轮把当前状态回调给调用方（如 worker 刷新 app.updated_at，
		// 让 reaper 凭 updated_at 区分「worker 仍在等待」与「孤儿」）。
		if onPoll != nil {
			onPoll(st)
		}
		if st.Ready {
			return nil
		}
		// 确定性坏态立即失败，不傻等到硬上限：pod 已不可能自行恢复成 Ready。
		if IsTerminalBad(st) {
			return fmt.Errorf("等待 app %s pod Ready 失败：pod 进入坏态（phase=%s msg=%s restarts=%d）", appID, st.Phase, st.Message, st.RestartCount)
		}
		phase, msg = st.Phase, st.Message
		select {
		case <-tctx.Done():
			if ctx.Err() != nil {
				return ctx.Err() // 父 ctx 取消
			}
			// 正常启动过程耗时超过宽松硬上限才到这里（放宽后极少触发）；返回超时让上层
			// markFailed，再由 reconciler 在 pod 实际 Ready 后兜底收敛。
			return fmt.Errorf("等待 app %s pod Ready 超时（phase=%s msg=%s）", appID, phase, msg)
		case <-ticker.C:
		}
	}
}

// wrapK8s 统一包装 k8s API 错误。
func wrapK8s(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("k8sorch: %s 失败: %w", op, err)
}
