// web_publish_wiring.go 提供 web-publish 开通流程所需的两个适配器类型：
//   - certProvisionerImpl：把 dnsprovider.New + acme.NewIssuer 组合成 handlers.CertProvisioner。
//   - clusterApplierImpl：把 *k8sorch.KubernetesAdapter 适配成 handlers.ClusterApplier。
//
// 两个类型均在 main 包内声明，避免 internal/worker/handlers 直接依赖
// integrations/acme 和 integrations/k8sorch，保持包依赖整洁。
package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"sync"
	"time"

	"oc-manager/internal/integrations/acme"
	"oc-manager/internal/integrations/dnsprovider"
	"oc-manager/internal/integrations/k8sorch"
	"oc-manager/internal/worker/handlers"
)

// 编译期断言：三个适配器类型满足各自接口。
var _ handlers.CertProvisioner = (*certProvisionerImpl)(nil)
var _ handlers.CertProvisioner = devSelfSignedCertProvisioner{}
var _ handlers.ClusterApplier = clusterApplierImpl{}

// ACME 账户私钥持久化位置：manager 命名空间内的 Opaque Secret（平台级单例，跨企业共用一个 ACME 账户）。
const (
	acmeAccountSecretName = "web-publish-acme-account"
	acmeAccountSecretKey  = "account.key.pem"
)

// acmeAccountKeyStore 抽象「幂等读取/创建持久化机密值」的能力，由 *k8sorch.KubernetesAdapter 实现。
// 抽成接口便于在 k8s 未启用时传 nil 退回旧行为，也便于将来替换持久化后端。
type acmeAccountKeyStore interface {
	GetOrCreateOpaqueSecretValue(ctx context.Context, name, dataKey string, gen func() ([]byte, error)) ([]byte, error)
}

// certProvisionerImpl 组合 dnsprovider.New + acme.NewIssuer 实现 handlers.CertProvisioner。
//
// ACME 账户私钥策略：从持久化 Secret 读取/创建一份稳定的平台账户私钥，所有签发复用同一账户，
// 使 lego 注册返回「已存在账户」而非新建，避免 Let's Encrypt「每 IP 每 3h 新注册数」限流（429）。
// 进程内缓存解析后的私钥，避免每次 Provision 都读 Secret。keyStore 为 nil（k8s 未启用）时退回
// 旧行为：每次签发生成新账户私钥。
type certProvisionerImpl struct {
	// acmeEmail 是注册到 ACME CA 的联系邮箱，用于到期提醒等通知。
	acmeEmail string
	// acmeDirURL 是 ACME 目录 URL；留空使用 lego 默认（Let's Encrypt 生产）。
	acmeDirURL string
	// keyStore 持久化 ACME 账户私钥；nil 时不持久化（降级为每次新账户）。
	keyStore acmeAccountKeyStore

	// mu 保护下面的缓存字段；accountKey 仅在成功加载后缓存，失败不缓存以便后续重试。
	mu         sync.Mutex
	accountKey crypto.Signer
}

// ensureAccountKey 返回稳定复用的 ACME 账户私钥（从 Secret 读取/创建，进程内缓存）。
// keyStore 为 nil 时返回 (nil, nil)，由 Issuer 退回为每次签发生成新账户。
func (c *certProvisionerImpl) ensureAccountKey(ctx context.Context) (crypto.Signer, error) {
	if c.keyStore == nil {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accountKey != nil {
		return c.accountKey, nil
	}
	pemBytes, err := c.keyStore.GetOrCreateOpaqueSecretValue(ctx, acmeAccountSecretName, acmeAccountSecretKey, generateECAccountKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("certProvisioner: 读取/创建 ACME 账户私钥失败: %w", err)
	}
	key, err := parseECAccountKeyPEM(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("certProvisioner: 解析 ACME 账户私钥失败: %w", err)
	}
	c.accountKey = key
	return key, nil
}

// generateECAccountKeyPEM 生成一把 P-256 ECDSA 私钥并编码为 PEM（EC PRIVATE KEY）。供首次持久化使用。
func generateECAccountKeyPEM() ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

// parseECAccountKeyPEM 解析 generateECAccountKeyPEM 写出的 PEM，返回 crypto.Signer。
func parseECAccountKeyPEM(pemBytes []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("ACME 账户私钥 PEM 解码失败")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return key, nil
}

// Provision 根据 in 中的 DNS provider 类型和凭证，签发 *.in.BaseDomain 通配证书。
// 流程：
//  1. 按 ProviderType 构造 DNS provider（校验凭证）；
//  2. 取稳定的平台 ACME 账户私钥（复用避免新注册限流）；
//  3. 构造 acme.Issuer 并 Issue（写通配 A 记录 + DNS-01 挑战），返回 PEM 证书链。
func (c *certProvisionerImpl) Provision(ctx context.Context, in handlers.CertProvisionInput) (acme.Certificate, error) {
	// 按 provider 类型构造 DNS provider 实例，同时校验凭证字段完整性。
	p, err := dnsprovider.New(ctx, dnsprovider.ProviderType(in.ProviderType), in.Credentials, in.BaseDomain)
	if err != nil {
		return acme.Certificate{}, fmt.Errorf("certProvisioner: 构造 DNS provider 失败: %w", err)
	}

	// 取稳定复用的平台 ACME 账户私钥（持久化在 Secret，跨重试/重启/副本共用同一账户）。
	accountKey, err := c.ensureAccountKey(ctx)
	if err != nil {
		return acme.Certificate{}, err
	}

	// 构造 ACME Issuer：注入复用账户私钥（nil 时 Issuer 退回每次新账户）。
	issuer := acme.NewIssuer(p, acme.IssuerConfig{
		Email:      c.acmeEmail,
		CADirURL:   c.acmeDirURL,
		AccountKey: accountKey,
	})

	// Issue 先幂等写通配 A 记录，再完成 ACME DNS-01 挑战并返回 PEM 证书链。
	return issuer.Issue(ctx, in.BaseDomain, in.IngressIP)
}

// devSelfSignedCertProvisioner 是仅供本地/dev 联调的 CertProvisioner：
// 直接现生成一张 *.BaseDomain 自签通配证书，完全跳过 DNS provider 调用与 ACME 签发链路。
//
// 存在动因：真实通配证书必须走 ACME DNS-01，依赖公网可解析域名 + 真实云 DNS 凭证，
// 本地 k3d（*.localhost、无公网、无真实凭证）无法完成。开启 dev_self_signed_cert 后，
// provisioning 状态机除「签证书」一步换成自签外，其余（解密凭证、写真实 k8s TLS Secret、
// 建真实通配 Ingress、状态收敛）全部走与生产一致的真实代码，使本地能端到端验证发布链路。
//
// 安全约束：仅当 config.WebPublish.DevSelfSignedCert=true 时才在 main 装配，默认关闭；
// 启用时进程启动打醒目 WARN 日志。自签证书浏览器不信任，生产绝不能启用。
type devSelfSignedCertProvisioner struct{}

// Provision 生成 *.in.BaseDomain 自签通配证书（含 BaseDomain 本身的 SAN），有效期 90 天，
// 返回与真实签发同形状的 acme.Certificate（PEM 证书 + 私钥 + NotAfter）。忽略 DNS provider 与凭证。
func (devSelfSignedCertProvisioner) Provision(_ context.Context, in handlers.CertProvisionInput) (acme.Certificate, error) {
	// 生成 2048 位 RSA 私钥（自签足够；与真实签发一致地输出 PEM）。
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return acme.Certificate{}, fmt.Errorf("devSelfSigned: 生成 RSA 私钥失败: %w", err)
	}

	// 有效期 90 天，与 Let's Encrypt 真实证书时长对齐，便于续期巡检逻辑在本地也可观测。
	notBefore := time.Now().UTC()
	notAfter := notBefore.Add(90 * 24 * time.Hour)
	wildcard := "*." + in.BaseDomain

	// 证书序列号：x509 要求唯一正整数；本地自签用当前纳秒时间戳足够避免重签碰撞。
	serial := big.NewInt(notBefore.UnixNano())

	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: wildcard},
		// SAN 同时覆盖通配子域与基础域名本身，使 <slug>.base_domain 与 base_domain 都被证书覆盖。
		DNSNames:              []string{wildcard, in.BaseDomain},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		// 自签：自己即为 CA，IsCA 置真使其能自我签名。
		IsCA: true,
	}

	// 自签：parent==template、签名私钥即自身私钥。
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return acme.Certificate{}, fmt.Errorf("devSelfSigned: 自签证书失败: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	return acme.Certificate{
		CertPEM:  certPEM,
		KeyPEM:   keyPEM,
		NotAfter: notAfter.Unix(),
	}, nil
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
