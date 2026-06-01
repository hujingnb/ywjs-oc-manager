package k8sorch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
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

// TestStatusReadyFromPod 验证有 Ready hermes 容器的 pod 归一为 Ready。
func TestStatusReadyFromPod(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app-a1-x", Namespace: "oc-apps", Labels: map[string]string{"app": "a1"}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
			{Name: "hermes", Ready: true, Image: "registry/hermes:v1"},
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
func TestWaitReadySucceedsAndHeartbeats(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app-a1-x", Namespace: "oc-apps", Labels: map[string]string{"app": "a1"}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
			{Name: "hermes", Ready: true},
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
		{"Pending 拉镜像瞬态", AppStatus{Phase: "Pending", Message: "PodInitializing"}, false},     // 启动瞬态：非坏态
		{"NotFound 真消失", AppStatus{Phase: "NotFound"}, true},                                 // pod/Deployment 消失：坏态
		{"Failed 终相", AppStatus{Phase: "Failed"}, true},                                      // Failed：坏态
		{"CrashLoopBackOff", AppStatus{Phase: "Running", Message: "CrashLoopBackOff"}, true},   // 反复崩溃：坏态
		{"重启达阈值", AppStatus{Phase: "Running", RestartCount: 3}, true},                        // 重启 >=3：坏态
		{"重启未达阈值", AppStatus{Phase: "Running", RestartCount: 2}, false},                      // 重启 <3：非坏态
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsTerminalBad(tc.st))
		})
	}
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
