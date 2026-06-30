package k8sorch

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetOrCreateOpaqueSecretValue 幂等地读取（不存在则用 gen 生成并写入）一个 Opaque Secret 中
// 指定 dataKey 的值，返回最终值。用于持久化平台级单例机密（如 ACME 账户私钥）：
// 多副本、多次调用、跨重启都拿到同一份值，避免每次生成新值。
//
// 处理四种情形：
//   - Secret 存在且含该 key：直接返回已有值（最常见，复用）；
//   - Secret 存在但缺该 key：用 gen 生成、补写该 key（保留其它 key），返回新值；
//   - Secret 不存在：生成并创建；
//   - 并发创建冲突（AlreadyExists）：重读他人写入的值返回，保证全局一致。
func (a *KubernetesAdapter) GetOrCreateOpaqueSecretValue(ctx context.Context, name, dataKey string, gen func() ([]byte, error)) ([]byte, error) {
	api := a.client.CoreV1().Secrets(a.namespace)

	existing, err := api.Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		if v, ok := existing.Data[dataKey]; ok && len(v) > 0 {
			return v, nil
		}
		// Secret 已存在但缺该 key：补写。
		val, gerr := gen()
		if gerr != nil {
			return nil, gerr
		}
		if existing.Data == nil {
			existing.Data = map[string][]byte{}
		}
		existing.Data[dataKey] = val
		if _, uerr := api.Update(ctx, existing, metav1.UpdateOptions{}); uerr != nil {
			return nil, wrapK8s("更新 Opaque Secret", uerr)
		}
		return val, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, wrapK8s("查询 Opaque Secret", err)
	}

	// 不存在：生成并创建。
	val, gerr := gen()
	if gerr != nil {
		return nil, gerr
	}
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: a.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/part-of":   "oc-manager",
				"app.kubernetes.io/component": "web-publish-acme",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{dataKey: val},
	}
	_, cerr := api.Create(ctx, s, metav1.CreateOptions{})
	if cerr == nil {
		return val, nil
	}
	// 并发：他人已创建，重读其值，保证全局返回同一份。
	if apierrors.IsAlreadyExists(cerr) {
		again, gerr2 := api.Get(ctx, name, metav1.GetOptions{})
		if gerr2 != nil {
			return nil, wrapK8s("重读 Opaque Secret", gerr2)
		}
		if v, ok := again.Data[dataKey]; ok && len(v) > 0 {
			return v, nil
		}
	}
	return nil, wrapK8s("创建 Opaque Secret", cerr)
}
