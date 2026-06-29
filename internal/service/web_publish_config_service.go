// Package service 的 web-publish 配置编排服务。
// 负责平台管理员对企业 web-publish 能力的配置（DNS provider、凭证加密、配额）和
// 开通/停用操作（写状态机 + 派发异步 provisioning job）。
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	null "github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/dnsprovider"
	"oc-manager/internal/store/sqlc"
)

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
}

// NewWebPublishConfigService 创建 WebPublishConfigService。
// cipher 必须已用 32 字节 master_key 初始化，用于加密 DNS 凭证。
func NewWebPublishConfigService(store WebPublishConfigStore, notifier JobNotifier, cipher *auth.Cipher) *WebPublishConfigService {
	return &WebPublishConfigService{store: store, notifier: notifier, cipher: cipher}
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
	// 权限检查：仅平台管理员可配置 web-publish。
	if !auth.CanManageWebPublishConfig(p) {
		return ErrForbidden
	}

	// 校验 DNS provider 是否在已知枚举白名单内，防止写入非法值。
	if !dnsprovider.ProviderType(in.DNSProvider).Valid() {
		return fmt.Errorf("不支持的 DNS provider: %s", in.DNSProvider)
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
//  3. 创建 web_publish_provision 异步 job（payload 含 org_id，priority 100，最多尝试 5 次）；
//  4. 即时 notifier.Enqueue——失败非致命（scheduler 周期兜底），仅忽略错误。
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
