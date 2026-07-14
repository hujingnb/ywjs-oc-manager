package k8sorch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

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
	r := NewRoutingOrchestrator(NewKubernetesAdapter(cs, "oc-apps"), NewKubernetesAdapter(cs, "oc-aicc"), routingResolver{})
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
}

// TestRoutingOrchestratorRejectsUnknownAppType 验证状态操作解析到未知类型时必须失败，
// 不能把未知类型静默路由至普通 namespace。
func TestRoutingOrchestratorRejectsUnknownAppType(t *testing.T) {
	resolver := routingResolver{"unknown": domain.AppType("unknown")}
	r := NewRoutingOrchestrator(nil, nil, resolver)

	// 未知类型不具备明确 namespace 归属，target 必须返回错误而非默认 normal。
	_, err := r.target(context.Background(), "unknown")
	assert.Error(t, err)
}
