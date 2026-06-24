package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	mlog "oc-manager/internal/log"
	"oc-manager/internal/store/sqlc"
)

// OnboardingStore 在单一事务里覆盖创建成员、应用、渠道绑定、审计和任务所需的所有写入。
// service 不直接依赖 sqlc.Queries 是为了让 cmd/server 在事务函数中传入 *sqlc.Queries，
// 而单元测试用内存桩替换全部方法。
type OnboardingStore interface {
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	GetUser(ctx context.Context, id string) (sqlc.User, error)
	GetActiveAppByOwner(ctx context.Context, ownerUserID string) (sqlc.App, error)
	// CountActiveAppsByOrg 统计企业当前未删除实例数（apps.deleted_at IS NULL），用于实例上限校验。
	CountActiveAppsByOrg(ctx context.Context, orgID string) (int64, error)
	CreateUser(ctx context.Context, arg sqlc.CreateUserParams) error
	CreateApp(ctx context.Context, arg sqlc.CreateAppParams) error
	CreateChannelBinding(ctx context.Context, arg sqlc.CreateChannelBindingParams) error
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) error
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
}

// TxRunner 用同一事务函数覆盖 OnboardingStore 全部写入。
// 调用 Begin 失败、fn 返回错误或 Commit 失败时调用方都不应继续，service 直接返回错误。
type TxRunner interface {
	WithTx(ctx context.Context, fn func(OnboardingStore) error) error
}

// MemberOnboardingService 把成员-应用-渠道绑定-审计-任务放在同一个事务里完成。
// 任意一步失败都要让整个事务回滚，避免"成员创建成功但应用没创建"这种悬挂状态。
// k8s 模型下不再需要选节点，pod 落点由调度器决定。
type MemberOnboardingService struct {
	tx                TxRunner
	hashPassword      PasswordHasher
	knowledgeDatasets KnowledgeDatasetProvisioner
	// defaultLocale 是平台默认语言（en/zh），在创建实例时作为 locale 的回退值。
	// owner 未显式设置语言时，实例 locale 落此值；否则快照 owner 的 locale。
	defaultLocale string
}

// NewMemberOnboardingService 创建 onboarding 服务。
// defaultLocale 是平台默认语言（en/zh），用于在 owner locale 未设置时回退；空时按 "en" 处理。
func NewMemberOnboardingService(tx TxRunner, hash PasswordHasher, defaultLocale ...string) *MemberOnboardingService {
	dl := "en"
	if len(defaultLocale) > 0 && defaultLocale[0] != "" {
		dl = defaultLocale[0]
	}
	return &MemberOnboardingService{tx: tx, hashPassword: hash, defaultLocale: dl}
}

// SetKnowledgeDatasetProvisioner 注入实例创建后的知识库 dataset 预创建能力。
func (s *MemberOnboardingService) SetKnowledgeDatasetProvisioner(p KnowledgeDatasetProvisioner) {
	s.knowledgeDatasets = p
}

// OnboardMemberInput 描述创建一个成员并联动初始化应用所需要的字段。
// k8s 模型下不需要指定节点，pod 落点由调度器决定。
type OnboardMemberInput struct {
	Username    string
	DisplayName string
	Password    string
	Role        string
	AppName     string
	ChannelType string
	VersionID   string // 必填：实例绑定的助手版本 ID，必须在组织的 assistant_version_ids 允许列表内。
}

// OnboardMemberResult 是事务成功后的视图。
type OnboardMemberResult struct {
	Member MemberResult `json:"member"`
	App    AppResult    `json:"app"`
	JobID  string       `json:"job_id"`
}

// CreateAppForMemberInput 描述为已有成员重建实例时需要的应用初始化字段。
// k8s 模型下不需要指定节点，pod 落点由调度器决定。
type CreateAppForMemberInput struct {
	AppName     string
	ChannelType string
	VersionID   string // 必填：实例绑定的助手版本 ID，必须在组织的 assistant_version_ids 允许列表内。
}

// CreateAppForMemberResult 是为已有成员创建新实例后的视图。
type CreateAppForMemberResult struct {
	App   AppResult `json:"app"`
	JobID string    `json:"job_id"`
}

// OnboardMember 在事务里创建用户、应用、渠道绑定、审计、app_initialize job。
// 该复合入口会直接创建应用，因此只允许本组织管理员发起，避免平台管理员绕过组织边界写入应用资源。
func (s *MemberOnboardingService) OnboardMember(ctx context.Context, principal auth.Principal, orgID string, input OnboardMemberInput) (OnboardMemberResult, error) {
	if !auth.CanCreateAppForOrg(principal, orgID) {
		return OnboardMemberResult{}, ErrForbidden
	}
	if input.Username == "" || input.Password == "" || input.DisplayName == "" || input.AppName == "" || input.VersionID == "" {
		return OnboardMemberResult{}, fmt.Errorf("%w: 用户名、密码、显示名、应用名、助手版本不能为空", ErrMemberCreateInvalid)
	}
	role := input.Role
	if role == "" {
		role = domain.UserRoleOrgMember
	}
	if role != domain.UserRoleOrgAdmin && role != domain.UserRoleOrgMember {
		return OnboardMemberResult{}, fmt.Errorf("%w: 不支持的角色", ErrMemberCreateInvalid)
	}
	channelType := input.ChannelType
	if channelType == "" {
		channelType = domain.ChannelTypeWeChat
	}
	hashedPassword, err := s.hashPassword(input.Password)
	if err != nil {
		return OnboardMemberResult{}, fmt.Errorf("生成密码 hash 失败: %w", err)
	}

	// 预先生成各行 ID，在事务内使用。
	userID := newUUID()
	appID := newUUID()
	channelBindingID := newUUID()
	auditID1 := newUUID()
	auditID2 := newUUID()
	jobID := newUUID()

	var result OnboardMemberResult
	var createdApp sqlc.App
	txErr := s.tx.WithTx(ctx, func(store OnboardingStore) error {
		org, err := store.GetOrganization(ctx, orgID)
		if err != nil {
			return fmt.Errorf("查询企业失败: %w", err)
		}
		if org.Status != domain.StatusActive {
			return fmt.Errorf("%w: 企业已停用", ErrMemberCreateInvalid)
		}
		// 校验所选助手版本在组织 allowlist 内，防止跨组织使用未授权版本。
		if !versionInOrgAllowlist(org, input.VersionID) {
			return fmt.Errorf("%w: 所选助手版本不在企业可用范围内", ErrMemberCreateInvalid)
		}
		// 校验企业未达实例数量上限（max_instance_count）。
		if err := ensureInstanceQuota(ctx, store, org); err != nil {
			return err
		}
		// 新成员语言随创建者：读操作者（创建该成员的管理员）的 locale，缺省回落平台默认。
		// 若 GetUser 查询失败或 locale 未设置，则安全回落到 s.defaultLocale，不阻断流程。
		memberLocale := s.defaultLocale
		if creator, err := store.GetUser(ctx, principal.UserID); err == nil && creator.Locale.Valid && creator.Locale.String != "" {
			memberLocale = creator.Locale.String
		}
		// CreateUser 为 :exec；事务内直接使用预生成 ID，事务外读回。
		if err := store.CreateUser(ctx, sqlc.CreateUserParams{
			ID:           userID,
			OrgID:        null.StringFrom(org.ID),
			Username:     input.Username,
			PasswordHash: hashedPassword,
			DisplayName:  input.DisplayName,
			Role:         role,
			Status:       domain.StatusActive,
			Locale:       null.StringFrom(memberLocale),
		}); err != nil {
			return fmt.Errorf("创建成员失败: %w", err)
		}
		// CreateApp 为 :exec；k8s 模型下不写 runtime_node_id，由调度器决定落点。
		// locale：新成员与实例语言随创建者，缺省平台默认。
		if err := store.CreateApp(ctx, sqlc.CreateAppParams{
			ID:           appID,
			OrgID:        org.ID,
			OwnerUserID:  userID,
			Name:         input.AppName,
			Description:  null.String{},
			Status:       domain.AppStatusDraft,
			ApiKeyStatus: domain.APIKeyStatusPending,
			VersionID:    null.StringFrom(input.VersionID),
			Locale:       null.StringFrom(memberLocale),
		}); err != nil {
			return fmt.Errorf("创建应用失败: %w", err)
		}
		createdApp = sqlc.App{ID: appID, OrgID: org.ID, OwnerUserID: userID}
		// CreateChannelBinding 为 :exec。
		if err := store.CreateChannelBinding(ctx, sqlc.CreateChannelBindingParams{
			ID:          channelBindingID,
			AppID:       appID,
			ChannelType: channelType,
			Status:      domain.ChannelStatusUnbound,
		}); err != nil {
			return fmt.Errorf("创建渠道绑定失败: %w", err)
		}
		// 成员创建审计。
		// metadata 存储结构化参数：member_name/app_name，供前端按语言渲染详情。
		memberAuditMeta, err := json.Marshal(map[string]any{
			"member_name": displayNameOrUsername(sqlc.User{DisplayName: input.DisplayName, Username: input.Username}),
			"app_name":    input.AppName,
		})
		if err != nil {
			return fmt.Errorf("序列化成员创建审计元数据失败: %w", err)
		}
		if err := store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
			ID:           auditID1,
			ActorID:      null.StringFrom(principal.UserID),
			ActorRole:    principal.Role,
			OrgID:        null.StringFrom(org.ID),
			TargetType:   "member",
			TargetID:     userID,
			Action:       "create_with_app",
			Result:       "succeeded",
			MetadataJson: memberAuditMeta,
		}); err != nil {
			return fmt.Errorf("写入审计日志失败: %w", err)
		}
		// 应用创建审计：合并 owner_user_id/channel_type（原有）与 member_name/app_name 到同一 metadata。
		appAuditMetadata, err := json.Marshal(map[string]any{
			"owner_user_id": userID,
			"channel_type":  channelType,
			"member_name":   input.DisplayName,
			"app_name":      input.AppName,
		})
		if err != nil {
			return fmt.Errorf("序列化应用创建审计元数据失败: %w", err)
		}
		// 应用创建审计。
		if err := store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
			ID:           auditID2,
			ActorID:      null.StringFrom(principal.UserID),
			ActorRole:    principal.Role,
			OrgID:        null.StringFrom(org.ID),
			TargetType:   "app",
			TargetID:     appID,
			Action:       "create",
			Result:       "succeeded",
			MetadataJson: appAuditMetadata,
		}); err != nil {
			return fmt.Errorf("写入应用创建审计日志失败: %w", err)
		}
		// app_initialize payload 只需 app_id；k8s 模型下不需要 runtime_node。
		payload, err := json.Marshal(map[string]any{
			"app_id": appID,
		})
		if err != nil {
			return fmt.Errorf("序列化 job payload 失败: %w", err)
		}
		// CreateJob 为 :exec；RunAfter 是 time.Time（MySQL DATETIME）。
		if err := store.CreateJob(ctx, sqlc.CreateJobParams{
			ID:          jobID,
			Type:        domain.JobTypeAppInitialize,
			Priority:    100,
			RunAfter:    time.Now(),
			MaxAttempts: 5,
			PayloadJson: payload,
		}); err != nil {
			return fmt.Errorf("创建初始化任务失败: %w", err)
		}
		// 构建事务内视图：sqlc 行从预生成 ID 拼装，最小化跨事务读。
		result = OnboardMemberResult{
			Member: MemberResult{
				ID:          userID,
				OrgID:       org.ID,
				Username:    input.Username,
				DisplayName: input.DisplayName,
				Role:        role,
				Status:      domain.StatusActive,
			},
			App: AppResult{
				ID:           appID,
				OrgID:        org.ID,
				OwnerUserID:  userID,
				Name:         input.AppName,
				Status:       domain.AppStatusDraft,
				APIKeyStatus: domain.APIKeyStatusPending,
				VersionID:    input.VersionID,
			},
			JobID: jobID,
		}
		return nil
	})
	if txErr != nil {
		return OnboardMemberResult{}, txErr
	}
	if s.knowledgeDatasets != nil {
		if _, err := s.knowledgeDatasets.EnsureAppDataset(ctx, createdApp); err != nil {
			slog.WarnContext(ctx, "预创建实例 RAGFlow dataset 失败", "app_id", createdApp.ID, mlog.Err(err))
		}
	}
	return result, nil
}

// CreateAppForMember 为已有成员创建新的应用实例。
// 它只允许目标成员当前没有未删除应用；旧删除记录保留，新的应用重新创建初始化任务。
func (s *MemberOnboardingService) CreateAppForMember(ctx context.Context, principal auth.Principal, orgID, userID string, input CreateAppForMemberInput) (CreateAppForMemberResult, error) {
	if !auth.CanCreateAppForMember(principal, orgID) {
		return CreateAppForMemberResult{}, ErrForbidden
	}
	if input.AppName == "" {
		return CreateAppForMemberResult{}, fmt.Errorf("%w: 应用名不能为空", ErrMemberCreateInvalid)
	}
	if input.VersionID == "" {
		return CreateAppForMemberResult{}, fmt.Errorf("%w: 助手版本不能为空", ErrMemberCreateInvalid)
	}
	channelType := input.ChannelType
	if channelType == "" {
		channelType = domain.ChannelTypeWeChat
	}

	// 预先生成 ID。
	appID := newUUID()
	channelBindingID := newUUID()
	auditID := newUUID()
	jobID := newUUID()

	var result CreateAppForMemberResult
	var createdApp sqlc.App
	txErr := s.tx.WithTx(ctx, func(store OnboardingStore) error {
		org, err := store.GetOrganization(ctx, orgID)
		if err != nil {
			return fmt.Errorf("查询企业失败: %w", err)
		}
		if org.Status != domain.StatusActive {
			return fmt.Errorf("%w: 企业已停用", ErrMemberCreateInvalid)
		}
		// 校验所选助手版本在组织 allowlist 内，防止跨组织使用未授权版本。
		if !versionInOrgAllowlist(org, input.VersionID) {
			return fmt.Errorf("%w: 所选助手版本不在企业可用范围内", ErrMemberCreateInvalid)
		}
		// 校验企业未达实例数量上限（max_instance_count）。
		if err := ensureInstanceQuota(ctx, store, org); err != nil {
			return err
		}
		user, err := store.GetUser(ctx, userID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("查询成员失败: %w", err)
		}
		if user.OrgID.String != org.ID {
			return ErrNotFound
		}
		if user.Status == domain.StatusDisabled {
			return fmt.Errorf("%w: 成员已下线", ErrMemberCreateInvalid)
		}
		if _, err := store.GetActiveAppByOwner(ctx, user.ID); err == nil {
			return fmt.Errorf("%w: 成员已有未删除实例", ErrMemberCreateInvalid)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("查询成员应用失败: %w", err)
		}
		// k8s 模型下不写 runtime_node_id，由调度器决定落点。
		// locale 快照：优先使用 owner 已设置的语言偏好；未设置时回退平台默认语言，确保 hermes 容器有确定语言。
		appLocale := s.defaultLocale
		if user.Locale.Valid && user.Locale.String != "" {
			appLocale = user.Locale.String
		}
		if err := store.CreateApp(ctx, sqlc.CreateAppParams{
			ID:           appID,
			OrgID:        org.ID,
			OwnerUserID:  user.ID,
			Name:         input.AppName,
			Description:  null.String{},
			Status:       domain.AppStatusDraft,
			ApiKeyStatus: domain.APIKeyStatusPending,
			VersionID:    null.StringFrom(input.VersionID),
			Locale:       null.StringFrom(appLocale),
		}); err != nil {
			if isAppsOwnerActiveUniqueViolation(err) {
				return fmt.Errorf("%w: 成员已有未删除实例", ErrMemberCreateInvalid)
			}
			return fmt.Errorf("创建应用失败: %w", err)
		}
		createdApp = sqlc.App{ID: appID, OrgID: org.ID, OwnerUserID: user.ID}
		if err := store.CreateChannelBinding(ctx, sqlc.CreateChannelBindingParams{
			ID:          channelBindingID,
			AppID:       appID,
			ChannelType: channelType,
			Status:      domain.ChannelStatusUnbound,
		}); err != nil {
			return fmt.Errorf("创建渠道绑定失败: %w", err)
		}
		// metadata 存储结构化参数：owner_user_id/channel_type/member_name/app_name，供前端按语言渲染详情。
		metadata, err := json.Marshal(map[string]any{
			"owner_user_id": user.ID,
			"channel_type":  channelType,
			"member_name":   displayNameOrUsername(user),
			"app_name":      input.AppName,
		})
		if err != nil {
			return fmt.Errorf("序列化应用创建审计元数据失败: %w", err)
		}
		if err := store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
			ID:           auditID,
			ActorID:      null.StringFrom(principal.UserID),
			ActorRole:    principal.Role,
			OrgID:        null.StringFrom(org.ID),
			TargetType:   "app",
			TargetID:     appID,
			Action:       "create_for_existing_member",
			Result:       "succeeded",
			MetadataJson: metadata,
		}); err != nil {
			return fmt.Errorf("写入应用创建审计日志失败: %w", err)
		}
		// app_initialize payload 只需 app_id；k8s 模型下不需要 runtime_node。
		payload, err := json.Marshal(map[string]any{
			"app_id": appID,
		})
		if err != nil {
			return fmt.Errorf("序列化 job payload 失败: %w", err)
		}
		if err := store.CreateJob(ctx, sqlc.CreateJobParams{
			ID:          jobID,
			Type:        domain.JobTypeAppInitialize,
			Priority:    100,
			RunAfter:    time.Now(),
			MaxAttempts: 5,
			PayloadJson: payload,
		}); err != nil {
			return fmt.Errorf("创建初始化任务失败: %w", err)
		}
		result = CreateAppForMemberResult{
			App: AppResult{
				ID:           appID,
				OrgID:        org.ID,
				OwnerUserID:  user.ID,
				Name:         input.AppName,
				Status:       domain.AppStatusDraft,
				APIKeyStatus: domain.APIKeyStatusPending,
				VersionID:    input.VersionID,
			},
			JobID: jobID,
		}
		return nil
	})
	if txErr != nil {
		return CreateAppForMemberResult{}, txErr
	}
	if s.knowledgeDatasets != nil {
		if _, err := s.knowledgeDatasets.EnsureAppDataset(ctx, createdApp); err != nil {
			slog.WarnContext(ctx, "预创建实例 RAGFlow dataset 失败", "app_id", createdApp.ID, mlog.Err(err))
		}
	}
	return result, nil
}

// ensureInstanceQuota 校验企业未达实例数量上限。
// org.MaxInstanceCount 无效（NULL）表示不限制，直接放行；否则统计企业未删除实例数，
// 达到或超过上限即返回 ErrInstanceLimitReached。
//
// 并发说明：计数与随后的 CreateApp 在同一事务内但不加行锁，两个并发事务理论上可能
// 都读到相同计数而各插一条、轻微越限。鉴于建实例是平台/企业管理员的手动低频操作，
// 此 race 可接受（见设计文档「语义决策」）。
func ensureInstanceQuota(ctx context.Context, store OnboardingStore, org sqlc.Organization) error {
	if !org.MaxInstanceCount.Valid {
		return nil
	}
	count, err := store.CountActiveAppsByOrg(ctx, org.ID)
	if err != nil {
		return fmt.Errorf("统计企业实例数失败: %w", err)
	}
	if count >= org.MaxInstanceCount.Int64 {
		return fmt.Errorf("%w (%d)", ErrInstanceLimitReached, org.MaxInstanceCount.Int64)
	}
	return nil
}

// versionInOrgAllowlist 判断 version_id 是否在组织 assistant_version_ids allowlist 内。
// org.AssistantVersionIds 存储 JSON 格式的 UUID 字符串数组；空字节表示未配置任何版本。
func versionInOrgAllowlist(org sqlc.Organization, versionID string) bool {
	if versionID == "" {
		return false
	}
	ids := []string{}
	if len(org.AssistantVersionIds) > 0 {
		if err := json.Unmarshal(org.AssistantVersionIds, &ids); err != nil {
			// allowlist 列由组织服务统一以 JSON 数组写入，理论上不会损坏；
			// 真损坏时记日志后按「拒绝」处理（返回 false），不静默吞掉。
			slog.Warn("解析企业 assistant_version_ids 失败", slog.String(mlog.KeyOrgID, org.ID), mlog.Err(err))
			return false
		}
	}
	for _, id := range ids {
		if id == versionID {
			return true
		}
	}
	return false
}

// isAppsOwnerActiveUniqueViolation 识别并发复建实例时由数据库兜底拦截的活跃实例唯一约束。
// MySQL 错误码 1062 对应 Duplicate entry；对应 pgconn 的 23505。
func isAppsOwnerActiveUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	// MySQL 1062: Duplicate entry '...' for key 'apps_owner_active'
	return containsMySQLDuplicateKey(errMsg, "apps_owner_active")
}

// containsMySQLDuplicateKey 判断 MySQL 错误消息是否为指定约束的 duplicate key。
func containsMySQLDuplicateKey(errMsg, keyName string) bool {
	return (strings.Contains(errMsg, "Duplicate entry") || strings.Contains(errMsg, "duplicate key")) &&
		strings.Contains(errMsg, keyName)
}

// displayNameOrUsername 返回用户用于展示的名称。
// display_name 优先；display_name 为空时回退 username；二者都为空时返回固定占位「成员」，
// 避免审计详情出现「新建成员 （含应用 X）」这种空白挂着的字段。
func displayNameOrUsername(user sqlc.User) string {
	if user.DisplayName != "" {
		return user.DisplayName
	}
	if user.Username != "" {
		return user.Username
	}
	return "成员"
}

