package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	mlog "oc-manager/internal/log"
	"oc-manager/internal/store/sqlc"
)

var organizationCodePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,30}[a-z0-9])$`)

// NewAPIFailureContext 描述 OrganizationService 内 new-api 调用失败的上下文，
// 供注入的 NewAPIFailureAuditor 写 audit_logs。
type NewAPIFailureContext struct {
	// ActorID 是触发外部调用的 manager 用户 ID，用于审计归因。
	ActorID string
	// ActorRole 是触发时的角色快照，避免后续角色变化影响审计解释。
	ActorRole string
	// OrgID 是失败影响到的组织 ID；组织创建早期失败时使用刚插入的 manager 行。
	OrgID string
	// Endpoint 是失败的 new-api 端点或内部步骤名称。
	Endpoint string
	// Err 是原始错误，审计辅助会做安全化处理后写入 metadata。
	Err error
}

// NewAPIFailureAuditor 抽象 new-api 失败审计写入能力，避免 service 直接依赖 audit 包
// （audit 包反向依赖 service.AuditEvent，会形成导入环）。
// *audit.NewAPIAuditHelper 通过适配器满足此接口。
type NewAPIFailureAuditor interface {
	RecordNewAPIFailure(ctx context.Context, fc NewAPIFailureContext)
}

// OrganizationVersionValidator 抽象「校验一组助手版本 id 都存在」的能力。
type OrganizationVersionValidator interface {
	ValidateAssistantVersionIDs(ctx context.Context, ids []string) ([]string, error)
}

// OrganizationStore 抽象组织管理所需的数据访问能力。
type OrganizationStore interface {
	// CreateOrganization 创建组织（:exec），写入后通过 GetOrganization 读回。
	CreateOrganization(ctx context.Context, arg sqlc.CreateOrganizationParams) error
	// SetOrganizationNewAPIUser 更新 new-api 用户信息（:exec），写入后通过 GetOrganization 读回。
	SetOrganizationNewAPIUser(ctx context.Context, arg sqlc.SetOrganizationNewAPIUserParams) error
	// CreateUser 创建用户（:exec）。
	CreateUser(ctx context.Context, arg sqlc.CreateUserParams) error
	HardDeleteOrganization(ctx context.Context, id string) error
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	ListOrganizations(ctx context.Context, arg sqlc.ListOrganizationsParams) ([]sqlc.Organization, error)
	GetOrgAdminByOrg(ctx context.Context, id null.String) (sqlc.User, error)
	// UpdateOrganizationProfile 更新组织资料（:exec），写入后通过 GetOrganization 读回。
	UpdateOrganizationProfile(ctx context.Context, arg sqlc.UpdateOrganizationProfileParams) error
	// UpdateOrganizationAICCConfig 更新企业 AICC 开通配置（:exec），写入后通过 GetOrganization 读回。
	UpdateOrganizationAICCConfig(ctx context.Context, arg sqlc.UpdateOrganizationAICCConfigParams) error
	// SetOrganizationStatus 更新组织状态（:exec），写入后通过 GetOrganization 读回。
	SetOrganizationStatus(ctx context.Context, arg sqlc.SetOrganizationStatusParams) error
}

// NewAPIUserProvisioner 抽象组织创建链路所需的 new-api 调用集合，便于测试中替换为 fake。
type NewAPIUserProvisioner interface {
	CreateUser(ctx context.Context, input newapi.CreateUserInput) (newapi.User, error)
	BootstrapUserAccessToken(ctx context.Context, username, password string) (string, error)
	DeleteUser(ctx context.Context, userID int64) error // OOS-1 孤儿清理：CreateOrganization 失败时 best-effort 调用
}

// OrganizationCredentials 是 organizations.newapi_user_credentials_ciphertext 解密后的明文。
//
// 三件套的角色：
//   - Username / Password：login 用的回退凭据，万一 access_token 被在 new-api UI 重置，
//     manager 还能 re-login 重新生成；
//   - AccessToken：日常 user-scoped 调用走的 Bearer，避免每次都 login。
type OrganizationCredentials struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	AccessToken string `json:"access_token"`
}

// OrganizationService 负责组织生命周期、new-api 用户 provisioning 和组织管理员初始化。
// 创建组织跨 manager DB 与 new-api 两个系统，失败时通过 best-effort 清理和审计降低孤儿资源风险。
type OrganizationService struct {
	// store 封装 manager 端组织和管理员账号的持久化操作。
	store OrganizationStore
	// provisioner 封装 new-api 用户创建、token bootstrap 和失败清理。
	provisioner NewAPIUserProvisioner
	// cipher 加密 organizations.newapi_user_credentials_ciphertext 中的 new-api 明文凭据。
	cipher *auth.Cipher
	// failAuditor 记录 new-api 失败；nil 时跳过审计，主要用于单元测试或最小装配。
	failAuditor NewAPIFailureAuditor
	// versionValidator 校验一组助手版本 id 都存在且未删除；未配置时禁止保存版本 allowlist。
	versionValidator OrganizationVersionValidator
	// knowledgeDatasets 在组织创建成功后预创建组织级 RAGFlow dataset；失败不回滚组织。
	knowledgeDatasets KnowledgeDatasetProvisioner
	// hashPassword 仅用于创建组织管理员，测试中可替换为快 hash。
	hashPassword PasswordHasher
}

// NewOrganizationService 构造组织服务。
// provisioner / cipher 必填；failAuditor 可为 nil（生产装配应注入满足 NewAPIFailureAuditor 的实现）。
func NewOrganizationService(store OrganizationStore, provisioner NewAPIUserProvisioner, cipher *auth.Cipher, failAuditor NewAPIFailureAuditor) *OrganizationService {
	return &OrganizationService{
		store:       store,
		provisioner: provisioner,
		cipher:      cipher,
		failAuditor: failAuditor,
		hashPassword: func(password string) (string, error) {
			return auth.HashPassword(password, auth.DefaultPasswordParams)
		},
	}
}

// SetVersionValidator 注入助手版本 allowlist 校验器。
func (s *OrganizationService) SetVersionValidator(v OrganizationVersionValidator) {
	s.versionValidator = v
}

// SetKnowledgeDatasetProvisioner 注入组织创建后的知识库 dataset 预创建能力。
func (s *OrganizationService) SetKnowledgeDatasetProvisioner(p KnowledgeDatasetProvisioner) {
	s.knowledgeDatasets = p
}

// OrganizationInput 是企业创建和更新的统一入参。
// Admin* 字段仅 CreateOrganization 使用，更新企业资料时会被忽略。
type OrganizationInput struct {
	// Name 是企业展示名。
	Name string
	// Code 是企业登录标识，创建后不可修改；仅允许小写字母、数字和短横线。
	Code string
	// ContactName 是业务联系人姓名。
	ContactName string
	// ContactPhone 是业务联系人电话。
	ContactPhone string
	// Remark 是平台管理员维护的内部备注。
	Remark string
	// CreditWarningThreshold 是企业余额预警阈值；nil 会写入 NULL，表示不设置预警阈值。
	CreditWarningThreshold *int32
	// MaxInstanceCount 是企业最多可创建的实例（应用）数；nil 写入 NULL，表示不限制。
	MaxInstanceCount *int32
	// KnowledgeQuotaBytes 是企业知识库累计容量上限，单位字节；nil 表示创建时默认 1GB，更新时保留旧值。
	KnowledgeQuotaBytes *int64
	// DefaultAppKnowledgeQuotaBytes 是该企业新建实例的默认知识库容量上限，单位字节；
	// nil 表示创建时默认 1GB、更新时保留旧值（前端负责必填校验）。
	DefaultAppKnowledgeQuotaBytes *int64
	// AssistantVersionIDs 是该企业可用的助手版本 id 列表（allowlist）。
	AssistantVersionIDs []string
	// AssistantVersionIDsSet 标记更新请求是否显式传入了 allowlist。
	AssistantVersionIDsSet bool
	// AdminUsername 是创建企业时初始化的 org_admin 账号名。
	AdminUsername string
	// AdminDisplayName 是初始化 org_admin 的显示名。
	AdminDisplayName string
	// AdminPassword 是初始化 org_admin 的明文密码，写库前会 hash。
	AdminPassword string
}

// AICCConfigInput 是平台管理员维护企业 AICC 开通状态和智能体上限的入参。
type AICCConfigInput struct {
	// Enabled 表示该企业是否可使用 AICC 子系统。
	Enabled bool
	// AgentLimit 是智能体数量上限；nil 表示不限。
	AgentLimit *int32
}

// OrganizationResult 是企业对外响应视图，包含必要的 new-api 绑定状态。
type OrganizationResult struct {
	// ID 是 manager 企业 UUID。
	ID string `json:"id"`
	// Name 是企业展示名。
	Name string `json:"name"`
	// Code 是企业登录标识，用于企业用户登录时定位企业。
	Code string `json:"code"`
	// Status 是企业状态，active / disabled 决定成员是否可登录。
	Status string `json:"status"`
	// ContactName 是业务联系人姓名。
	ContactName string `json:"contact_name,omitempty"`
	// ContactPhone 是业务联系人电话。
	ContactPhone string `json:"contact_phone,omitempty"`
	// Remark 是平台管理员维护的内部备注。
	Remark string `json:"remark,omitempty"`
	// NewAPIUserID 是企业在 new-api 侧的用户 ID，缺失时充值和用量接口不可用。
	NewAPIUserID string `json:"newapi_user_id,omitempty"`
	// CreditWarningThreshold 是企业余额预警阈值。
	CreditWarningThreshold *int32 `json:"credit_warning_threshold,omitempty"`
	// MaxInstanceCount 是企业实例数量上限；nil 表示不限制。
	MaxInstanceCount *int32 `json:"max_instance_count,omitempty"`
	// KnowledgeQuotaBytes 是企业知识库累计容量上限，单位字节。
	KnowledgeQuotaBytes int64 `json:"knowledge_quota_bytes"`
	// DefaultAppKnowledgeQuotaBytes 是该企业新建实例的默认知识库容量上限，单位字节。
	DefaultAppKnowledgeQuotaBytes int64 `json:"default_app_knowledge_quota_bytes"`
	// AdminUsername 是企业首个可用管理员账号名，用于平台管理员复制登录信息。
	AdminUsername string `json:"admin_username,omitempty"`
	// AssistantVersionIDs 是该企业可用的助手版本 id 列表（allowlist）。
	AssistantVersionIDs []string `json:"assistant_version_ids"`
	// AICCEnabled 表示企业是否已开通 AI Contact Center。
	AICCEnabled bool `json:"aicc_enabled"`
	// AICCAgentLimit 是企业 AICC 智能体数量上限；nil 表示不限。
	AICCAgentLimit *int32 `json:"aicc_agent_limit,omitempty"`
}

// CreateOrganization 创建组织：先 INSERT manager 端记录，再串联调 new-api 创业务 user 并落凭据密文。
//
// 失败路径：任何步骤报错时——
//   - 已创建的 new-api user 调 DeleteUser best-effort 清理（OOS-1）；
//   - 原失败原因 + 清理失败（如有）通过 auditHelper 落 audit_logs（OOS-3）；
//   - manager 端组织行 HardDeleteOrganization 回滚。
func (s *OrganizationService) CreateOrganization(ctx context.Context, principal auth.Principal, input OrganizationInput) (OrganizationResult, error) {
	// 组织创建会在 new-api 侧创建真实计费主体，只允许平台管理员触发。
	if principal.Role != domain.UserRolePlatformAdmin {
		return OrganizationResult{}, ErrForbidden
	}
	if s.provisioner == nil || s.cipher == nil {
		return OrganizationResult{}, fmt.Errorf("organization service 未装配 newapi provisioner / cipher，无法创建企业")
	}
	if input.AdminUsername == "" || input.AdminDisplayName == "" || input.AdminPassword == "" {
		return OrganizationResult{}, fmt.Errorf("%w: 管理员用户名、显示名和密码不能为空", ErrMemberCreateInvalid)
	}
	code, err := normalizeOrganizationCode(input.Code)
	if err != nil {
		return OrganizationResult{}, err
	}
	// 校验助手版本 allowlist 中的每个 id 都存在且未删除。
	// versionValidator 未装配时直接拒绝，避免写入未经版本目录确认的 id。
	if s.versionValidator == nil {
		return OrganizationResult{}, fmt.Errorf("版本校验器未配置，无法保存企业可用版本")
	}
	cleanVersionIDs, err := s.versionValidator.ValidateAssistantVersionIDs(ctx, input.AssistantVersionIDs)
	if err != nil {
		return OrganizationResult{}, err
	}
	versionIDsJSON, err := json.Marshal(cleanVersionIDs)
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("序列化企业可用版本失败: %w", err)
	}
	adminPasswordHash, err := s.hashPassword(input.AdminPassword)
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("生成管理员密码 hash 失败: %w", err)
	}
	knowledgeQuotaBytes, err := normalizeKnowledgeQuotaBytes(input.KnowledgeQuotaBytes)
	if err != nil {
		return OrganizationResult{}, err
	}
	// 个人知识库默认配额：与企业知识库同样走 normalize（nil→1GB、>0 校验），前端保证必填。
	defaultAppKnowledgeQuotaBytes, err := normalizeKnowledgeQuotaBytes(input.DefaultAppKnowledgeQuotaBytes)
	if err != nil {
		return OrganizationResult{}, err
	}

	// CreateOrganization 为 :exec；预先生成 ID，写入后通过 GetOrganization 读回。
	orgID := newUUID()
	if err := s.store.CreateOrganization(ctx, sqlc.CreateOrganizationParams{
		ID:                            orgID,
		Name:                          input.Name,
		Code:                          code,
		Status:                        domain.StatusActive,
		ContactName:                   nullStr(input.ContactName),
		ContactPhone:                  nullStr(input.ContactPhone),
		Remark:                        nullStr(input.Remark),
		CreditWarningThreshold:        nullIntFromInt32Ptr(input.CreditWarningThreshold),
		MaxInstanceCount:              nullIntFromInt32Ptr(input.MaxInstanceCount),
		KnowledgeQuotaBytes:           knowledgeQuotaBytes,
		DefaultAppKnowledgeQuotaBytes: defaultAppKnowledgeQuotaBytes,
		AssistantVersionIds:           versionIDsJSON,
	}); err != nil {
		if isMySQLUniqueViolation(err) {
			return OrganizationResult{}, fmt.Errorf("%w: 企业名称或企业标识已存在", ErrConflict)
		}
		return OrganizationResult{}, fmt.Errorf("创建企业失败: %w", err)
	}
	org, err := s.store.GetOrganization(ctx, orgID)
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("读取新建企业失败: %w", err)
	}

	// 失败时回滚刚刚 INSERT 的 manager 行；rollback 自身失败只记入返回错误，不掩盖原因。
	commit, createdUserID, err := s.provisionNewAPIUser(ctx, &org)
	if err != nil {
		// OOS-1：best-effort 调 DeleteUser 清理 new-api 孤儿 user。
		// 使用独立的短超时 ctx，避免原 ctx 取消导致清理也被中止。
		// slog.WarnContext 仍传原 ctx，让 trace_id 自动注入（A-4 SetRequestIDExtractor 已配）。
		if createdUserID != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			if delErr := s.provisioner.DeleteUser(cleanupCtx, *createdUserID); delErr != nil {
				slog.WarnContext(ctx, "best-effort 清理 newapi user 失败",
					"newapi_user_id", *createdUserID,
					mlog.Err(delErr),
				)
				// OOS-3：DeleteUser 自身失败也写审计。
				// 此处刻意不带 OrgID：该组织行随后会被 HardDeleteOrganization 回滚删除，
				// 若审计 org_id 指向它，audit_logs_org_id_fkey 外键（无 ON DELETE CASCADE）
				// 反过来会阻止回滚删除，导致组织脏行残留并返回 500。
				if s.failAuditor != nil {
					s.failAuditor.RecordNewAPIFailure(ctx, NewAPIFailureContext{
						ActorID:   principal.UserID,
						ActorRole: principal.Role,
						Endpoint:  fmt.Sprintf("DELETE /api/user/%d", *createdUserID),
						Err:       delErr,
					})
				}
			}
		}
		// OOS-3：原失败原因写审计（区别于 DeleteUser 失败）。
		// 同上：不带 OrgID，避免审计记录阻止后续组织行回滚。
		if s.failAuditor != nil {
			s.failAuditor.RecordNewAPIFailure(ctx, NewAPIFailureContext{
				ActorID:   principal.UserID,
				ActorRole: principal.Role,
				Endpoint:  "CreateOrganization",
				Err:       err,
			})
		}
		// manager 端回滚保留
		if rollbackErr := s.store.HardDeleteOrganization(ctx, org.ID); rollbackErr != nil {
			return OrganizationResult{}, fmt.Errorf("%w；回滚企业行失败: %v", err, rollbackErr)
		}
		return OrganizationResult{}, err
	}
	org = commit
	// CreateUser 为 :exec；管理员 OrgID 是组织 ID（null.String）。
	adminUserID := newUUID()
	if err := s.store.CreateUser(ctx, sqlc.CreateUserParams{
		ID:           adminUserID,
		OrgID:        null.StringFrom(org.ID),
		Username:     input.AdminUsername,
		PasswordHash: adminPasswordHash,
		DisplayName:  input.AdminDisplayName,
		Role:         domain.UserRoleOrgAdmin,
		Status:       domain.StatusActive,
	}); err != nil {
		if createdUserID != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			if delErr := s.provisioner.DeleteUser(cleanupCtx, *createdUserID); delErr != nil {
				slog.WarnContext(ctx, "best-effort 清理 newapi user 失败",
					"newapi_user_id", *createdUserID,
					mlog.Err(delErr),
				)
				// 不带 OrgID：组织行随后会被 HardDeleteOrganization 回滚删除，
				// 审计 org_id 指向它会因外键约束阻止回滚（详见 provision 失败分支注释）。
				if s.failAuditor != nil {
					s.failAuditor.RecordNewAPIFailure(ctx, NewAPIFailureContext{
						ActorID:   principal.UserID,
						ActorRole: principal.Role,
						Endpoint:  fmt.Sprintf("DELETE /api/user/%d", *createdUserID),
						Err:       delErr,
					})
				}
			}
		}
		wrappedErr := fmt.Errorf("创建企业管理员失败: %w", err)
		// 不带 OrgID：同上，避免审计记录阻止后续组织行回滚。
		if s.failAuditor != nil {
			s.failAuditor.RecordNewAPIFailure(ctx, NewAPIFailureContext{
				ActorID:   principal.UserID,
				ActorRole: principal.Role,
				Endpoint:  "CreateOrganizationAdmin",
				Err:       wrappedErr,
			})
		}
		if rollbackErr := s.store.HardDeleteOrganization(ctx, org.ID); rollbackErr != nil {
			return OrganizationResult{}, fmt.Errorf("%w；回滚企业行失败: %v", wrappedErr, rollbackErr)
		}
		return OrganizationResult{}, wrappedErr
	}
	result := toOrganizationResult(org)
	result.AdminUsername = input.AdminUsername
	if s.knowledgeDatasets != nil {
		if _, err := s.knowledgeDatasets.EnsureOrgDataset(ctx, org); err != nil {
			slog.WarnContext(ctx, "预创建企业 RAGFlow dataset 失败", slog.String(mlog.KeyOrgID, org.ID), mlog.Err(err))
		}
	}
	return result, nil
}

// normalizeOrganizationCode 统一企业标识格式，避免大小写或空白导致同一标识多种写法。
func normalizeOrganizationCode(value string) (string, error) {
	code := strings.ToLower(strings.TrimSpace(value))
	if !organizationCodePattern.MatchString(code) {
		return "", fmt.Errorf("%w: 企业标识必须为 3-32 位小写字母、数字或短横线，且不能以短横线开头或结尾", ErrMemberCreateInvalid)
	}
	return code, nil
}

// isMySQLUniqueViolation 判断底层 MySQL 错误是否为唯一约束冲突（error 1062）。
func isMySQLUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "Duplicate entry") || strings.Contains(err.Error(), "duplicate key")
}

// provisionNewAPIUser 在 new-api 创建对应业务 user，登录拿 access_token，加密落库。
//
// 返回值第二个 *int64 是"已创建的 new-api user_id"——
//   - CreateUser 之前任意失败 → nil（无孤儿）
//   - CreateUser 之后任意失败 → 非 nil（调用方负责 best-effort 调 DeleteUser 清理孤儿）
func (s *OrganizationService) provisionNewAPIUser(ctx context.Context, org *sqlc.Organization) (sqlc.Organization, *int64, error) {
	// new-api username 由组织 code 派生并追加随机后缀，避免与历史遗留、未随组织
	// 清理的 new-api 孤儿账号同名而撞唯一约束、导致创建失败。
	username, err := buildNewAPIUsername(org.Code)
	if err != nil {
		return sqlc.Organization{}, nil, err
	}
	password, err := generateUserPassword()
	if err != nil {
		return sqlc.Organization{}, nil, fmt.Errorf("生成 new-api 密码失败: %w", err)
	}

	user, err := s.provisioner.CreateUser(ctx, newapi.CreateUserInput{
		Username:    username,
		Password:    password,
		DisplayName: org.Name,
	})
	if err != nil {
		return sqlc.Organization{}, nil, fmt.Errorf("调用 new-api 创建用户失败: %w", err)
	}
	if user.ID == 0 {
		return sqlc.Organization{}, nil, fmt.Errorf("new-api CreateUser 返回 user_id=0")
	}
	createdUserID := user.ID

	accessToken, err := s.provisioner.BootstrapUserAccessToken(ctx, username, password)
	if err != nil {
		return sqlc.Organization{}, &createdUserID, fmt.Errorf("调用 new-api 登录拿 access_token 失败: %w", err)
	}

	credPayload, err := json.Marshal(OrganizationCredentials{
		Username:    username,
		Password:    password,
		AccessToken: accessToken,
	})
	if err != nil {
		return sqlc.Organization{}, &createdUserID, fmt.Errorf("序列化 new-api 凭据失败: %w", err)
	}
	ciphertext, err := s.cipher.Encrypt(credPayload)
	if err != nil {
		return sqlc.Organization{}, &createdUserID, fmt.Errorf("加密 new-api 凭据失败: %w", err)
	}

	// SetOrganizationNewAPIUser 为 :exec；写入后通过 GetOrganization 读回。
	if err := s.store.SetOrganizationNewAPIUser(ctx, sqlc.SetOrganizationNewAPIUserParams{
		ID:                              org.ID,
		NewapiUserID:                    null.StringFrom(strconv.FormatInt(user.ID, 10)),
		NewapiUserCredentialsCiphertext: null.StringFrom(ciphertext),
		// 同步落 new-api 侧 username（org.Code 派生 + 随机后缀），供 usage service
		// 直接读 organizations.newapi_username 定位 new-api 账号，避免运行时反查或
		// 解密凭据；该列是 user-scoped 调用定位 new-api 账号的唯一权威来源。
		NewapiUsername: null.StringFrom(username),
	}); err != nil {
		return sqlc.Organization{}, &createdUserID, fmt.Errorf("写入 new-api user 信息失败: %w", err)
	}
	updated, err := s.store.GetOrganization(ctx, org.ID)
	if err != nil {
		return sqlc.Organization{}, &createdUserID, fmt.Errorf("读取 new-api 信息写入后的企业记录失败: %w", err)
	}
	return updated, &createdUserID, nil
}

// generateUserPassword 生成 16 字符随机密码。
//
// 长度选 16：
//   - new-api `model.User.Password` 字段绑了 `validate:"max=20"` 等校验，超过会被拒；
//   - 16 字符 base32 等效熵 ≈ 80 bits，对内部 server-to-server 凭据足够；
//   - 用 base32 不用 hex 是为了输出无 +/=、不需要 URL 编码的字符。
func generateUserPassword() (string, error) {
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return strings.TrimRight(base32.StdEncoding.EncodeToString(raw), "="), nil
}

// new-api username 生成相关常量。
const (
	// newapiUsernameMaxLen 是 new-api 侧 username 的最大长度，对应 new-api
	// model.User.Username 的 `validate:"max=20"` 校验，超出会被 new-api 拒绝。
	newapiUsernameMaxLen = 20
	// newapiUsernameSuffixLen 是拼到 new-api username 末尾的随机后缀长度。
	// 6 位 base32 小写字符（[a-z2-7]）熵约 30 bits，足以避免与同 code 的少量
	// 历史残留账号碰撞，同时为 code 前缀留出足够长度。
	newapiUsernameSuffixLen = 6
)

// generateNewAPIUsernameSuffix 生成 new-api username 的随机后缀。
//
// base32 小写后字符集为 [a-z2-7]，是组织 code 合法字符集 [a-z0-9-] 的子集，
// 拼接后不会引入 new-api 不接受的字符。
func generateNewAPIUsernameSuffix() (string, error) {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	encoded := strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(raw), "="))
	return encoded[:newapiUsernameSuffixLen], nil
}

// buildNewAPIUsername 由组织 code 派生 new-api username：code 前缀 + "-" + 随机后缀。
//
// 加随机后缀是为了防止与历史遗留、未随组织清理的 new-api 孤儿账号同名而撞
// users_username_key 唯一约束、导致组织创建失败。整体长度受 new-api
// `validate:"max=20"` 限制，code 过长时按预算截断前缀。派生出的 username 会随
// 组织一起落 organizations.newapi_username，后续 user-scoped 调用一律以该列为准。
func buildNewAPIUsername(code string) (string, error) {
	suffix, err := generateNewAPIUsernameSuffix()
	if err != nil {
		return "", fmt.Errorf("生成 new-api 用户名后缀失败: %w", err)
	}
	// code 前缀预算 = 上限 - 分隔符 "-" - 后缀长度。
	prefixBudget := newapiUsernameMaxLen - 1 - len(suffix)
	prefix := code
	if len(prefix) > prefixBudget {
		// 截断后去掉可能残留的尾部 "-"，避免出现 "xxx--suffix" 这种双横线。
		prefix = strings.TrimRight(prefix[:prefixBudget], "-")
	}
	return prefix + "-" + suffix, nil
}

// ListOrganizations 列出未删除组织；第一版仅平台管理员可访问全量组织。
func (s *OrganizationService) ListOrganizations(ctx context.Context, principal auth.Principal, limit, offset int32) ([]OrganizationResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return nil, ErrForbidden
	}
	orgs, err := s.store.ListOrganizations(ctx, sqlc.ListOrganizationsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("查询企业列表失败: %w", err)
	}
	return s.toOrganizationResultsWithAdminUsernames(ctx, orgs), nil
}

// GetOrganization 根据角色限制组织访问范围。
func (s *OrganizationService) GetOrganization(ctx context.Context, principal auth.Principal, orgID string) (OrganizationResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin && principal.OrgID != orgID {
		return OrganizationResult{}, ErrForbidden
	}
	org, err := s.store.GetOrganization(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return OrganizationResult{}, ErrNotFound
	}
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("查询企业失败: %w", err)
	}
	return s.toOrganizationResultWithAdminUsername(ctx, org), nil
}

// UpdateOrganization 更新组织基础资料；生命周期状态必须走 enable/disable。
func (s *OrganizationService) UpdateOrganization(ctx context.Context, principal auth.Principal, orgID string, input OrganizationInput) (OrganizationResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return OrganizationResult{}, ErrForbidden
	}
	current, err := s.store.GetOrganization(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return OrganizationResult{}, ErrNotFound
	}
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("查询企业失败: %w", err)
	}
	// 更新未提交容量时保留数据库原值；显式提交时只校验正数，不做用量快照或本地缓存。
	knowledgeQuotaBytes := current.KnowledgeQuotaBytes
	if input.KnowledgeQuotaBytes != nil {
		if err := validateKnowledgeQuotaBytes(*input.KnowledgeQuotaBytes); err != nil {
			return OrganizationResult{}, err
		}
		knowledgeQuotaBytes = *input.KnowledgeQuotaBytes
	}
	// 个人知识库默认配额：未提交时保留数据库原值；显式提交时只校验正数。
	defaultAppKnowledgeQuotaBytes := current.DefaultAppKnowledgeQuotaBytes
	if input.DefaultAppKnowledgeQuotaBytes != nil {
		if err := validateKnowledgeQuotaBytes(*input.DefaultAppKnowledgeQuotaBytes); err != nil {
			return OrganizationResult{}, err
		}
		defaultAppKnowledgeQuotaBytes = *input.DefaultAppKnowledgeQuotaBytes
	}
	// 处理助手版本 allowlist：显式传入时校验并更新，否则保留原有值。
	var versionIDsJSON []byte
	if input.AssistantVersionIDsSet {
		if s.versionValidator == nil {
			return OrganizationResult{}, fmt.Errorf("版本校验器未配置，无法保存企业可用版本")
		}
		cleanVersionIDs, err := s.versionValidator.ValidateAssistantVersionIDs(ctx, input.AssistantVersionIDs)
		if err != nil {
			return OrganizationResult{}, err
		}
		versionIDsJSON, err = json.Marshal(cleanVersionIDs)
		if err != nil {
			return OrganizationResult{}, fmt.Errorf("序列化企业可用版本失败: %w", err)
		}
	} else {
		// 未显式传入时原样保留数据库中已有的版本 allowlist。
		versionIDsJSON = current.AssistantVersionIds
	}
	// UpdateOrganizationProfile 为 :exec；写入后通过 GetOrganization 读回。
	if err := s.store.UpdateOrganizationProfile(ctx, sqlc.UpdateOrganizationProfileParams{
		ID:                            orgID,
		Name:                          input.Name,
		ContactName:                   nullStr(input.ContactName),
		ContactPhone:                  nullStr(input.ContactPhone),
		Remark:                        nullStr(input.Remark),
		CreditWarningThreshold:        nullIntFromInt32Ptr(input.CreditWarningThreshold),
		MaxInstanceCount:              nullIntFromInt32Ptr(input.MaxInstanceCount),
		KnowledgeQuotaBytes:           knowledgeQuotaBytes,
		DefaultAppKnowledgeQuotaBytes: defaultAppKnowledgeQuotaBytes,
		AssistantVersionIds:           versionIDsJSON,
	}); err != nil {
		return OrganizationResult{}, fmt.Errorf("更新企业失败: %w", err)
	}
	org, err := s.store.GetOrganization(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return OrganizationResult{}, ErrNotFound
	}
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("读取更新后企业失败: %w", err)
	}
	return s.toOrganizationResultWithAdminUsername(ctx, org), nil
}

// UpdateAICCConfig 更新企业 AICC 开通状态和智能体数量上限。
func (s *OrganizationService) UpdateAICCConfig(ctx context.Context, principal auth.Principal, orgID string, input AICCConfigInput) (OrganizationResult, error) {
	// AICC 开通是平台级租户配置，权限判断统一复用 authorizer，避免 service 内散落角色判断。
	if !auth.CanManageAICCConfig(principal) {
		return OrganizationResult{}, ErrForbidden
	}
	if input.AgentLimit != nil && *input.AgentLimit < 0 {
		return OrganizationResult{}, fmt.Errorf("%w: AICC 智能体数量上限不能为负数", ErrInvalidArgument)
	}
	// UpdateOrganizationAICCConfig 为 :exec；写入后回读组织，确保响应包含数据库最终状态。
	if err := s.store.UpdateOrganizationAICCConfig(ctx, sqlc.UpdateOrganizationAICCConfigParams{
		AiccEnabled:    input.Enabled,
		AiccAgentLimit: nullIntFromInt32Ptr(input.AgentLimit),
		ID:             orgID,
	}); err != nil {
		return OrganizationResult{}, fmt.Errorf("更新企业 AICC 配置失败: %w", err)
	}
	org, err := s.store.GetOrganization(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return OrganizationResult{}, ErrNotFound
	}
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("读取企业失败: %w", err)
	}
	return toOrganizationResult(org), nil
}

// SetOrganizationStatus 启用或禁用企业；软删除后续由删除流程单独处理。
func (s *OrganizationService) SetOrganizationStatus(ctx context.Context, principal auth.Principal, orgID, status string) (OrganizationResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return OrganizationResult{}, ErrForbidden
	}
	if status != domain.StatusActive && status != domain.StatusDisabled {
		return OrganizationResult{}, fmt.Errorf("非法企业状态: %s", status)
	}
	// SetOrganizationStatus 为 :exec；写入后通过 GetOrganization 读回。
	if err := s.store.SetOrganizationStatus(ctx, sqlc.SetOrganizationStatusParams{ID: orgID, Status: status}); err != nil {
		return OrganizationResult{}, fmt.Errorf("更新企业状态失败: %w", err)
	}
	org, err := s.store.GetOrganization(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return OrganizationResult{}, ErrNotFound
	}
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("读取状态更新后企业失败: %w", err)
	}
	return s.toOrganizationResultWithAdminUsername(ctx, org), nil
}

// DecryptOrganizationCredentials 把组织的 newapi_user_credentials_ciphertext 还原为明文凭据。
//
// 给 worker handler / 其它 service 用：拿到三件套后构造 newapi.UserScopedClient 调 user-scoped
// 接口（创建 token / 拿完整 sk- / 改 status）。cipher 为 nil 直接返回错误，避免静默退化。
func DecryptOrganizationCredentials(org sqlc.Organization, cipher *auth.Cipher) (OrganizationCredentials, error) {
	if cipher == nil {
		return OrganizationCredentials{}, fmt.Errorf("cipher 未配置，无法解密 new-api 凭据")
	}
	if !org.NewapiUserCredentialsCiphertext.Valid || org.NewapiUserCredentialsCiphertext.String == "" {
		return OrganizationCredentials{}, fmt.Errorf("企业 %s 未持有 new-api 凭据密文", org.ID)
	}
	plain, err := cipher.Decrypt(org.NewapiUserCredentialsCiphertext.String)
	if err != nil {
		return OrganizationCredentials{}, fmt.Errorf("解密 new-api 凭据失败: %w", err)
	}
	var creds OrganizationCredentials
	if err := json.Unmarshal(plain, &creds); err != nil {
		return OrganizationCredentials{}, fmt.Errorf("解析 new-api 凭据失败: %w", err)
	}
	if creds.Username == "" || creds.Password == "" || creds.AccessToken == "" {
		return OrganizationCredentials{}, fmt.Errorf("企业 %s 的 new-api 凭据三件套不完整", org.ID)
	}
	return creds, nil
}

func toOrganizationResults(orgs []sqlc.Organization) []OrganizationResult {
	results := make([]OrganizationResult, 0, len(orgs))
	for _, org := range orgs {
		results = append(results, toOrganizationResult(org))
	}
	return results
}

// toOrganizationResultsWithAdminUsernames 在组织基础视图上补充管理员用户名。
// 密码明文只在创建请求里短暂出现，数据库只保存 hash，因此响应不会也不能包含管理员密码。
func (s *OrganizationService) toOrganizationResultsWithAdminUsernames(ctx context.Context, orgs []sqlc.Organization) []OrganizationResult {
	results := toOrganizationResults(orgs)
	for idx, org := range orgs {
		results[idx].AdminUsername = s.getOrgAdminUsername(ctx, org.ID)
	}
	return results
}

// toOrganizationResultWithAdminUsername 为单组织响应补充管理员用户名；查不到管理员时保留空值，
// 避免一个历史异常组织阻断组织资料、状态切换等主流程。
func (s *OrganizationService) toOrganizationResultWithAdminUsername(ctx context.Context, org sqlc.Organization) OrganizationResult {
	result := toOrganizationResult(org)
	result.AdminUsername = s.getOrgAdminUsername(ctx, org.ID)
	return result
}

// getOrgAdminUsername 查询企业下最早创建且未下线的 org_admin。
// sql.ErrNoRows 表示组织尚无可用管理员，返回空字符串即可由前端显示提示。
func (s *OrganizationService) getOrgAdminUsername(ctx context.Context, orgID string) string {
	user, err := s.store.GetOrgAdminByOrg(ctx, null.StringFrom(orgID))
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		slog.WarnContext(ctx, "查询企业管理员用户名失败", slog.String(mlog.KeyOrgID, orgID), mlog.Err(err))
		return ""
	}
	return user.Username
}

func toOrganizationResult(org sqlc.Organization) OrganizationResult {
	// 解析助手版本 allowlist，列为空时兜底为空 slice，避免前端收到 null。
	versionIDs := []string{}
	if len(org.AssistantVersionIds) > 0 {
		if err := json.Unmarshal(org.AssistantVersionIds, &versionIDs); err != nil {
			// 组织 assistant_version_ids 列由本服务统一以 JSON 数组写入，理论上不会损坏；
			// 真出现损坏时记日志而不是静默吞掉，避免组织列表/详情无声降级。
			slog.Warn("解析企业 assistant_version_ids 失败", slog.String(mlog.KeyOrgID, org.ID), mlog.Err(err))
			versionIDs = []string{}
		}
	}
	return OrganizationResult{
		ID:                            org.ID,
		Name:                          org.Name,
		Code:                          org.Code,
		Status:                        org.Status,
		ContactName:                   strOrEmpty(org.ContactName),
		ContactPhone:                  strOrEmpty(org.ContactPhone),
		Remark:                        strOrEmpty(org.Remark),
		NewAPIUserID:                  strOrEmpty(org.NewapiUserID),
		CreditWarningThreshold:        int32PtrFromNullInt(org.CreditWarningThreshold),
		MaxInstanceCount:              int32PtrFromNullInt(org.MaxInstanceCount),
		KnowledgeQuotaBytes:           org.KnowledgeQuotaBytes,
		DefaultAppKnowledgeQuotaBytes: org.DefaultAppKnowledgeQuotaBytes,
		AssistantVersionIDs:           versionIDs,
		AICCEnabled:                   org.AiccEnabled,
		AICCAgentLimit:                int32PtrFromNullInt(org.AiccAgentLimit),
	}
}

// nullIntFromInt32Ptr 把可选 int32 指针转换为 null.Int（用于 CreditWarningThreshold）。
// nil 表示未设置阈值，写 NULL；非 nil 时写入值。
func nullIntFromInt32Ptr(value *int32) null.Int {
	if value == nil {
		return null.Int{}
	}
	return null.IntFrom(int64(*value))
}

// int32PtrFromNullInt 把 null.Int 读取为 *int32 指针（用于 API 响应）。
// NULL 返回 nil；有效值截断为 int32。
func int32PtrFromNullInt(value null.Int) *int32 {
	if !value.Valid {
		return nil
	}
	result := int32(value.Int64)
	return &result
}
