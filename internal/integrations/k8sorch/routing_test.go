package k8sorch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"oc-manager/internal/domain"
)

type routingResolver map[string]domain.AppType

// ResolveAppType 模拟从持久化记录读取应用类型；测试可传入未知类型验证路由不会降级为普通 namespace。
func (r routingResolver) ResolveAppType(_ context.Context, id string) (domain.AppType, error) {
	return r[id], nil
}

// TestRoutingOrchestratorEnsureAppSeparatesNamespaces 验证两类应用不会在对方 namespace 创建资源。
func TestRoutingOrchestratorEnsureAppSeparatesNamespaces(t *testing.T) {
	cs := fake.NewSimpleClientset()
	r := NewRoutingOrchestrator(NewKubernetesAdapter(cs, "oc-apps"), NewAICCKubernetesAdapter(cs, "oc-aicc"), routingResolver{})
	normal := testSpec()
	normal.AppID = "normal"
	normal.AppType = domain.AppTypeStandard
	aicc := testSpec()
	aicc.AppID = "aicc"
	aicc.AppType = domain.AppTypeAICC
	require.NoError(t, r.EnsureApp(context.Background(), normal))
	require.NoError(t, r.EnsureApp(context.Background(), aicc))
	_, err := cs.AppsV1().Deployments("oc-apps").Get(context.Background(), "app-normal", metav1.GetOptions{})
	require.NoError(t, err)
	_, err = cs.AppsV1().Deployments("oc-aicc").Get(context.Background(), "app-aicc", metav1.GetOptions{})
	require.NoError(t, err)
	_, err = cs.AppsV1().Deployments("oc-apps").Get(context.Background(), "app-aicc", metav1.GetOptions{})
	require.Error(t, err)
	// AICC 的 HPA 也必须随路由写入专用 namespace，不能落在普通应用 namespace。
	_, err = cs.AutoscalingV2beta2().HorizontalPodAutoscalers("oc-aicc").Get(context.Background(), "app-aicc", metav1.GetOptions{})
	require.NoError(t, err)
	_, err = cs.AutoscalingV2beta2().HorizontalPodAutoscalers("oc-apps").Get(context.Background(), "app-aicc", metav1.GetOptions{})
	require.Error(t, err)
}

// TestRoutingOrchestratorAICCStopStartManagesHPA 验证 AICC 停止先移除 HPA 后缩容到零，
// 启动再恢复 HPA，避免 minReplicas=1 把已停止的客服应用自动拉起。
func TestRoutingOrchestratorAICCStopStartManagesHPA(t *testing.T) {
	cs := fake.NewSimpleClientset()
	var operations []string
	cs.PrependReactor("*", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetVerb() == "delete" || action.GetVerb() == "create" || action.GetVerb() == "update" {
			operations = append(operations, action.GetVerb()+"/"+action.GetResource().Resource)
		}
		return false, nil, nil
	})
	r := NewRoutingOrchestrator(NewKubernetesAdapter(cs, "oc-apps"), NewAICCKubernetesAdapter(cs, "oc-aicc"), routingResolver{"aicc": domain.AppTypeAICC})
	spec := testSpec()
	spec.AppID = "aicc"
	spec.AppType = domain.AppTypeAICC
	require.NoError(t, r.EnsureApp(context.Background(), spec))

	// 停止后 HPA 不存在，Deployment 必须保持零副本，不能被 minReplicas 自动恢复。
	operations = nil
	require.NoError(t, r.Stop(context.Background(), "aicc"))
	assert.Equal(t, []string{"delete/horizontalpodautoscalers", "update/deployments"}, operations,
		"停止必须先删除 HPA，再把 Deployment 缩容到零")
	dep, err := cs.AppsV1().Deployments("oc-aicc").Get(context.Background(), "app-aicc", metav1.GetOptions{})
	require.NoError(t, err)
	require.NotNil(t, dep.Spec.Replicas)
	assert.Equal(t, int32(0), *dep.Spec.Replicas)
	_, err = cs.AutoscalingV2beta2().HorizontalPodAutoscalers("oc-aicc").Get(context.Background(), "app-aicc", metav1.GetOptions{})
	require.Error(t, err)

	// 启动时先恢复 HPA，再将 Deployment 拉到最小副本，确保恢复后可继续弹性扩容。
	operations = nil
	require.NoError(t, r.Start(context.Background(), "aicc"))
	assert.Equal(t, []string{"update/deployments", "create/horizontalpodautoscalers"}, operations,
		"启动必须先拉起 Deployment，再恢复 HPA，避免缩放失败时 HPA 提前启动实例")
	hpa, err := cs.AutoscalingV2beta2().HorizontalPodAutoscalers("oc-aicc").Get(context.Background(), "app-aicc", metav1.GetOptions{})
	require.NoError(t, err)
	require.NotNil(t, hpa.Spec.MinReplicas)
	assert.Equal(t, int32(1), *hpa.Spec.MinReplicas)
	dep, err = cs.AppsV1().Deployments("oc-aicc").Get(context.Background(), "app-aicc", metav1.GetOptions{})
	require.NoError(t, err)
	require.NotNil(t, dep.Spec.Replicas)
	assert.Equal(t, int32(1), *dep.Spec.Replicas)
}

// TestRoutingOrchestratorRejectsUnknownAppType 验证状态操作解析到未知类型时必须失败，
// 不能把未知类型静默路由至普通 namespace。
func TestRoutingOrchestratorRejectsUnknownAppType(t *testing.T) {
	resolver := routingResolver{
		// 未登记的枚举值必须拒绝，防止后续类型扩展被静默路由为普通应用。
		"unknown": domain.AppType("unknown"),
		// 持久化字段为空时同样必须拒绝，避免错误数据被默认分发到普通 namespace。
		"empty": "",
	}
	r := NewRoutingOrchestrator(nil, nil, resolver)

	// 未知类型不具备明确 namespace 归属，target 必须返回错误而非默认 normal。
	_, err := r.target(context.Background(), "unknown")
	assert.Error(t, err)
	// 空类型也不具备明确 namespace 归属，必须与未知枚举一致 fail-closed。
	_, err = r.target(context.Background(), "empty")
	assert.Error(t, err)
}
