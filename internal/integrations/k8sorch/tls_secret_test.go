package k8sorch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestRenderTLSSecret 覆盖：渲染出的 Secret 类型为 TLS、含 tls.crt/tls.key 两个 Data key、名称与命名空间正确。
func TestRenderTLSSecret(t *testing.T) {
	s := RenderTLSSecret("wildcard-apps-example-com", "ocm", []byte("CERT"), []byte("KEY"))
	assert.Equal(t, corev1.SecretTypeTLS, s.Type)
	assert.Equal(t, []byte("CERT"), s.Data[corev1.TLSCertKey])
	assert.Equal(t, []byte("KEY"), s.Data[corev1.TLSPrivateKeyKey])
	assert.Equal(t, "wildcard-apps-example-com", s.Name)
	assert.Equal(t, "ocm", s.Namespace)
}

// TestApplyTLSSecretCreateThenUpdate 覆盖：首次 Apply 创建，二次 Apply 同名走更新分支不报错且内容刷新（续期场景）。
func TestApplyTLSSecretCreateThenUpdate(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(client, "ocm")
	ctx := context.Background()

	require.NoError(t, a.ApplyTLSSecret(ctx, "wc", []byte("C1"), []byte("K1")))
	got, err := client.CoreV1().Secrets("ocm").Get(ctx, "wc", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []byte("C1"), got.Data[corev1.TLSCertKey])

	require.NoError(t, a.ApplyTLSSecret(ctx, "wc", []byte("C2"), []byte("K2")))
	got, err = client.CoreV1().Secrets("ocm").Get(ctx, "wc", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []byte("C2"), got.Data[corev1.TLSCertKey])
}

// TestDeleteTLSSecretIdempotent 覆盖：删除不存在的 Secret 不报错（幂等回收）。
func TestDeleteTLSSecretIdempotent(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := NewKubernetesAdapter(client, "ocm")
	require.NoError(t, a.DeleteTLSSecret(context.Background(), "missing"))
}
