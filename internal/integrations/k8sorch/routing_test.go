package k8sorch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type routingResolver map[string]bool

func (r routingResolver) IsAICCHidden(_ context.Context, id string) (bool, error) { return r[id], nil }

// TestRoutingOrchestratorEnsureAppSeparatesNamespaces 验证两类应用不会在对方 namespace 创建资源。
func TestRoutingOrchestratorEnsureAppSeparatesNamespaces(t *testing.T) {
	cs := fake.NewSimpleClientset()
	r := NewRoutingOrchestrator(NewKubernetesAdapter(cs, "oc-apps"), NewKubernetesAdapter(cs, "oc-aicc"), routingResolver{})
	normal := testSpec()
	normal.AppID = "normal"
	aicc := testSpec()
	aicc.AppID = "aicc"
	aicc.AICCHidden = true
	require.NoError(t, r.EnsureApp(context.Background(), normal))
	require.NoError(t, r.EnsureApp(context.Background(), aicc))
	_, err := cs.AppsV1().Deployments("oc-apps").Get(context.Background(), "app-normal", metav1.GetOptions{})
	require.NoError(t, err)
	_, err = cs.AppsV1().Deployments("oc-aicc").Get(context.Background(), "app-aicc", metav1.GetOptions{})
	require.NoError(t, err)
	_, err = cs.AppsV1().Deployments("oc-apps").Get(context.Background(), "app-aicc", metav1.GetOptions{})
	require.Error(t, err)
}
