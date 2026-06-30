package k8sorch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestRenderWildcardIngress 覆盖：host 为 *.base、TLS 引用给定 Secret、backend 指向 service/port、class 正确。
func TestRenderWildcardIngress(t *testing.T) {
	ing := RenderWildcardIngress(WildcardIngressSpec{
		Name: "wc-apps", Namespace: "oc-apps", BaseDomain: "apps.example.com",
		TLSSecretName: "wildcard-apps", IngressClassName: "traefik",
		BackendService: "site-server", BackendPort: 80,
	})
	assert.Equal(t, "traefik", *ing.Spec.IngressClassName)
	require.Len(t, ing.Spec.Rules, 1)
	assert.Equal(t, "*.apps.example.com", ing.Spec.Rules[0].Host)
	require.Len(t, ing.Spec.TLS, 1)
	assert.Equal(t, []string{"*.apps.example.com"}, ing.Spec.TLS[0].Hosts)
	assert.Equal(t, "wildcard-apps", ing.Spec.TLS[0].SecretName)
	b := ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service
	assert.Equal(t, "site-server", b.Name)
	assert.Equal(t, int32(80), b.Port.Number)
	// 移动云 LB 注解：ingress.property 含通配 host（缺失则线上 LB 不路由）、TLS 由 LB 终止、放宽体积/超时。
	assert.Contains(t, ing.Annotations["kubernetes.io/ingress.property"], `"host":"*.apps.example.com"`)
	assert.Equal(t, "TERMINATED_HTTPS", ing.Annotations["kubernetes.io/load-balancer-protocol"])
	assert.Equal(t, "1024m", ing.Annotations["nginx.ingress.kubernetes.io/proxy-body-size"])
}

// TestApplyWildcardIngressCreateThenUpdate 覆盖：首次 Apply 创建，二次 Apply 同名走更新分支不报错。
func TestApplyWildcardIngressCreateThenUpdate(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(client, "oc-apps")
	spec := WildcardIngressSpec{Name: "wc", Namespace: "oc-apps", BaseDomain: "apps.example.com",
		TLSSecretName: "s", IngressClassName: "traefik", BackendService: "site-server", BackendPort: 80}

	require.NoError(t, a.ApplyWildcardIngress(context.Background(), spec))
	_, err := client.NetworkingV1().Ingresses("oc-apps").Get(context.Background(), "wc", metav1.GetOptions{})
	require.NoError(t, err)

	require.NoError(t, a.ApplyWildcardIngress(context.Background(), spec))
}

// TestDeleteWildcardIngressIdempotent 覆盖：删除不存在的 Ingress 不报错（回收幂等）。
func TestDeleteWildcardIngressIdempotent(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(client, "oc-apps")
	require.NoError(t, a.DeleteWildcardIngress(context.Background(), "missing"))
}
