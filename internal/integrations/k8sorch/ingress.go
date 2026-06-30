package k8sorch

import (
	"context"
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WildcardIngressSpec 描述一条 *.base_domain → site-server 的通配 Ingress。
type WildcardIngressSpec struct {
	Name             string // Ingress 名（按企业基础域名确定性生成）
	Namespace        string // 命名空间（与 TLS Secret、site-server Service 同）
	BaseDomain       string // 企业基础域名（不含通配前缀）
	TLSSecretName    string // 通配证书 TLS Secret 名
	IngressClassName string // ingressClassName，跟随环境
	BackendService   string // backend Service 名（site-server）
	BackendPort      int32  // backend Service 端口
}

// RenderWildcardIngress 渲染一条把 *.base_domain 全部 path 转发给 site-server、用通配证书做 TLS 的 Ingress。
// backend Service 可能此刻尚未存在（Plan 3 部署），k8s 允许，公网访问 503 直到 Service 出现。
func RenderWildcardIngress(s WildcardIngressSpec) *networkingv1.Ingress {
	wildcard := "*." + s.BaseDomain
	pathType := networkingv1.PathTypePrefix
	className := s.IngressClassName
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        s.Name,
			Namespace:   s.Namespace,
			Labels:      map[string]string{"app.kubernetes.io/part-of": "oc-manager", "app.kubernetes.io/component": "web-publish-ingress"},
			Annotations: wildcardIngressAnnotations(wildcard),
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			TLS: []networkingv1.IngressTLS{{
				Hosts:      []string{wildcard},
				SecretName: s.TLSSecretName,
			}},
			Rules: []networkingv1.IngressRule{{
				Host: wildcard,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: s.BackendService,
									Port: networkingv1.ServiceBackendPort{Number: s.BackendPort},
								},
							},
						}},
					},
				},
			}},
		},
	}
}

// wildcardIngressAnnotations 生成通配 Ingress 的注解，与线上 ocm Ingress 的约定对齐：
//   - kubernetes.io/ingress.property：移动云 ingress controller 据此把规则编程进外部 LB，
//     缺失则 LB 不会路由该域名（线上必需）；值描述 host/path/匹配方式/后端协议（HTTP，
//     与 ocm Ingress 一致——TLS 由 LB 终止，到后端走 HTTP）。
//   - kubernetes.io/load-balancer-protocol=TERMINATED_HTTPS：LB 终止 HTTPS（用本 Ingress
//     TLS 段引用的通配证书），到 site-server 走明文 HTTP。
//   - nginx proxy-*：放宽请求体大小与读写超时，适配静态站点较大文件（与 ocm Ingress 一致）。
//
// 这些注解对不识别它们的 controller（如本地 k3d traefik）无副作用，会被直接忽略。
func wildcardIngressAnnotations(wildcardHost string) map[string]string {
	return map[string]string{
		"kubernetes.io/ingress.property":                    fmt.Sprintf(`[{"host":%q,"path":"/","compareType":"Prefix","protocol":"HTTP"}]`, wildcardHost),
		"kubernetes.io/load-balancer-protocol":              "TERMINATED_HTTPS",
		"nginx.ingress.kubernetes.io/proxy-body-size":       "1024m",
		"nginx.ingress.kubernetes.io/proxy-connect-timeout": "2000",
		"nginx.ingress.kubernetes.io/proxy-read-timeout":    "600",
		"nginx.ingress.kubernetes.io/proxy-send-timeout":    "600",
	}
}

// ApplyWildcardIngress 幂等 apply 通配 Ingress（首建创建、改配置更新）。
// spec.Namespace 为空则用 adapter 命名空间，保持与 TLS Secret/Service 一致。
func (a *KubernetesAdapter) ApplyWildcardIngress(ctx context.Context, spec WildcardIngressSpec) error {
	if spec.Namespace == "" {
		spec.Namespace = a.namespace
	}
	ing := RenderWildcardIngress(spec)
	api := a.client.NetworkingV1().Ingresses(spec.Namespace)
	existing, err := api.Get(ctx, ing.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, cerr := api.Create(ctx, ing, metav1.CreateOptions{})
		return wrapK8s("创建通配 Ingress", cerr)
	}
	if err != nil {
		return wrapK8s("查询通配 Ingress", err)
	}
	ing.ResourceVersion = existing.ResourceVersion
	_, uerr := api.Update(ctx, ing, metav1.UpdateOptions{})
	return wrapK8s("更新通配 Ingress", uerr)
}

// DeleteWildcardIngress 删除通配 Ingress（NotFound 视为成功，幂等）。
func (a *KubernetesAdapter) DeleteWildcardIngress(ctx context.Context, name string) error {
	err := a.client.NetworkingV1().Ingresses(a.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return wrapK8s("删除通配 Ingress", err)
	}
	return nil
}
