package k8sorch

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

// KubernetesAdapter 用 client-go 实现 Orchestrator。
type KubernetesAdapter struct {
	client    kubernetes.Interface
	namespace string
}

// NewKubernetesAdapter 构造 adapter（client 可注入 fake 便于单测）。
func NewKubernetesAdapter(client kubernetes.Interface, namespace string) *KubernetesAdapter {
	return &KubernetesAdapter{client: client, namespace: namespace}
}

var _ Orchestrator = (*KubernetesAdapter)(nil)

// EnsureApp 幂等 apply Secret → Service → Deployment（先建依赖后建主体）。
// EnsureApp 是全量 reconcile，会把 replicas 重置回 1（创建/换镜像重启用）；暂停 app 用 Scale(0) 而非 EnsureApp。
func (a *KubernetesAdapter) EnsureApp(ctx context.Context, spec AppSpec) error {
	if err := a.applySecret(ctx, RenderSecret(spec, a.namespace)); err != nil {
		return err
	}
	if err := a.applyService(ctx, RenderService(spec, a.namespace)); err != nil {
		return err
	}
	return a.applyDeployment(ctx, RenderDeployment(spec, a.namespace))
}

func (a *KubernetesAdapter) applyDeployment(ctx context.Context, d *appsv1.Deployment) error {
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

// RolloutRestart 给 Deployment 的 pod template 注解写入当前时间戳，触发 Deployment 按
// Recreate 策略重建 pod（等价 kubectl rollout restart）。用于渠道绑定后重载 hermes platform。
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

// Delete 删除三资源（NotFound 视为成功）。
func (a *KubernetesAdapter) Delete(ctx context.Context, appID string) error {
	del := metav1.DeleteOptions{}
	if err := a.client.AppsV1().Deployments(a.namespace).Delete(ctx, deploymentName(appID), del); err != nil && !apierrors.IsNotFound(err) {
		return wrapK8s("删除 Deployment", err)
	}
	if err := a.client.CoreV1().Services(a.namespace).Delete(ctx, serviceName(appID), del); err != nil && !apierrors.IsNotFound(err) {
		return wrapK8s("删除 Service", err)
	}
	if err := a.client.CoreV1().Secrets(a.namespace).Delete(ctx, secretName(appID), del); err != nil && !apierrors.IsNotFound(err) {
		return wrapK8s("删除 Secret", err)
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
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Name == "hermes" {
			st.Ready = cs.Ready
			st.RestartCount = cs.RestartCount
			st.ImageRef = cs.Image
			if cs.State.Waiting != nil {
				st.Message = cs.State.Waiting.Reason
			}
		}
	}
	return st, nil
}

// waitReadyPollInterval 是 WaitReady 轮询 pod 状态的间隔。
var waitReadyPollInterval = 2 * time.Second

// WaitReady 轮询 Status 直到 hermes 容器 Ready 或 timeout/ctx 取消。
// 用 timeout 派生 tctx 控制整体超时，ticker 控制轮询节奏；
// Status 查询使用原始 ctx，避免 tctx 提前取消导致查询失败无法区分超时与查询错误。
func (a *KubernetesAdapter) WaitReady(ctx context.Context, appID string, timeout time.Duration) error {
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
		if st.Ready {
			return nil
		}
		phase, msg = st.Phase, st.Message
		select {
		case <-tctx.Done():
			if ctx.Err() != nil {
				return ctx.Err() // 父 ctx 取消
			}
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
