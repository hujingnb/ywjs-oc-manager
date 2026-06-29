// web_publish_wiring.go 提供 web-publish 开通流程所需的两个适配器类型：
//   - certProvisionerImpl：把 dnsprovider.New + acme.NewIssuer 组合成 handlers.CertProvisioner。
//   - clusterApplierImpl：把 *k8sorch.KubernetesAdapter 适配成 handlers.ClusterApplier。
//
// 两个类型均在 main 包内声明，避免 internal/worker/handlers 直接依赖
// integrations/acme 和 integrations/k8sorch，保持包依赖整洁。
package main

import (
	"context"
	"fmt"

	"oc-manager/internal/integrations/acme"
	"oc-manager/internal/integrations/dnsprovider"
	"oc-manager/internal/integrations/k8sorch"
	"oc-manager/internal/worker/handlers"
)

// 编译期断言：两个适配器类型满足各自接口。
var _ handlers.CertProvisioner = certProvisionerImpl{}
var _ handlers.ClusterApplier = clusterApplierImpl{}

// certProvisionerImpl 组合 dnsprovider.New + acme.NewIssuer 实现 handlers.CertProvisioner。
//
// 设计说明：每次 Provision 调用均重新构造 Provider 与 Issuer，保证无共享状态（并发安全）；
// acme.Issuer 的账户私钥在 NewIssuer 内部生成，每次签发均独立注册，符合无状态 ACME 账户策略。
type certProvisionerImpl struct {
	// acmeEmail 是注册到 ACME CA 的联系邮箱，用于到期提醒等通知。
	acmeEmail string
	// acmeDirURL 是 ACME 目录 URL；留空使用 lego 默认（Let's Encrypt 生产）。
	acmeDirURL string
}

// Provision 根据 in 中的 DNS provider 类型和凭证，签发 *.in.BaseDomain 通配证书。
// 流程：
//  1. 按 ProviderType 构造 DNS provider（校验凭证）；
//  2. 构造 acme.Issuer（连接 ACME CA）；
//  3. 调用 Issue 写通配 A 记录并完成 DNS-01 挑战，返回 PEM 证书链。
func (c certProvisionerImpl) Provision(ctx context.Context, in handlers.CertProvisionInput) (acme.Certificate, error) {
	// 按 provider 类型构造 DNS provider 实例，同时校验凭证字段完整性。
	p, err := dnsprovider.New(ctx, dnsprovider.ProviderType(in.ProviderType), in.Credentials, in.BaseDomain)
	if err != nil {
		return acme.Certificate{}, fmt.Errorf("certProvisioner: 构造 DNS provider 失败: %w", err)
	}

	// 构造 ACME Issuer：每次调用重新生成账户私钥并向 CA 注册（无状态 ACME 账户策略）。
	issuer := acme.NewIssuer(p, acme.IssuerConfig{
		Email:    c.acmeEmail,
		CADirURL: c.acmeDirURL,
	})

	// Issue 先幂等写通配 A 记录，再完成 ACME DNS-01 挑战并返回 PEM 证书链。
	return issuer.Issue(ctx, in.BaseDomain, in.IngressIP)
}

// clusterApplierImpl 把 *k8sorch.KubernetesAdapter 适配为 handlers.ClusterApplier。
//
// ApplyTLSSecret 直接委托给 adapter 的同名方法；
// ApplyWildcardIngress 把 handlers.WildcardIngressParams 映射到 k8sorch.WildcardIngressSpec。
type clusterApplierImpl struct {
	// adapter 是持有 k8s clientset 与命名空间的具体适配器。
	adapter *k8sorch.KubernetesAdapter
}

// ApplyTLSSecret 幂等写入或更新 k8s TLS Secret。
func (c clusterApplierImpl) ApplyTLSSecret(ctx context.Context, name string, certPEM, keyPEM []byte) error {
	return c.adapter.ApplyTLSSecret(ctx, name, certPEM, keyPEM)
}

// ApplyWildcardIngress 把 handlers.WildcardIngressParams 映射到 k8sorch.WildcardIngressSpec，
// 然后委托给 KubernetesAdapter.ApplyWildcardIngress；Namespace 留空让 adapter 用自身命名空间。
func (c clusterApplierImpl) ApplyWildcardIngress(ctx context.Context, p handlers.WildcardIngressParams) error {
	return c.adapter.ApplyWildcardIngress(ctx, k8sorch.WildcardIngressSpec{
		Name:             p.Name,
		BaseDomain:       p.BaseDomain,
		TLSSecretName:    p.TLSSecretName,
		IngressClassName: p.IngressClassName,
		BackendService:   p.BackendService,
		BackendPort:      p.BackendPort,
		// Namespace 留空：KubernetesAdapter.ApplyWildcardIngress 以 adapter 命名空间兜底。
	})
}

// noopClusterApplier 在 Kubernetes 未启用时占位，让 handler 得以注册，
// 但调用时返回清晰的错误，而不是 nil panic。
// web-publish 开通本身依赖 k8s，未启用时 provisioning 任务到达 worker 会失败、
// backoff 重试，运维修复后重新启用即可——这是预期的降级语义。
type noopClusterApplier struct{}

var _ handlers.ClusterApplier = noopClusterApplier{}

// ApplyTLSSecret 在 Kubernetes 未启用时返回明确错误。
func (noopClusterApplier) ApplyTLSSecret(_ context.Context, _ string, _, _ []byte) error {
	return fmt.Errorf("kubernetes 未启用，无法写入 TLS Secret")
}

// ApplyWildcardIngress 在 Kubernetes 未启用时返回明确错误。
func (noopClusterApplier) ApplyWildcardIngress(_ context.Context, _ handlers.WildcardIngressParams) error {
	return fmt.Errorf("kubernetes 未启用，无法创建通配 Ingress")
}
