package acme

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"oc-manager/internal/integrations/dnsprovider"

	"github.com/go-acme/lego/v5/certcrypto"
	"github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge/dns01"
	"github.com/go-acme/lego/v5/lego"
	"github.com/go-acme/lego/v5/registration"
)

// Certificate 是一次签发的产物：PEM 编码的证书链与私钥，外加从证书解析出的到期时间。
type Certificate struct {
	CertPEM  []byte // tls.crt：证书链 PEM
	KeyPEM   []byte // tls.key：私钥 PEM
	NotAfter int64  // 证书到期 Unix 秒（供 cert_not_after 落库与续期巡检）；fake/未解析时为 0
}

// obtainer 抽象「真正向 ACME CA 请求一张证书」的动作，便于单测注入 fake。
type obtainer interface {
	Obtain(ctx context.Context, domains []string) (Certificate, error)
}

// Issuer 编排通配证书签发：先确保通配 A 记录解析，再请求证书。
type Issuer struct {
	provider dnsprovider.Provider
	obtainer obtainer
}

// IssuerConfig 包含生产 Issuer 所需的 ACME 配置。
type IssuerConfig struct {
	// Email 是注册到 CA 的联系邮箱，用于到期提醒等通知。
	Email string
	// CADirURL 是 ACME 目录 URL；留空则使用 lego 默认值（Let's Encrypt 生产）。
	CADirURL string
	// AccountKey 是稳定复用的 ACME 账户私钥；非 nil 时所有签发共用该账户，
	// 使 lego 注册返回「已存在账户」而非新建，避免 Let's Encrypt 新注册限流（429）。
	// 留空（如本地/测试）则每次签发生成新账户私钥（旧行为）。
	AccountKey crypto.Signer
}

// NewIssuer 构造一个连接真实 ACME CA 的 Issuer（生产用）。
// 每次调用生成新的账户私钥并向 CA 注册；本期账户无状态，每次签发均重新注册。
func NewIssuer(p dnsprovider.Provider, cfg IssuerConfig) *Issuer {
	ob := &legoObtainer{provider: p, cfg: cfg}
	return &Issuer{provider: p, obtainer: ob}
}

// newIssuerWithObtainer 用显式 obtainer 构造 Issuer（供单测注入 fake）。
func newIssuerWithObtainer(p dnsprovider.Provider, ob obtainer) *Issuer {
	return &Issuer{provider: p, obtainer: ob}
}

// Issue 签发 *.baseDomain 通配证书：先幂等写通配 A 记录（DNS 未就绪直接失败，不浪费 ACME 配额），
// 再用通配域名请求证书并返回 PEM。
func (i *Issuer) Issue(ctx context.Context, baseDomain, ip string) (Certificate, error) {
	// 先幂等写通配 A 记录，DNS 失败时直接返回，避免消耗 ACME 签发配额。
	if err := i.provider.EnsureWildcardA(ctx, baseDomain, ip); err != nil {
		return Certificate{}, fmt.Errorf("acme: 写通配 A 记录失败: %w", err)
	}
	wildcard := "*." + baseDomain
	cert, err := i.obtainer.Obtain(ctx, []string{wildcard})
	if err != nil {
		return Certificate{}, fmt.Errorf("acme: 签发 %s 失败: %w", wildcard, err)
	}
	return cert, nil
}

// legoObtainer 是 obtainer 的生产实现，用 lego v5 向真实 ACME CA 请求证书。
// 每次 Obtain 均生成新账户并注册（本期账户无状态策略）。
type legoObtainer struct {
	// provider 同时充当 lego DNS-01 挑战器（内嵌 challenge.Provider）。
	provider dnsprovider.Provider
	// cfg 保存邮箱与 CA 目录 URL。
	cfg IssuerConfig
}

// Obtain 用 lego v5 完整流程签发域名列表对应的证书，返回 PEM 与解析出的 NotAfter。
//
// 流程：生成账户 → 构建 lego Config → 创建 Client → 设置 DNS-01 挑战器 →
// 注册账户 → 签发证书 → 解析 NotAfter。
func (o *legoObtainer) Obtain(ctx context.Context, domains []string) (Certificate, error) {
	// 1. 准备 ACME 账户：优先复用配置注入的稳定账户私钥（避免新注册限流），
	// 未注入时退回为本次签发生成新私钥（旧行为，适配本地/测试）。
	var acc *account
	if o.cfg.AccountKey != nil {
		acc = newAccountWithKey(o.cfg.Email, o.cfg.AccountKey)
	} else {
		var err error
		acc, err = newAccount(o.cfg.Email)
		if err != nil {
			return Certificate{}, fmt.Errorf("acme: 生成账户私钥失败: %w", err)
		}
	}

	// 2. 构建 lego Config；若指定了自定义 CA 目录（如 ZeroSSL、Pebble 测试），覆盖默认值。
	config := lego.NewConfig(acc)
	if o.cfg.CADirURL != "" {
		config.CADirURL = o.cfg.CADirURL
	}

	// 3. 创建 lego Client（建立 ACME 目录连接）。
	client, err := lego.NewClient(config)
	if err != nil {
		return Certificate{}, fmt.Errorf("acme: 创建 lego client 失败: %w", err)
	}

	// 4. 设置 DNS-01 挑战器：provider 内嵌 lego challenge.Provider，可直接传入。
	// SetDNS01Provider(p challenge.Provider, opts ...dns01.ChallengeOption) error
	//
	// DisableRecursiveNSsPropagationRequirement：关闭「递归 NS 传播预检」。集群内 manager pod 的
	// recursive resolver 是 CoreDNS，看不到刚写到公网 alidns 的 _acme-challenge TXT（缓存/不对外递归），
	// 会让预检一直超时（NS 10.233.0.10:53 did not return the expected TXT record）。关掉后预检仅以
	// 「直接查询域名权威 NS」为判据——权威 NS 即 alidns，已有该记录，可靠；且 LE 最终也按权威校验，安全。
	if err := client.Challenge.SetDNS01Provider(o.provider,
		dns01.DisableRecursiveNSsPropagationRequirement(),
	); err != nil {
		return Certificate{}, fmt.Errorf("acme: 设置 DNS-01 provider 失败: %w", err)
	}

	// 5. 向 CA 注册账户，lego v5 签名为 Register(ctx, RegisterOptions) (*acme.ExtendedAccount, error)。
	// TermsOfServiceAgreed=true 是必须的，否则 CA 拒绝注册。
	reg, err := client.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return Certificate{}, fmt.Errorf("acme: 注册 ACME 账户失败: %w", err)
	}
	// 将注册结果写回账户结构，后续签发请求需携带已注册的账户 URL。
	acc.registration = reg

	// 6. 请求证书签发。Bundle=true 使返回的 Certificate 字节包含完整证书链（Leaf + Intermediate）。
	// KeyType 必填：lego v5 将私钥类型移到 ObtainRequest（NewConfig 不再设默认），
	// 不设会报 "the key type is missing"。用 RSA2048 兼容性最广。
	// Obtain(ctx, ObtainRequest) (*Resource, error) — lego v5 签名。
	resource, err := client.Certificate.Obtain(ctx, certificate.ObtainRequest{
		Domains: domains,
		KeyType: certcrypto.RSA2048,
		Bundle:  true,
	})
	if err != nil {
		return Certificate{}, fmt.Errorf("acme: 签发证书失败: %w", err)
	}

	// 7. 构建结果；尝试从 PEM 解析叶证书到期时间，解析失败不阻断主流程（NotAfter=0）。
	cert := Certificate{
		CertPEM: resource.Certificate,
		KeyPEM:  resource.PrivateKey,
	}
	if na, ok := parseCertNotAfter(resource.Certificate); ok {
		cert.NotAfter = na
	}
	return cert, nil
}

// parseCertNotAfter 从 PEM 编码的证书链中解析第一张（叶）证书的到期时间 Unix 秒。
// 解析失败时返回 (0, false)，不影响主流程。
func parseCertNotAfter(certPEM []byte) (int64, bool) {
	// pem.Decode 只返回第一个 PEM 块（即叶证书），对链式 PEM 足够。
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return 0, false
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return 0, false
	}
	return leaf.NotAfter.Unix(), true
}
