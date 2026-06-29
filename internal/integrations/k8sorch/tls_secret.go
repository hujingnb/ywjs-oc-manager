package k8sorch

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RenderTLSSecret 渲染一个 kubernetes.io/tls 类型的 Secret，供通配 Ingress 引用。
// name 由调用方按企业基础域名确定性生成（如 wildcard-<sanitized base domain>），
// namespace 跟随通配 Ingress 所在命名空间。
func RenderTLSSecret(name, namespace string, certPEM, keyPEM []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/part-of":    "oc-manager",
				"app.kubernetes.io/component": "web-publish-cert",
			},
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       certPEM, // tls.crt
			corev1.TLSPrivateKeyKey: keyPEM,  // tls.key
		},
	}
}

// ApplyTLSSecret 幂等 apply 通配证书 Secret（首签创建、续期更新），
// 复用与 applySecret 一致的 get-or-create-then-update 模式。
func (a *KubernetesAdapter) ApplyTLSSecret(ctx context.Context, name string, certPEM, keyPEM []byte) error {
	s := RenderTLSSecret(name, a.namespace, certPEM, keyPEM)
	api := a.client.CoreV1().Secrets(a.namespace)
	existing, err := api.Get(ctx, s.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, cerr := api.Create(ctx, s, metav1.CreateOptions{})
		return wrapK8s("创建 TLS Secret", cerr)
	}
	if err != nil {
		return wrapK8s("查询 TLS Secret", err)
	}
	// 续期场景：保留 resourceVersion 以满足 k8s 乐观锁要求，覆盖证书内容。
	s.ResourceVersion = existing.ResourceVersion
	_, uerr := api.Update(ctx, s, metav1.UpdateOptions{})
	return wrapK8s("更新 TLS Secret", uerr)
}

// DeleteTLSSecret 删除通配证书 Secret（NotFound 视为成功，幂等）。
func (a *KubernetesAdapter) DeleteTLSSecret(ctx context.Context, name string) error {
	err := a.client.CoreV1().Secrets(a.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return wrapK8s("删除 TLS Secret", err)
	}
	return nil
}
