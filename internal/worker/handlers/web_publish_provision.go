// Package handlers — web_publish_provision handler
// 负责企业 web-publish 能力的一次性开通状态机：
//  1. 解密 DNS 凭证
//  2. 通过 ACME 签发通配证书
//  3. 写入 TLS Secret 到 k8s
//  4. 建立通配 Ingress
//  5. 更新数据库 provisioning/cert 状态
//
// 任一步骤失败都置 failed 并返回错误，让 worker backoff 重试。

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	null "github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/acme"
	"oc-manager/internal/store/sqlc"
)

// WebPublishProvisionStore 是 handler 需要的最小数据访问能力接口。
// 从 Queries 截取，便于单测注入 fake。
type WebPublishProvisionStore interface {
	// GetWebPublishConfig 取企业 web-publish 配置；不存在返回 sql.ErrNoRows。
	GetWebPublishConfig(ctx context.Context, orgID string) (sqlc.OrgWebPublishConfig, error)
	// SetWebPublishProvisioning 更新 provisioning 结果：状态 + 摘要 + cert_secret_name。
	SetWebPublishProvisioning(ctx context.Context, arg sqlc.SetWebPublishProvisioningParams) error
	// SetWebPublishCertStatus 更新证书状态与时间戳。
	SetWebPublishCertStatus(ctx context.Context, arg sqlc.SetWebPublishCertStatusParams) error
}

// CertProvisionInput 是签发通配证书所需的输入参数。
type CertProvisionInput struct {
	// ProviderType 是 DNS provider 标识，如 alidns/tencentcloud 等。
	ProviderType string
	// Credentials 是从 DnsCredentialsCiphertext 解密后的凭证 KV 对。
	Credentials map[string]string
	// BaseDomain 是企业基础域名，证书将覆盖 *.BaseDomain。
	BaseDomain string
	// IngressIP 是通配 Ingress 对外暴露的公网 IP，用于签发前 DNS A 记录检查。
	IngressIP string
}

// CertProvisioner 抽象证书签发动作，便于生产实现与单测 fake 解耦。
type CertProvisioner interface {
	Provision(ctx context.Context, in CertProvisionInput) (acme.Certificate, error)
}

// WildcardIngressParams 是创建通配 Ingress 所需的参数。
type WildcardIngressParams struct {
	// Name 是 Ingress 资源名称。
	Name string
	// BaseDomain 是企业基础域名，Ingress 规则匹配 *.BaseDomain。
	BaseDomain string
	// TLSSecretName 是存放通配证书的 k8s TLS Secret 名称。
	TLSSecretName string
	// IngressClassName 指定使用的 Ingress Controller（如 traefik/nginx）。
	IngressClassName string
	// BackendService 是通配 Ingress 默认后端 Service 名称。
	BackendService string
	// BackendPort 是后端 Service 端口。
	BackendPort int32
}

// ClusterApplier 抽象 k8s 集群副作用，便于单测注入 fake。
type ClusterApplier interface {
	// ApplyTLSSecret 幂等写入或更新 k8s TLS Secret。
	ApplyTLSSecret(ctx context.Context, name string, certPEM, keyPEM []byte) error
	// ApplyWildcardIngress 幂等创建或更新通配 Ingress。
	ApplyWildcardIngress(ctx context.Context, params WildcardIngressParams) error
}

// WebPublishProvisionConfig 是 handler 的平台级静态配置，来自启动参数/configmap。
type WebPublishProvisionConfig struct {
	// IngressPublicIP 是通配 Ingress 对外暴露的公网 IP（DNS A 记录目标）。
	IngressPublicIP string
	// IngressClassName 是 Ingress Controller 类名（留空则不设置 ingressClassName）。
	IngressClassName string
	// BackendService 是通配 Ingress 的默认后端 Service（留空则不设置 defaultBackend）。
	BackendService string
	// BackendPort 是后端 Service 端口（BackendService 非空时有效）。
	BackendPort int32
}

// provisionPayload 是 web_publish_provision job 的 payload 结构。
type provisionPayload struct {
	OrgID string `json:"org_id"`
}

// WebPublishProvisionHandler 实现企业 web-publish 能力开通状态机。
type WebPublishProvisionHandler struct {
	store   WebPublishProvisionStore
	prov    CertProvisioner
	applier ClusterApplier
	cipher  *auth.Cipher
	cfg     WebPublishProvisionConfig
}

// NewWebPublishProvisionHandler 构造 WebPublishProvisionHandler。
func NewWebPublishProvisionHandler(
	store WebPublishProvisionStore,
	prov CertProvisioner,
	applier ClusterApplier,
	cipher *auth.Cipher,
	cfg WebPublishProvisionConfig,
) *WebPublishProvisionHandler {
	return &WebPublishProvisionHandler{
		store:   store,
		prov:    prov,
		applier: applier,
		cipher:  cipher,
		cfg:     cfg,
	}
}

// 编译期断言：Handle 满足 HandlerFunc 签名。
var _ HandlerFunc = (*WebPublishProvisionHandler)(nil).Handle

// Handle 执行 web-publish 开通状态机。
// payload.org_id → 查配置 → 解密凭证 → 签证书 → 写 TLS Secret → 建 Ingress → 置 ready/issued。
// 任一步失败：置 cert=failed + provisioning=failed 并返回错误（worker backoff 重试）。
func (h *WebPublishProvisionHandler) Handle(ctx context.Context, job sqlc.Job) error {
	// 反序列化 payload 取 org_id
	var payload provisionPayload
	if err := json.Unmarshal(job.PayloadJson, &payload); err != nil {
		return fmt.Errorf("解析 web_publish_provision payload 失败: %w", err)
	}
	orgID := payload.OrgID

	// 读取企业配置
	cfg, err := h.store.GetWebPublishConfig(ctx, orgID)
	if err != nil {
		return fmt.Errorf("读取企业 %s web-publish 配置失败: %w", orgID, err)
	}

	// 确定通配证书 TLS Secret / Ingress 的资源名。
	// cert_secret_name 列默认空串，Configure/Enable 路径从不赋值，首次 provisioning 时为空——
	// 直接传空名给 ApplyTLSSecret/ApplyWildcardIngress 会被 k8s 拒绝并永久卡死。
	// 这里在 handler 内按企业基础域名确定性派生（k8s 资源名：小写字母数字与连字符），
	// 重试时同一 baseDomain 派生同名，保证幂等可恢复；成功后再写回 DB。
	secretName := cfg.CertSecretName
	if secretName == "" {
		secretName = "wildcard-" + strings.ReplaceAll(cfg.BaseDomain, ".", "-")
	}

	// 标记证书状态为 issuing，表示本次开通已启动。
	// 此处写库失败只返回 error 交给 worker 重试，刻意不调 fail() 置 failed：
	// DB 不可用时 fail() 自己也写不进，标 failed 既无意义又会留下不准确状态。
	if err := h.setCertStatus(ctx, orgID, domain.CertStatusIssuing, 0, false, null.Time{}); err != nil {
		return err
	}

	// 解密 DNS 凭证
	creds, err := h.decryptCredentials(cfg)
	if err != nil {
		return h.fail(ctx, orgID, secretName, fmt.Errorf("解密 DNS 凭证失败: %w", err))
	}

	// 调用证书签发器
	cert, err := h.prov.Provision(ctx, CertProvisionInput{
		ProviderType: cfg.DnsProvider,
		Credentials:  creds,
		BaseDomain:   cfg.BaseDomain,
		IngressIP:    h.cfg.IngressPublicIP,
	})
	if err != nil {
		return h.fail(ctx, orgID, secretName, fmt.Errorf("签发通配证书失败: %w", err))
	}

	// 写入 TLS Secret
	if err := h.applier.ApplyTLSSecret(ctx, secretName, cert.CertPEM, cert.KeyPEM); err != nil {
		return h.fail(ctx, orgID, secretName, fmt.Errorf("写入 TLS Secret 失败: %w", err))
	}

	// 建通配 Ingress（名称与证书 secret 同名，保持一致）
	if err := h.applier.ApplyWildcardIngress(ctx, WildcardIngressParams{
		Name:             secretName,
		BaseDomain:       cfg.BaseDomain,
		TLSSecretName:    secretName,
		IngressClassName: h.cfg.IngressClassName,
		BackendService:   h.cfg.BackendService,
		BackendPort:      h.cfg.BackendPort,
	}); err != nil {
		return h.fail(ctx, orgID, secretName, fmt.Errorf("建通配 Ingress 失败: %w", err))
	}

	// 全部成功：更新 cert 状态为 issued，记录到期时间与签发时间。
	// 首次签发不设置 cert_last_renewed_at（用 null.Time{}，COALESCE 保留原值）。
	//
	// 注意：到这里通配证书 Secret 与 Ingress 都已真实创建成功，集群已就绪。
	// 若此处或下面写 ready 的 DB 调用失败，刻意只返回 error 交给 worker 重试，
	// 绝不调 fail() 置 failed——重试是幂等的（重签 + 重 apply Secret/Ingress 安全），
	// 最终会把状态收敛到 issued/ready；而若标 failed 会造成「集群已就绪却显示失败」
	// 的错误观测。这是有意设计，不是漏掉 fail()。
	if err := h.setCertStatus(ctx, orgID, domain.CertStatusIssued, cert.NotAfter, true, null.Time{}); err != nil {
		return err
	}

	// 更新 provisioning 状态为 ready，并把派生的 secret 名写回 DB（同上：写失败只返回 error 重试，不置 failed）
	if err := h.store.SetWebPublishProvisioning(ctx, sqlc.SetWebPublishProvisioningParams{
		ProvisioningStatus:  domain.ProvisioningReady,
		ProvisioningMessage: null.String{},
		CertSecretName:      secretName,
		OrgID:               orgID,
	}); err != nil {
		return fmt.Errorf("更新 provisioning 状态为 ready 失败: %w", err)
	}

	return nil
}

// fail 在任一步骤出错时置 cert=failed + provisioning=failed，然后返回原始错误。
// 返回原始错误（而非 fail 内部错误）是为了让 worker 能对业务错误做 backoff/重试。
func (h *WebPublishProvisionHandler) fail(ctx context.Context, orgID, certSecretName string, cause error) error {
	msg := null.StringFrom(cause.Error())

	// 置 cert=failed
	_ = h.store.SetWebPublishCertStatus(ctx, sqlc.SetWebPublishCertStatusParams{
		CertStatus:  domain.CertStatusFailed,
		CertMessage: msg,
		OrgID:       orgID,
	})

	// 置 provisioning=failed，保留 cert_secret_name 不变
	_ = h.store.SetWebPublishProvisioning(ctx, sqlc.SetWebPublishProvisioningParams{
		ProvisioningStatus:  domain.ProvisioningFailed,
		ProvisioningMessage: msg,
		CertSecretName:      certSecretName,
		OrgID:               orgID,
	})

	return cause
}

// setCertStatus 更新证书状态与相关时间字段。
// 当 recordIssuedAt=true 时写入 cert_last_issued_at=now()；
// 首次签发传 null.Time{} 给 CertLastRenewedAt，COALESCE 会保留原值（通常为 NULL）。
func (h *WebPublishProvisionHandler) setCertStatus(
	ctx context.Context,
	orgID string,
	status string,
	notAfterUnix int64,
	recordIssuedAt bool,
	renewedAt null.Time,
) error {
	params := sqlc.SetWebPublishCertStatusParams{
		CertStatus:        status,
		CertLastRenewedAt: renewedAt,
		OrgID:             orgID,
	}

	// 仅在 notAfterUnix>0 时写证书到期时间，issuing 阶段无需写
	if notAfterUnix > 0 {
		params.CertNotAfter = null.TimeFrom(time.Unix(notAfterUnix, 0).UTC())
	}

	// 签发成功时记录最近签发时间
	if recordIssuedAt {
		params.CertLastIssuedAt = null.TimeFrom(time.Now().UTC())
	}

	if err := h.store.SetWebPublishCertStatus(ctx, params); err != nil {
		return fmt.Errorf("更新证书状态 %s 失败: %w", status, err)
	}
	return nil
}

// decryptCredentials 解密 DnsCredentialsCiphertext，返回凭证 KV map。
func (h *WebPublishProvisionHandler) decryptCredentials(cfg sqlc.OrgWebPublishConfig) (map[string]string, error) {
	// 凭证密文为空说明配置不完整
	if !cfg.DnsCredentialsCiphertext.Valid || cfg.DnsCredentialsCiphertext.String == "" {
		return nil, fmt.Errorf("企业 %s 的 DNS 凭证密文为空", cfg.OrgID)
	}

	plain, err := h.cipher.Decrypt(cfg.DnsCredentialsCiphertext.String)
	if err != nil {
		return nil, fmt.Errorf("AES 解密失败: %w", err)
	}

	var creds map[string]string
	if err := json.Unmarshal(plain, &creds); err != nil {
		return nil, fmt.Errorf("凭证 JSON 解析失败: %w", err)
	}
	return creds, nil
}
