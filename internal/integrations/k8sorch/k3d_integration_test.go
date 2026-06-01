// Package k8sorch_test 包含 KubernetesAdapter 对真实 k8s API 的集成测试。
// 所有测试均被环境变量门控：OC_K8S_TEST_KUBECONFIG 缺失则 Skip，不影响 CI 单元测试。
package k8sorch_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"oc-manager/internal/integrations/k8sorch"
)

// k3dEnv 读取环境变量构造测试用 adapter 与 clientset，缺少 kubeconfig 即 Skip。
// 返回 adapter、原始 clientset（用于硬断言资源）、目标 namespace。
func k3dEnv(t *testing.T) (adapter *k8sorch.KubernetesAdapter, cs kubernetes.Interface, ns string) {
	t.Helper()

	// OC_K8S_TEST_KUBECONFIG 缺失则跳过所有 k3d 集成测，避免影响无 k8s 环境的 CI。
	kubeconfig := os.Getenv("OC_K8S_TEST_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("未设置 OC_K8S_TEST_KUBECONFIG，跳过 k3d 编排集成测")
	}

	// OC_K8S_TEST_NS 未设置则回退到默认 namespace oc-apps。
	ns = os.Getenv("OC_K8S_TEST_NS")
	if ns == "" {
		ns = "oc-apps"
	}

	var err error
	cs, err = k8sorch.NewClientset(kubeconfig)
	require.NoError(t, err, "NewClientset 应能成功解析 kubeconfig")

	adapter = k8sorch.NewKubernetesAdapter(cs, ns)
	return adapter, cs, ns
}

// testImages 从环境变量读取测试镜像，缺失时回退 busybox 占位镜像。
// 集成测核心是验证资源创建正确性，而非 pod Ready，故占位镜像满足需求。
func testImages() (hermesImage, opsImage string) {
	hermesImage = os.Getenv("OC_K8S_TEST_HERMES_IMAGE")
	if hermesImage == "" {
		hermesImage = "busybox:latest"
	}
	opsImage = os.Getenv("OC_K8S_TEST_OPS_IMAGE")
	if opsImage == "" {
		opsImage = "busybox:latest"
	}
	return hermesImage, opsImage
}

// testAppID 生成稳定唯一的测试 appID，使用固定前缀 + USER 避免与真实 app 命名冲突。
func testAppID(prefix string) string {
	user := os.Getenv("USER")
	if user == "" {
		user = "ci"
	}
	return prefix + user
}

// containsName 检查容器名列表中是否包含指定名称。
func containsName(names []string, target string) bool {
	for _, n := range names {
		if n == target {
			return true
		}
	}
	return false
}

// TestK3dEnsureAppCreatesResources 验证 EnsureApp 调用真实 k8s API 后，
// Deployment/Service/Secret 资源均被正确创建，且资源结构（replicas、容器数、端口、Secret key）符合规范。
func TestK3dEnsureAppCreatesResources(t *testing.T) {
	// 构造 adapter 与 clientset，缺少 kubeconfig 则 Skip
	adapter, cs, ns := k3dEnv(t)
	ctx := context.Background()

	hermesImage, opsImage := testImages()
	id := testAppID("it-a2a-")

	spec := k8sorch.AppSpec{
		AppID:           id,
		HermesImage:     hermesImage,
		OpsImage:        opsImage,
		ControlToken:    "test-control-token",
		BootstrapURL:    "http://ocm.localhost/api/v1/bootstrap",
		ImagePullSecret: "acr-pull",
		Resources: k8sorch.ResourceLimits{
			RequestsCPU:    "100m",
			RequestsMemory: "256Mi",
			LimitsCPU:      "1",
			LimitsMemory:   "1Gi",
		},
	}

	// 先注册 Cleanup，确保无论测试成功或失败都清理资源，防止污染 k3d 集群。
	t.Cleanup(func() {
		_ = adapter.Delete(context.Background(), id)
	})

	// EnsureApp 调用真实 k8s API 创建三资源，必须无错误
	require.NoError(t, adapter.EnsureApp(ctx, spec), "EnsureApp 应成功创建 Deployment/Service/Secret")

	// ---- 硬断言 Deployment ----
	// 验证 Deployment app-<id> 被创建，且 replicas/容器结构符合规范
	depName := "app-" + id
	dep, err := cs.AppsV1().Deployments(ns).Get(ctx, depName, metav1.GetOptions{})
	require.NoError(t, err, "Deployment %s 应存在", depName)

	// replicas 必须为 1（EnsureApp 硬编码 replicas=1）
	require.NotNil(t, dep.Spec.Replicas, "Deployment.Spec.Replicas 不应为 nil")
	assert.Equal(t, int32(1), *dep.Spec.Replicas, "Deployment replicas 应为 1")

	// 普通容器必须恰好 3 个（hermes / oc-ops / s3-sync），顺序不保证，按名断言
	containers := dep.Spec.Template.Spec.Containers
	assert.Len(t, containers, 3, "Deployment 应有 3 个普通容器（hermes/oc-ops/s3-sync）")
	containerNames := make([]string, 0, len(containers))
	for _, c := range containers {
		containerNames = append(containerNames, c.Name)
	}
	assert.True(t, containsName(containerNames, "hermes"), "普通容器应包含 hermes")
	assert.True(t, containsName(containerNames, "oc-ops"), "普通容器应包含 oc-ops")
	assert.True(t, containsName(containerNames, "s3-sync"), "普通容器应包含 s3-sync")

	// initContainer 必须恰好 1 个，名为 restore（启动时从 manager bootstrap 拉取配置）
	initContainers := dep.Spec.Template.Spec.InitContainers
	assert.Len(t, initContainers, 1, "Deployment 应有 1 个 initContainer")
	if len(initContainers) == 1 {
		assert.Equal(t, "restore", initContainers[0].Name, "initContainer 名应为 restore")
	}

	// ---- 硬断言 Service ----
	// 验证 Service app-<id>-ocops 被创建，且暴露端口 8080（oc-ops 控制平面 API）
	svcName := "app-" + id + "-ocops"
	svc, err := cs.CoreV1().Services(ns).Get(ctx, svcName, metav1.GetOptions{})
	require.NoError(t, err, "Service %s 应存在", svcName)

	// Service 端口 8080 是 oc-ops 控制平面的访问入口，必须正确配置
	hasPort8080 := false
	for _, p := range svc.Spec.Ports {
		if p.Port == 8080 {
			hasPort8080 = true
			break
		}
	}
	assert.True(t, hasPort8080, "Service 应包含端口 8080")

	// ---- 硬断言 Secret ----
	// 验证 Secret app-<id>-token 被创建，且包含 control-token key（per-app 控制凭证）
	secretName := "app-" + id + "-token"
	sec, err := cs.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
	require.NoError(t, err, "Secret %s 应存在", secretName)

	// k8s API 读回时 StringData 会转换到 Data（base64 编码），两者均可接受
	// StringData 在创建时被写入但读回时清空，实际数据存在 Data 字段中
	_, inData := sec.Data["control-token"]
	_, inStringData := sec.StringData["control-token"]
	assert.True(t, inData || inStringData, "Secret 应包含 control-token key（Data 或 StringData）")

	// ---- Status 软验证 ----
	// 验证资源创建后 Status 能感知 app（pod 至少被调度，Phase != NotFound）
	st, err := adapter.Status(ctx, id)
	require.NoError(t, err, "Status 应无错误")
	assert.NotEqual(t, "NotFound", st.Phase,
		"资源已存在，pod 应被调度（至少 Pending），Phase 不应为 NotFound")

	// ---- WaitReady 软断言 ----
	// pod Ready 依赖完整 bootstrap 闭环 + 真实镜像，属 A2b 合并验证范围；
	// 此处仅记录未达 Ready 的原因，不 fail。
	werr := adapter.WaitReady(ctx, id, 90*time.Second, nil)
	if werr != nil {
		t.Logf("WaitReady 未达 Ready（pod Ready 依赖完整 bootstrap 闭环 + 真实镜像，属 A2b 合并验证范围）：%v", werr)
	}
}

// TestK3dRolloutRestartRecreatesPod 验证 RolloutRestart 在真实 k3d 集群中给 Deployment
// pod template 写入 kubectl.kubernetes.io/restartedAt 注解，触发 Deployment 滚动重建。
// 测试流程：EnsureApp 创建资源 → RolloutRestart → 断言注解已写入且非空。
func TestK3dRolloutRestartRecreatesPod(t *testing.T) {
	// 构造 adapter 与 clientset，缺少 OC_K8S_TEST_KUBECONFIG 则 Skip
	adapter, cs, ns := k3dEnv(t)
	ctx := context.Background()

	hermesImage, opsImage := testImages()
	id := testAppID("it-a2b-rr-")

	spec := k8sorch.AppSpec{
		AppID:           id,
		HermesImage:     hermesImage,
		OpsImage:        opsImage,
		ControlToken:    "test-control-token",
		BootstrapURL:    "http://ocm.localhost/api/v1/bootstrap",
		ImagePullSecret: "acr-pull",
		Resources: k8sorch.ResourceLimits{
			RequestsCPU:    "100m",
			RequestsMemory: "256Mi",
			LimitsCPU:      "1",
			LimitsMemory:   "1Gi",
		},
	}

	// 先注册 Cleanup，确保无论测试成功或失败都清理资源，防止污染 k3d 集群。
	t.Cleanup(func() {
		_ = adapter.Delete(context.Background(), id)
	})

	// EnsureApp 先建好 Deployment，RolloutRestart 才有资源可 patch
	require.NoError(t, adapter.EnsureApp(ctx, spec), "EnsureApp 应成功，RolloutRestart 测试前提")

	// RolloutRestart 给 pod template 写入 restartedAt 注解，触发 Deployment 滚动重建
	require.NoError(t, adapter.RolloutRestart(ctx, id), "RolloutRestart 应成功 patch restartedAt 注解")

	// 读回 Deployment，断言注解已写入且非空（注解存在即证明 patch 生效）
	depName := "app-" + id
	d, err := cs.AppsV1().Deployments(ns).Get(ctx, depName, metav1.GetOptions{})
	require.NoError(t, err, "RolloutRestart 后 Deployment %s 应存在", depName)

	// kubectl.kubernetes.io/restartedAt 注解是 kubectl rollout restart 的标准触发机制，
	// 非空说明 patch 已正确写入 pod template annotations，Deployment 控制器会触发滚动重建。
	restartedAt := d.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"]
	assert.NotEmpty(t, restartedAt, "pod template annotations 应包含非空的 kubectl.kubernetes.io/restartedAt")
}

// TestK3dDeleteRemovesResources 验证 Delete 后 Deployment/Service/Secret 均从 k8s 集群中移除。
// Delete 后资源删除为异步操作，使用 require.Eventually 轮询断言最终 NotFound，避免偶发 flake。
func TestK3dDeleteRemovesResources(t *testing.T) {
	// 构造 adapter 与 clientset，缺少 kubeconfig 则 Skip
	adapter, cs, ns := k3dEnv(t)
	ctx := context.Background()

	hermesImage, opsImage := testImages()
	id := testAppID("it-a2a-del-")

	spec := k8sorch.AppSpec{
		AppID:           id,
		HermesImage:     hermesImage,
		OpsImage:        opsImage,
		ControlToken:    "test-control-token",
		BootstrapURL:    "http://ocm.localhost/api/v1/bootstrap",
		ImagePullSecret: "acr-pull",
		Resources: k8sorch.ResourceLimits{
			RequestsCPU:    "100m",
			RequestsMemory: "256Mi",
			LimitsCPU:      "1",
			LimitsMemory:   "1Gi",
		},
	}

	// 先注册 Cleanup，确保测试中途失败也能清理残留资源
	t.Cleanup(func() {
		_ = adapter.Delete(context.Background(), id)
	})

	// 先创建资源，再验证删除效果
	require.NoError(t, adapter.EnsureApp(ctx, spec), "EnsureApp 应成功，删除测试前提")

	// 确认 Deployment 存在，确保 Delete 前资源确实被创建（排除误报）
	depName := "app-" + id
	_, err := cs.AppsV1().Deployments(ns).Get(ctx, depName, metav1.GetOptions{})
	require.NoError(t, err, "Delete 前 Deployment %s 应存在", depName)

	// 删除三资源，必须无错误
	require.NoError(t, adapter.Delete(ctx, id), "Delete 应成功移除所有资源")

	// ---- 断言 Deployment 最终 NotFound ----
	// k8s 删除为异步操作，轮询最多 15 秒等待资源消失，避免偶发 flake
	require.Eventually(t,
		func() bool {
			_, err := cs.AppsV1().Deployments(ns).Get(ctx, depName, metav1.GetOptions{})
			return apierrors.IsNotFound(err)
		},
		15*time.Second,
		500*time.Millisecond,
		"Deployment %s 应在 Delete 后最终 NotFound", depName,
	)

	// ---- 断言 Service 最终 NotFound ----
	svcName := "app-" + id + "-ocops"
	require.Eventually(t,
		func() bool {
			_, err := cs.CoreV1().Services(ns).Get(ctx, svcName, metav1.GetOptions{})
			return apierrors.IsNotFound(err)
		},
		15*time.Second,
		500*time.Millisecond,
		"Service %s 应在 Delete 后最终 NotFound", svcName,
	)

	// ---- 断言 Secret 最终 NotFound ----
	secretName := "app-" + id + "-token"
	require.Eventually(t,
		func() bool {
			_, err := cs.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
			return apierrors.IsNotFound(err)
		},
		15*time.Second,
		500*time.Millisecond,
		"Secret %s 应在 Delete 后最终 NotFound", secretName,
	)
}
