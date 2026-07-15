package k8sorch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"oc-manager/internal/domain"
)

// TestEnsureAppCreatesResources 验证 EnsureApp 在空集群创建 Deployment/Service/Secret。
func TestEnsureAppCreatesResources(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	require.NoError(t, a.EnsureApp(context.Background(), testSpec()))
	_, err := cs.AppsV1().Deployments("oc-apps").Get(context.Background(), "app-a1", metav1.GetOptions{})
	require.NoError(t, err)
	_, err = cs.CoreV1().Services("oc-apps").Get(context.Background(), "app-a1-ocops", metav1.GetOptions{})
	require.NoError(t, err)
	sec, err := cs.CoreV1().Secrets("oc-apps").Get(context.Background(), "app-a1-token", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "tok", sec.StringData["control-token"])
}

// TestEnsureAppIdempotent 验证重复 EnsureApp 更新而非报已存在。
func TestEnsureAppIdempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	require.NoError(t, a.EnsureApp(context.Background(), testSpec()))
	require.NoError(t, a.EnsureApp(context.Background(), testSpec()))
}

// TestEnsureAppAICCCreatesHPA 验证 AICC 适配器会创建指向应用 Deployment 的 HPA，普通应用不创建。
func TestEnsureAppAICCCreatesHPA(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewAICCKubernetesAdapter(cs, "oc-aicc")
	aicc := testSpec()
	aicc.AppType = domain.AppTypeAICC
	require.NoError(t, a.EnsureApp(context.Background(), aicc))
	_, err := cs.NetworkingV1().NetworkPolicies("oc-aicc").Get(context.Background(), "app-a1-egress", metav1.GetOptions{})
	require.NoError(t, err)
	// 模拟 HPA 已将 Deployment 扩容；后续业务 reconcile 不得把副本数强制写回初始值 1。
	require.NoError(t, a.Scale(context.Background(), aicc.AppID, 3))
	// 重复 reconcile 应走 Update 路径并保持幂等，避免 worker 重试时因 HPA 已存在而失败。
	require.NoError(t, a.EnsureApp(context.Background(), aicc))

	hpa, err := cs.AutoscalingV2().HorizontalPodAutoscalers("oc-aicc").Get(context.Background(), "app-a1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "app-a1", hpa.Spec.ScaleTargetRef.Name)
	dep, err := cs.AppsV1().Deployments("oc-aicc").Get(context.Background(), "app-a1", metav1.GetOptions{})
	require.NoError(t, err)
	require.NotNil(t, dep.Spec.Replicas)
	assert.Equal(t, int32(3), *dep.Spec.Replicas)
	normal := NewKubernetesAdapter(cs, "oc-apps")
	standard := testSpec()
	standard.AppID = "normal"
	require.NoError(t, normal.EnsureApp(context.Background(), standard))
	_, err = cs.AutoscalingV2().HorizontalPodAutoscalers("oc-apps").Get(context.Background(), "app-normal", metav1.GetOptions{})
	require.Error(t, err)
	_, err = cs.AutoscalingV2().HorizontalPodAutoscalers("oc-apps").Get(context.Background(), "app-a1", metav1.GetOptions{})
	require.Error(t, err)
}

// TestDeleteAICCDeletesHPA 验证删除 AICC 应用时 HPA 一并删除，避免遗留控制器继续调节已删除 Deployment。
func TestDeleteAICCDeletesHPA(t *testing.T) {
	cs := fake.NewSimpleClientset(&autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: "app-a1", Namespace: "oc-aicc"}})
	a := NewAICCKubernetesAdapter(cs, "oc-aicc")

	require.NoError(t, a.Delete(context.Background(), "a1"))
	_, err := cs.AutoscalingV2().HorizontalPodAutoscalers("oc-aicc").Get(context.Background(), "app-a1", metav1.GetOptions{})
	require.Error(t, err)
}

// TestDeleteAICCDeletesNetworkPolicyAfterPodReclaimed 验证 AICC 的 Deployment 与 Pod 已回收后，
// 专属 egress 策略会被删除，避免 app ID 长期积累不可审计的孤儿 NetworkPolicy。
func TestDeleteAICCDeletesNetworkPolicyAfterPodReclaimed(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewAICCKubernetesAdapter(cs, "oc-aicc")
	spec := testSpec()
	spec.AppType = domain.AppTypeAICC
	require.NoError(t, a.EnsureApp(context.Background(), spec))

	require.NoError(t, a.Delete(context.Background(), spec.AppID))
	_, err := cs.NetworkingV1().NetworkPolicies("oc-aicc").Get(context.Background(), "app-a1-egress", metav1.GetOptions{})
	require.Error(t, err)
}

// TestStartStopNormalAppKeepsLegacyScaleSemantics 验证普通应用停止和启动仅缩放 Deployment，
// 不创建 AICC 专属 HPA，保持既有运行时操作语义。
func TestStartStopNormalAppKeepsLegacyScaleSemantics(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	require.NoError(t, a.EnsureApp(context.Background(), testSpec()))

	require.NoError(t, a.Stop(context.Background(), "a1"))
	dep, err := cs.AppsV1().Deployments("oc-apps").Get(context.Background(), "app-a1", metav1.GetOptions{})
	require.NoError(t, err)
	require.NotNil(t, dep.Spec.Replicas)
	assert.Equal(t, int32(0), *dep.Spec.Replicas)
	_, err = cs.AutoscalingV2().HorizontalPodAutoscalers("oc-apps").Get(context.Background(), "app-a1", metav1.GetOptions{})
	require.Error(t, err)

	require.NoError(t, a.Start(context.Background(), "a1"))
	dep, err = cs.AppsV1().Deployments("oc-apps").Get(context.Background(), "app-a1", metav1.GetOptions{})
	require.NoError(t, err)
	require.NotNil(t, dep.Spec.Replicas)
	assert.Equal(t, int32(1), *dep.Spec.Replicas)
}

// TestEnsureAppAICCRestoresHPAAfterStop 验证 AICC 停止后因重试或初始化 reconcile 再次 Ensure 时，
// HPA 会以 minReplicas=1 恢复，重新接管后续弹性伸缩。
func TestEnsureAppAICCRestoresHPAAfterStop(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewAICCKubernetesAdapter(cs, "oc-aicc")
	spec := testSpec()
	spec.AppType = domain.AppTypeAICC
	require.NoError(t, a.EnsureApp(context.Background(), spec))
	require.NoError(t, a.Stop(context.Background(), spec.AppID))

	require.NoError(t, a.EnsureApp(context.Background(), spec))
	hpa, err := cs.AutoscalingV2().HorizontalPodAutoscalers("oc-aicc").Get(context.Background(), "app-a1", metav1.GetOptions{})
	require.NoError(t, err)
	require.NotNil(t, hpa.Spec.MinReplicas)
	assert.Equal(t, int32(1), *hpa.Spec.MinReplicas)
}

// TestStopAICCDeletesHPAWhenDeploymentAlreadyMissing 验证 Deployment 被带外删除时，
// Stop 仍幂等清理残留 HPA，避免 minReplicas 控制器在后续资源恢复后意外拉起应用。
func TestStopAICCDeletesHPAWhenDeploymentAlreadyMissing(t *testing.T) {
	cs := fake.NewSimpleClientset(&autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: "app-a1", Namespace: "oc-aicc"}})
	a := NewAICCKubernetesAdapter(cs, "oc-aicc")

	require.NoError(t, a.Stop(context.Background(), "a1"))
	_, err := cs.AutoscalingV2().HorizontalPodAutoscalers("oc-aicc").Get(context.Background(), "app-a1", metav1.GetOptions{})
	require.Error(t, err)
}

// TestScale 验证 Scale 改 replicas。
func TestScale(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	require.NoError(t, a.EnsureApp(context.Background(), testSpec()))
	require.NoError(t, a.Scale(context.Background(), "a1", 0))
	d, _ := cs.AppsV1().Deployments("oc-apps").Get(context.Background(), "app-a1", metav1.GetOptions{})
	assert.Equal(t, int32(0), *d.Spec.Replicas)
}

// TestUpdateImage 验证 UpdateImage patch hermes/oc-ops 镜像。
func TestUpdateImage(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	require.NoError(t, a.EnsureApp(context.Background(), testSpec()))
	require.NoError(t, a.UpdateImage(context.Background(), "a1", "registry/hermes:v2"))
	d, _ := cs.AppsV1().Deployments("oc-apps").Get(context.Background(), "app-a1", metav1.GetOptions{})
	for _, c := range d.Spec.Template.Spec.Containers {
		if c.Name == "hermes" || c.Name == "oc-ops" {
			assert.Equal(t, "registry/hermes:v2", c.Image)
		}
	}
}

// TestDeleteIdempotent 验证 Delete 幂等（不存在不报错）。
func TestDeleteIdempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	require.NoError(t, a.Delete(context.Background(), "nonexist"))
}

// TestStatusNotFound 验证 Deployment 和 pod 均不存在时 Status 返回 NotFound。
// 覆盖「app 已被带外删除 / 真消失」路径：fake 集群中既无 Deployment 也无 pod，
// 期望 Phase=="NotFound"，让 reconciler 据此判断异常。
func TestStatusNotFound(t *testing.T) {
	// 空集群：Deployment 和 pod 都不存在，模拟 app 真消失的场景。
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	st, err := a.Status(context.Background(), "a1")
	require.NoError(t, err)
	// Deployment 不存在 + 无 pod → 真消失，应返回 NotFound。
	assert.Equal(t, "NotFound", st.Phase)
}

// TestStatusPendingWhenDeploymentExistsNoPod 验证 Deployment 存在但 pod 尚未创建时
// Status 返回 Pending 而非 NotFound，防止 reconciler 在 Recreate 过渡窗口误判崩溃。
// 回归保护场景：EnsureApp 刚完成 / UpdateImage 触发 Recreate 过渡期，
// 旧 pod 已停而新 pod 尚未被 ReplicaSet 调度起来——这是瞬态正常，绝不能误标 error。
func TestStatusPendingWhenDeploymentExistsNoPod(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	// EnsureApp 建出 Deployment，但 fake 集群不会自动创建 pod（无控制器模拟）。
	require.NoError(t, a.EnsureApp(context.Background(), testSpec()))

	st, err := a.Status(context.Background(), "a1")
	require.NoError(t, err)
	// Deployment 存在 + 无 pod → 调度过渡中，应返回 Pending 而非 NotFound，
	// 确保 reconciler 的 podIsBad("Pending")==false，不会误把过渡窗口标为崩溃。
	assert.Equal(t, "Pending", st.Phase, "Deployment 存在但无 pod 时应返回 Pending，而非 NotFound")
	assert.NotEqual(t, "NotFound", st.Phase, "过渡窗口不得被误判为 app 消失")
	assert.False(t, st.Ready, "pod 未起时 Ready 应为 false")
}

// TestStatusReadyFromPod 验证 hermes 与 oc-ops 均 Ready 的 pod 归一为 Ready。
// 两个关键业务容器都就绪才代表实例可对外服务（hermes=引擎，oc-ops=渠道登录/对话 sidecar）。
func TestStatusReadyFromPod(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app-a1-x", Namespace: "oc-apps", Labels: map[string]string{"app": "a1"}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
			{Name: "hermes", Ready: true, Image: "registry/hermes:v1"},
			{Name: "oc-ops", Ready: true}, // oc-ops 也 Ready，pod 才真正可对外服务
		}},
	})
	a := NewKubernetesAdapter(cs, "oc-apps")
	st, err := a.Status(context.Background(), "a1")
	require.NoError(t, err)
	assert.True(t, st.Ready)
	assert.Equal(t, "Running", st.Phase)
	assert.Equal(t, "registry/hermes:v1", st.ImageRef)
	assert.Equal(t, int32(0), st.RestartCount)
}

// TestStatusWaitingReason 验证 hermes 容器 Waiting 时 Message 取 Reason、Ready=false。
func TestStatusWaitingReason(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app-a1-x", Namespace: "oc-apps", Labels: map[string]string{"app": "a1"}},
		Status: corev1.PodStatus{Phase: corev1.PodPending, ContainerStatuses: []corev1.ContainerStatus{
			{Name: "hermes", Ready: false, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}},
		}},
	})
	a := NewKubernetesAdapter(cs, "oc-apps")
	st, err := a.Status(context.Background(), "a1")
	require.NoError(t, err)
	assert.False(t, st.Ready)
	assert.Equal(t, "CrashLoopBackOff", st.Message)
}

// TestWaitReadyFailsFastOnTerminalBad 验证 pod 进入确定坏态（这里空集群 → NotFound）时
// WaitReady 快速失败，而非傻等到 timeout——给 10s timeout 但应几乎立即返回。
func TestWaitReadyFailsFastOnTerminalBad(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	start := time.Now()
	err := a.WaitReady(context.Background(), "a1", 10*time.Second, nil)
	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second, "确定坏态应快速失败，不等满 timeout")
}

// TestWaitReadySucceedsAndHeartbeats 验证 pod Ready 时 WaitReady 成功返回，且每轮回调 onPoll（心跳）。
// hermes 与 oc-ops 均 Ready，模拟真正可对外服务的 pod，WaitReady 应立即成功。
func TestWaitReadySucceedsAndHeartbeats(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app-a1-x", Namespace: "oc-apps", Labels: map[string]string{"app": "a1"}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
			{Name: "hermes", Ready: true},
			{Name: "oc-ops", Ready: true}, // oc-ops 也 Ready，pod 整体就绪 WaitReady 才能成功
		}},
	})
	a := NewKubernetesAdapter(cs, "oc-apps")
	polls := 0
	err := a.WaitReady(context.Background(), "a1", time.Second, func(AppStatus) { polls++ })
	require.NoError(t, err)
	assert.GreaterOrEqual(t, polls, 1, "onPoll 心跳至少被回调一次")
}

// TestWaitReadyPendingDoesNotFailFast 验证 pod 处于 PodInitializing（拉镜像/调度，正常瞬态）时
// WaitReady 不快速失败、而是持续等待：用短 timeout 让其因硬上限超时，证明它在等而非立即判坏态返回。
func TestWaitReadyPendingDoesNotFailFast(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app-a1-x", Namespace: "oc-apps", Labels: map[string]string{"app": "a1"}},
		Status: corev1.PodStatus{Phase: corev1.PodPending, ContainerStatuses: []corev1.ContainerStatus{
			{Name: "hermes", Ready: false, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "PodInitializing"}}},
		}},
	})
	a := NewKubernetesAdapter(cs, "oc-apps")
	err := a.WaitReady(context.Background(), "a1", 100*time.Millisecond, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "超时", "PodInitializing 不算坏态，应一直等到 timeout 才超时")
}

// TestIsTerminalBad table-driven 覆盖确定坏态判定的各分支——WaitReady 与 reconciler 共用此口径。
func TestIsTerminalBad(t *testing.T) {
	cases := []struct {
		name string
		st   AppStatus
		want bool
	}{
		{"Ready 正常", AppStatus{Phase: "Running", Ready: true}, false},                        // 就绪：非坏态
		{"Pending 拉镜像瞬态", AppStatus{Phase: "Pending", Message: "PodInitializing"}, false},    // 启动瞬态：非坏态
		{"NotFound 真消失", AppStatus{Phase: "NotFound"}, true},                                 // pod/Deployment 消失：坏态
		{"Failed 终相", AppStatus{Phase: "Failed"}, true},                                      // Failed：坏态
		{"CrashLoopBackOff", AppStatus{Phase: "Running", Message: "CrashLoopBackOff"}, true}, // 反复崩溃：坏态
		{"重启达阈值", AppStatus{Phase: "Running", RestartCount: 3}, true},                        // 重启 >=3：坏态
		{"重启未达阈值", AppStatus{Phase: "Running", RestartCount: 2}, false},                      // 重启 <3：非坏态
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsTerminalBad(tc.st))
		})
	}
}

// TestStatus_RequiresBothHermesAndOcops 验证：pod 整体就绪需 hermes 与 oc-ops 容器都 Ready。
// 渠道登录/对话实际打 oc-ops sidecar，仅 hermes Ready 不代表服务可用（oc-ops 未起仍会 502）。
func TestStatus_RequiresBothHermesAndOcops(t *testing.T) {
	// Case A：hermes Ready、oc-ops 未就绪 → pod 整体不可用，st.Ready 应为 false。
	t.Run("hermes_ready_ocops_not_ready", func(t *testing.T) {
		cs := fake.NewSimpleClientset(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "app-a1-x", Namespace: "oc-apps", Labels: map[string]string{"app": "a1"}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
				{Name: "hermes", Ready: true, Image: "registry/hermes:v1"},
				{Name: "oc-ops", Ready: false}, // oc-ops 未就绪，渠道登录/对话会 502
			}},
		})
		a := NewKubernetesAdapter(cs, "oc-apps")
		st, err := a.Status(context.Background(), "a1")
		require.NoError(t, err)
		// oc-ops 未 Ready → pod 整体不可对外服务，Ready 必须为 false
		assert.False(t, st.Ready, "oc-ops 未 Ready 时 pod 整体不应标 Ready")
	})

	// Case B：hermes 与 oc-ops 均 Ready → pod 完全就绪，st.Ready 应为 true。
	t.Run("both_hermes_and_ocops_ready", func(t *testing.T) {
		cs := fake.NewSimpleClientset(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "app-a1-x", Namespace: "oc-apps", Labels: map[string]string{"app": "a1"}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
				{Name: "hermes", Ready: true, Image: "registry/hermes:v1"},
				{Name: "oc-ops", Ready: true}, // 两个关键容器都 Ready
			}},
		})
		a := NewKubernetesAdapter(cs, "oc-apps")
		st, err := a.Status(context.Background(), "a1")
		require.NoError(t, err)
		// hermes 与 oc-ops 均 Ready → pod 可对外服务，Ready 应为 true
		assert.True(t, st.Ready, "hermes 和 oc-ops 均 Ready 时 pod 应标 Ready")
	})
}

// TestPatchSecretKeysSetAndDelete 验证按 key 增删 Secret 不影响其他 key（control-token 保留）。
// 覆盖飞书渠道绑定（写入 feishu-* key）与解绑（删除 feishu-* key）两条路径，
// 确保操作不会覆盖 Secret 中已有的 control-token 等无关 key。
func TestPatchSecretKeysSetAndDelete(t *testing.T) {
	// 预置已有 control-token 的 Secret，模拟 app 已创建后的真实状态。
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName("app-1"), Namespace: "oc-apps"},
		Data:       map[string][]byte{"control-token": []byte("tok")},
	})
	a := &KubernetesAdapter{client: client, namespace: "oc-apps"}

	// 增三个飞书 key（渠道绑定场景）：set 写入、del 为 nil。
	err := a.PatchSecretKeys(context.Background(), "app-1",
		map[string]string{"feishu-app-id": "cli_x", "feishu-app-secret": "sec", "feishu-domain": "feishu"}, nil)
	require.NoError(t, err)
	got, _ := client.CoreV1().Secrets("oc-apps").Get(context.Background(), secretName("app-1"), metav1.GetOptions{})
	// 新 key 应写入
	require.Equal(t, "cli_x", string(got.Data["feishu-app-id"]))
	// control-token 不应被动：PatchSecretKeys 只改指定 key，不替换整个 Secret
	require.Equal(t, "tok", string(got.Data["control-token"]), "control-token 不应被动")

	// 删三个飞书 key（渠道解绑场景）：set 为 nil、del 传 key 列表。
	require.NoError(t, a.PatchSecretKeys(context.Background(), "app-1", nil,
		[]string{"feishu-app-id", "feishu-app-secret", "feishu-domain"}))
	got2, _ := client.CoreV1().Secrets("oc-apps").Get(context.Background(), secretName("app-1"), metav1.GetOptions{})
	// 飞书 key 应被删除
	_, ok := got2.Data["feishu-app-id"]
	require.False(t, ok)
	// control-token 仍保留
	require.Equal(t, "tok", string(got2.Data["control-token"]))
}

// TestRolloutRestartPatchesAnnotation 验证 RolloutRestart 给 pod template 写入 restartedAt 注解、不动镜像/副本。
// 渠道绑定后重载 hermes platform 的等价 kubectl rollout restart 路径。
func TestRolloutRestartPatchesAnnotation(t *testing.T) {
	cs := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(cs, "oc-apps")
	// 先建立 Deployment（replicas=1）
	require.NoError(t, a.EnsureApp(context.Background(), testSpec()))
	// 执行滚动重启
	require.NoError(t, a.RolloutRestart(context.Background(), "a1"))
	d, err := cs.AppsV1().Deployments("oc-apps").Get(context.Background(), "app-a1", metav1.GetOptions{})
	require.NoError(t, err)
	// pod template 注解应含 restartedAt，触发 Deployment 重建 pod
	assert.NotEmpty(t, d.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"])
	// 副本数不变（仍为 1），RolloutRestart 不改 replicas
	require.NotNil(t, d.Spec.Replicas)
	assert.Equal(t, int32(1), *d.Spec.Replicas)
}
