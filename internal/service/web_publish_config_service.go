// Package service 的 web-publish 配置编排服务。
// 负责平台管理员对企业 web-publish 能力的配置（DNS provider、凭证加密、配额）和
// 开通/停用操作（写状态机 + 派发异步 provisioning job）。
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	null "github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/dnsprovider"
	"oc-manager/internal/store/sqlc"
)

// ErrWebPublishNotConfigured 表示企业从未配置过 web-publish（org_web_publish_config 无该企业行）。
// 与 ErrWebPublishNotProvisioned（已配置但开通流程未就绪）语义不同：本错误是「尚未配置」的
// 正常空态，handler 据此返回 200 + null body，让前端展示「未配置」初始表单而非报错
// （与前端 WebPublishConfigResult | null 契约一致）。
var ErrWebPublishNotConfigured = errors.New("企业未配置 web-publish")

// WebPublishConfigResult 是 Get 返回的脱敏配置视图。
// 凭证密文绝不出现在此结构体中；证书状态字段供前端展示当前续签进度。
type WebPublishConfigResult struct {
	// OrgID 是企业 ID。
	OrgID string `json:"org_id"`
	// Enabled 表示 web-publish 能力是否已由平台管理员开启。
	Enabled bool `json:"enabled"`
	// BaseDomain 是企业 web-publish 根域名（如 apps.example.com）。
	BaseDomain string `json:"base_domain"`
	// WildcardDomain 是通配域名，值为 "*." + BaseDomain。
	WildcardDomain string `json:"wildcard_domain"`
	// DNSProvider 是 DNS 服务商标识，如 alidns/tencentcloud。
	DNSProvider string `json:"dns_provider"`
	// SiteTTLDays 是站点存活天数配额。
	SiteTTLDays int32 `json:"site_ttl_days"`
	// MaxSites 是企业下最大站点数配额。
	MaxSites int32 `json:"max_sites"`
	// ProvisioningStatus 是当前开通流程状态（provisioning/ready/failed/disabled 等）。
	ProvisioningStatus string `json:"provisioning_status"`
	// ProvisioningMessage 是开通失败时的错误摘要，正常时为空串。
	ProvisioningMessage string `json:"provisioning_message,omitempty"`
	// CertStatus 是通配证书的当前状态（none/issuing/renewing/issued/failed）。
	CertStatus string `json:"cert_status"`
	// CertNotAfter 是证书到期时间；尚未签发时为 nil。
	CertNotAfter *time.Time `json:"cert_not_after,omitempty"`
	// CertLastIssuedAt 是最近一次首签成功时间；尚未首签时为 nil。
	CertLastIssuedAt *time.Time `json:"cert_last_issued_at,omitempty"`
	// CertLastRenewedAt 是最近一次续签成功时间；从未续签时为 nil。
	CertLastRenewedAt *time.Time `json:"cert_last_renewed_at,omitempty"`
	// CertMessage 是证书签发失败时的错误摘要，正常时为空串。
	CertMessage string `json:"cert_message,omitempty"`
}

// WebPublishConfigStore 抽象 WebPublishConfigService 需要的存储查询能力。
// 故意最小化接口，只包含本服务实际调用的方法，避免强依赖具体 Queries 类型。
type WebPublishConfigStore interface {
	// GetWebPublishConfig 按企业 ID 查配置；不存在返回 sql.ErrNoRows。
	GetWebPublishConfig(ctx context.Context, orgID string) (sqlc.OrgWebPublishConfig, error)
	// UpsertWebPublishConfig 写入或更新企业 web-publish 基础配置（base_domain/provider/凭证/配额）。
	// 不触碰 provisioning_status 与 cert_* 状态字段，那些由状态机维护。
	UpsertWebPublishConfig(ctx context.Context, arg sqlc.UpsertWebPublishConfigParams) error
	// SetWebPublishEnabled 仅更新 enabled 与 provisioning_status 两列。
	SetWebPublishEnabled(ctx context.Context, arg sqlc.SetWebPublishEnabledParams) error
	// CreateJob 插入异步任务记录，供 scheduler / worker 消费。
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
}

// WebPublishConfigInput 是 Configure 方法的输入参数。
// 字段对应 org_web_publish_config 表的可写列（凭证明文在 service 层加密后落库）。
type WebPublishConfigInput struct {
	// OrgID 是目标企业 ID。
	OrgID string
	// BaseDomain 是企业 web-publish 的根域名（如 apps.example.com）。
	BaseDomain string
	// DNSProvider 是 DNS provider 枚举值，必须在 dnsprovider.ProviderType 白名单内。
	DNSProvider string
	// Credentials 是 DNS provider 凭证明文 map（如 access_key_id / access_key_secret）。
	// 为空时不更新凭证密文字段（null.String{Valid:false}），便于仅修改配额等场景。
	Credentials map[string]string
	// SiteTTLDays 是站点 TLS 证书/DNS 记录的 TTL 天数；<=0 时默认填 7。
	SiteTTLDays int
	// MaxSites 是企业下最大站点数；<=0 时默认填 20。
	MaxSites int
}

// WebPublishConfigService 编排企业 web-publish 能力的配置与开通/停用。
//
// 设计约束：
//   - Configure 负责写 DNS 配置（provider + 凭证密文 + 配额），凭证明文绝不出库；
//   - Enable 负责触发开通（写状态机 + 派发 provisioning job + 即时通知 worker）；
//   - Disable 负责停用（写状态机）；
//   - 所有写操作均先检查 CanManageWebPublishConfig——仅平台管理员可调用。
type WebPublishConfigService struct {
	store    WebPublishConfigStore
	notifier JobNotifier // JobNotifier 接口复用自 runtime_operation_service.go
	cipher   *auth.Cipher
	// devSelfSignedCert 反映平台是否开启本地/dev 自签证书模式（来自 config.WebPublish.DevSelfSignedCert）。
	// 仅当其为 true 时，才允许选用 dnsprovider.ProviderLocal（本地调试占位 provider）。生产恒为 false。
	devSelfSignedCert bool
}

// NewWebPublishConfigService 创建 WebPublishConfigService。
// cipher 必须已用 32 字节 master_key 初始化，用于加密 DNS 凭证。
// devSelfSignedCert 透传平台 dev 自签开关，用于 gate 「local」provider 的可用性。
func NewWebPublishConfigService(store WebPublishConfigStore, notifier JobNotifier, cipher *auth.Cipher, devSelfSignedCert bool) *WebPublishConfigService {
	return &WebPublishConfigService{store: store, notifier: notifier, cipher: cipher, devSelfSignedCert: devSelfSignedCert}
}

// Configure 写入企业 web-publish 基础配置，仅平台管理员可调用。
//
// 流程：
//  1. 权限检查（CanManageWebPublishConfig）；
//  2. 校验 DNSProvider 白名单；
//  3. 若 Credentials 非空，JSON 序列化后 AES-GCM 加密，明文不落库；
//  4. SiteTTLDays / MaxSites 补默认值；
//  5. 调用 UpsertWebPublishConfig（ON DUPLICATE KEY UPDATE，不触动 provisioning/cert 状态机）。
func (s *WebPublishConfigService) Configure(ctx context.Context, p auth.Principal, in WebPublishConfigInput) error {
	// 权限检查：平台管理员可配置任意企业；企业管理员仅可配置自己所属企业。
	if !auth.CanConfigureWebPublish(p, in.OrgID) {
		return ErrForbidden
	}
	// 企业管理员（非平台管理员）只能在平台管理员「已开通」之后才配置：
	// 读取现有配置，要求 enabled=true，否则视为越权（防止绕过开通闸自行初始化/在停用态改配置）。
	// 平台管理员不受此限（含首次创建配置行）。
	if !auth.CanManageWebPublishConfig(p) {
		cfg, err := s.store.GetWebPublishConfig(ctx, in.OrgID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrForbidden
			}
			return fmt.Errorf("查询企业 %s web-publish 配置失败: %w", in.OrgID, err)
		}
		if !cfg.Enabled {
			return ErrForbidden
		}
	}

	// 校验 DNS provider 是否在已知枚举白名单内，防止写入非法值。
	if !dnsprovider.ProviderType(in.DNSProvider).Valid() {
		return fmt.Errorf("不支持的 DNS provider: %s", in.DNSProvider)
	}
	// 「local」是仅供本地 dev 调试的占位 provider：只有平台开启 dev_self_signed_cert 时才允许选用，
	// 否则（生产）拒绝，避免误选导致真实签发链路（factory.New 不支持 local）失败。
	if dnsprovider.ProviderType(in.DNSProvider) == dnsprovider.ProviderLocal && !s.devSelfSignedCert {
		return fmt.Errorf("DNS provider %q 仅在本地 dev 自签模式下可用", in.DNSProvider)
	}

	// 凭证加密：明文 map → JSON → AES-GCM 密文 → base64 字符串 → null.String。
	// Credentials 为空时使用 null.String{} 表示"不更新"，避免覆盖已有凭证。
	var credCiphertext null.String
	if len(in.Credentials) > 0 {
		raw, err := json.Marshal(in.Credentials)
		if err != nil {
			return fmt.Errorf("序列化 DNS 凭证失败: %w", err)
		}
		enc, err := s.cipher.Encrypt(raw)
		if err != nil {
			return fmt.Errorf("加密 DNS 凭证失败: %w", err)
		}
		credCiphertext = null.StringFrom(enc)
	}

	// 补默认值：SiteTTLDays 默认 7 天，MaxSites 默认 20 个。
	ttl := in.SiteTTLDays
	if ttl <= 0 {
		ttl = 7
	}
	maxSites := in.MaxSites
	if maxSites <= 0 {
		maxSites = 20
	}

	// Upsert：首次写入时创建行，后续更新时覆盖可配置字段，不触碰 provisioning_status 与 cert_*。
	return s.store.UpsertWebPublishConfig(ctx, sqlc.UpsertWebPublishConfigParams{
		OrgID:                    in.OrgID,
		BaseDomain:               in.BaseDomain,
		DnsProvider:              in.DNSProvider,
		DnsCredentialsCiphertext: credCiphertext,
		SiteTtlDays:              int32(ttl),
		MaxSites:                 int32(maxSites),
	})
}

// Enable 开通企业 web-publish 能力，仅平台管理员可调用。
//
// 流程：
//  1. 权限检查；
//  2. 调用 SetWebPublishEnabled（enabled=true, provisioning_status=provisioning）；
//  3. 通过 enqueueProvision 创建并通知 provisioning job。
func (s *WebPublishConfigService) Enable(ctx context.Context, p auth.Principal, orgID string) error {
	// 权限检查：仅平台管理员可开通 web-publish。
	if !auth.CanManageWebPublishConfig(p) {
		return ErrForbidden
	}

	// 置开通中状态，enabled=true 表示平台侧已授权，provisioning 表示 worker 尚在处理。
	if err := s.store.SetWebPublishEnabled(ctx, sqlc.SetWebPublishEnabledParams{
		Enabled:            true,
		ProvisioningStatus: domain.ProvisioningInProgress,
		OrgID:              orgID,
	}); err != nil {
		return fmt.Errorf("设置开通状态失败: %w", err)
	}

	return s.enqueueProvision(ctx, orgID)
}

// enqueueProvision 构造并持久化一个 web_publish_provision 异步 job，
// 然后即时通知 worker 入队（失败非致命，scheduler 周期扫库兜底）。
// 被 Enable、RetryProvision 与 EnqueueProvision 复用，避免重复代码。
func (s *WebPublishConfigService) enqueueProvision(ctx context.Context, orgID string) error {
	// 构造 provisioning job payload：包含 org_id 供 worker 查配置并执行 DNS + 证书 + Ingress 开通。
	payload, err := json.Marshal(map[string]string{"org_id": orgID})
	if err != nil {
		return fmt.Errorf("序列化 provisioning job payload 失败: %w", err)
	}

	// 创建 provisioning job，5 次最大尝试覆盖 DNS/证书偶发失败场景。
	jobID := newUUID()
	if err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		ID:          jobID,
		Type:        domain.JobTypeWebPublishProvision,
		Priority:    100,
		RunAfter:    time.Now(),
		MaxAttempts: 5,
		PayloadJson: payload,
	}); err != nil {
		return fmt.Errorf("创建 provisioning job 失败: %w", err)
	}

	// 即时通知 worker 入队；失败不阻塞响应——scheduler 周期性扫库兜底。
	if s.notifier != nil {
		_ = s.notifier.Enqueue(ctx, jobID)
	}
	return nil
}

// EnqueueProvision 为指定企业入队一个 web_publish_provision job，供续签巡检器调用。
// 不做权限检查——调用方（CertRenewalChecker）为内部系统组件，已在上层保证合法性。
func (s *WebPublishConfigService) EnqueueProvision(ctx context.Context, orgID string) error {
	return s.enqueueProvision(ctx, orgID)
}

// RetryProvision 为指定企业手动重试 provision，仅平台管理员可调用。
// 用于证书签发/续签失败后由平台管理员触发手动重试，不修改 provisioning/enabled 状态。
func (s *WebPublishConfigService) RetryProvision(ctx context.Context, p auth.Principal, orgID string) error {
	// 权限检查：仅平台管理员可触发手动重试。
	if !auth.CanManageWebPublishConfig(p) {
		return ErrForbidden
	}
	return s.enqueueProvision(ctx, orgID)
}

// Get 查询企业 web-publish 配置的脱敏视图，仅拥有 CanViewOrg 权限的主体可调用。
// 脱敏原则：DNS 凭证密文不出现在返回结果中；证书状态字段全量返回供前端展示。
func (s *WebPublishConfigService) Get(ctx context.Context, p auth.Principal, orgID string) (WebPublishConfigResult, error) {
	// 权限检查：平台管理员或归属企业的成员均可查看配置状态。
	if !auth.CanViewOrg(p, orgID) {
		return WebPublishConfigResult{}, ErrForbidden
	}

	// 从存储层读取企业配置。无配置行是正常空态（企业从未配置过 web-publish），
	// 映射为可识别的 ErrWebPublishNotConfigured，由 handler 返回 200 + null，
	// 而非裹进通用错误落到 500。其余错误才是真正的存储异常。
	cfg, err := s.store.GetWebPublishConfig(ctx, orgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WebPublishConfigResult{}, ErrWebPublishNotConfigured
		}
		return WebPublishConfigResult{}, fmt.Errorf("查询企业 %s web-publish 配置失败: %w", orgID, err)
	}

	// 将 null.Time 映射为 *time.Time：Valid=true 时返回时间指针，否则返回 nil。
	nullTimePtr := func(t null.Time) *time.Time {
		if !t.Valid {
			return nil
		}
		v := t.Time
		return &v
	}

	return WebPublishConfigResult{
		OrgID:               cfg.OrgID,
		Enabled:             cfg.Enabled,
		BaseDomain:          cfg.BaseDomain,
		WildcardDomain:      "*." + cfg.BaseDomain,
		DNSProvider:         cfg.DnsProvider,
		SiteTTLDays:         cfg.SiteTtlDays,
		MaxSites:            cfg.MaxSites,
		ProvisioningStatus:  cfg.ProvisioningStatus,
		ProvisioningMessage: cfg.ProvisioningMessage.String,
		CertStatus:          cfg.CertStatus,
		CertNotAfter:        nullTimePtr(cfg.CertNotAfter),
		CertLastIssuedAt:    nullTimePtr(cfg.CertLastIssuedAt),
		CertLastRenewedAt:   nullTimePtr(cfg.CertLastRenewedAt),
		CertMessage:         cfg.CertMessage.String,
	}, nil
}

// Disable 停用企业 web-publish 能力，仅平台管理员可调用。
// 仅更新 enabled=false + provisioning_status=disabled，不删除配置数据，
// 便于将来重新开通时复用已有配置。
func (s *WebPublishConfigService) Disable(ctx context.Context, p auth.Principal, orgID string) error {
	// 权限检查：仅平台管理员可停用 web-publish。
	if !auth.CanManageWebPublishConfig(p) {
		return ErrForbidden
	}

	// 置停用状态，不派发 job（停用由 service 直接写库，worker 无需感知）。
	return s.store.SetWebPublishEnabled(ctx, sqlc.SetWebPublishEnabledParams{
		Enabled:            false,
		ProvisioningStatus: domain.ProvisioningDisabled,
		OrgID:              orgID,
	})
}
