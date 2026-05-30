package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// NodeSelector 抽象「列出活跃节点 + 当前应用数」的能力。
// 解耦 onboarding 与 runtime_node_service / sqlc 之间的依赖，让 service 单测可注入内存桩。
type NodeSelector interface {
	ListActiveNodesWithAppCounts(ctx context.Context) ([]NodeWithCount, error)
}

// NodeWithCount 描述一个活跃节点的容量上限与当前应用数。
// MaxApps 为 nil 表示不限；剩余容量 = MaxApps - AppCount，nil 视为 +∞。
type NodeWithCount struct {
	NodeID   string
	MaxApps  *int32
	AppCount int64
}

// OnboardingStore 在单一事务里覆盖创建成员、应用、渠道绑定、审计和任务所需的所有写入。
// service 不直接依赖 sqlc.Queries 是为了让 cmd/server 在事务函数中传入 *sqlc.Queries，
// 而单元测试用内存桩替换全部方法。
type OnboardingStore interface {
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	GetUser(ctx context.Context, id string) (sqlc.User, error)
	GetActiveAppByOwner(ctx context.Context, ownerUserID string) (sqlc.App, error)
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
type MemberOnboardingService struct {
	tx                TxRunner
	hashPassword      PasswordHasher
	selector          NodeSelector
	knowledgeDatasets KnowledgeDatasetProvisioner
}

// NewMemberOnboardingService 创建 onboarding 服务。selector 可以为 nil；
// 此时 input.NodeID 为空会直接返 ErrNoNodeAvailable（生产部署应注入 SQLNodeSelector）。
func NewMemberOnboardingService(tx TxRunner, hash PasswordHasher, selector NodeSelector) *MemberOnboardingService {
	return &MemberOnboardingService{tx: tx, hashPassword: hash, selector: selector}
}

// SetKnowledgeDatasetProvisioner 注入实例创建后的知识库 dataset 预创建能力。
func (s *MemberOnboardingService) SetKnowledgeDatasetProvisioner(p KnowledgeDatasetProvisioner) {
	s.knowledgeDatasets = p
}

// selectNode 在显式 NodeID 为空时按剩余容量自动选。
// 排序规则：剩余容量降序优先（NULL = +∞ 排最前）；同剩余容量按当前应用数升序，
// 防止同一节点连续被选导致 over-commit。
func (s *MemberOnboardingService) selectNode(ctx context.Context) (string, error) {
	if s.selector == nil {
		return "", ErrNoNodeAvailable
	}
	nodes, err := s.selector.ListActiveNodesWithAppCounts(ctx)
	if err != nil {
		return "", fmt.Errorf("查询节点列表失败: %w", err)
	}
	type cand struct {
		id     string
		remain int64
		count  int64
	}
	cands := make([]cand, 0, len(nodes))
	for _, n := range nodes {
		if n.MaxApps == nil {
			cands = append(cands, cand{id: n.NodeID, remain: math.MaxInt64, count: n.AppCount})
			continue
		}
		remain := int64(*n.MaxApps) - n.AppCount
		if remain > 0 {
			cands = append(cands, cand{id: n.NodeID, remain: remain, count: n.AppCount})
		}
	}
	if len(cands) == 0 {
		return "", ErrNoNodeAvailable
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].remain != cands[j].remain {
			return cands[i].remain > cands[j].remain
		}
		return cands[i].count < cands[j].count
	})
	return cands[0].id, nil
}

// OnboardMemberInput 描述创建一个成员并联动初始化应用所需要的字段。
type OnboardMemberInput struct {
	Username    string
	DisplayName string
	Password    string
	Role        string
	AppName     string
	ChannelType string
	NodeID      string // 可选：指定要部署的 runtime 节点 ID。
	VersionID   string // 必填：实例绑定的助手版本 ID，必须在组织的 assistant_version_ids 允许列表内。
}

// OnboardMemberResult 是事务成功后的视图。
type OnboardMemberResult struct {
	Member MemberResult `json:"member"`
	App    AppResult    `json:"app"`
	JobID  string       `json:"job_id"`
}

// CreateAppForMemberInput 描述为已有成员重建实例时需要的应用初始化字段。
type CreateAppForMemberInput struct {
	AppName     string
	ChannelType string
	NodeID      string
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

	// 显式 NodeID 走原校验路径（ops 手工指派）；为空则自动选节点。
	// 自动选在事务之外完成：节点列表读不需要事务隔离，且短路失败更高效。
	if input.NodeID == "" {
		chosen, err := s.selectNode(ctx)
		if err != nil {
			return OnboardMemberResult{}, err
		}
		input.NodeID = chosen
	} else if _, err := uuid.Parse(input.NodeID); err != nil {
		// 旧实现把 NodeID 赋给 pgtype.UUID 时会隐式拒绝非法格式；迁移到 string 后显式校验，
		// 保持「显式指定的 runtime 节点 ID 非法 → 归类为成员创建参数错误」的行为。
		return OnboardMemberResult{}, fmt.Errorf("%w: 指定的 runtime 节点 ID 非法", ErrMemberCreateInvalid)
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
		// CreateUser 为 :exec；事务内直接使用预生成 ID，事务外读回。
		if err := store.CreateUser(ctx, sqlc.CreateUserParams{
			ID:           userID,
			OrgID:        null.StringFrom(org.ID),
			Username:     input.Username,
			PasswordHash: hashedPassword,
			DisplayName:  input.DisplayName,
			Role:         role,
			Status:       domain.StatusActive,
		}); err != nil {
			return fmt.Errorf("创建成员失败: %w", err)
		}
		// CreateApp 为 :exec；RuntimeNodeID nullable（spec-A2a），NodeID 为空字符串时写 NULL。
		if err := store.CreateApp(ctx, sqlc.CreateAppParams{
			ID:            appID,
			OrgID:         org.ID,
			OwnerUserID:   userID,
			RuntimeNodeID: null.NewString(input.NodeID, input.NodeID != ""),
			Name:          input.AppName,
			Description:   null.String{},
			Status:        domain.AppStatusDraft,
			ApiKeyStatus:  domain.APIKeyStatusPending,
			VersionID:     null.StringFrom(input.VersionID),
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
		if err := store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
			ID:            auditID1,
			ActorID:       null.StringFrom(principal.UserID),
			ActorRole:     principal.Role,
			OrgID:         null.StringFrom(org.ID),
			TargetType:    "member",
			TargetID:      userID,
			Action:        "create_with_app",
			Result:        "succeeded",
			DetailMessage: null.StringFrom(fmt.Sprintf("新建成员 %s（含应用 %s）", displayNameOrUsername(sqlc.User{DisplayName: input.DisplayName, Username: input.Username}), input.AppName)),
		}); err != nil {
			return fmt.Errorf("写入审计日志失败: %w", err)
		}
		appAuditMetadata, err := json.Marshal(map[string]any{
			"owner_user_id":   userID,
			"channel_type":    channelType,
			"runtime_node_id": input.NodeID,
		})
		if err != nil {
			return fmt.Errorf("序列化应用创建审计元数据失败: %w", err)
		}
		// 应用创建审计。
		if err := store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
			ID:            auditID2,
			ActorID:       null.StringFrom(principal.UserID),
			ActorRole:     principal.Role,
			OrgID:         null.StringFrom(org.ID),
			TargetType:    "app",
			TargetID:      appID,
			Action:        "create",
			Result:        "succeeded",
			MetadataJson:  appAuditMetadata,
			DetailMessage: null.StringFrom(fmt.Sprintf("归属成员 %s，渠道 %s", input.DisplayName, channelLabel(channelType))),
		}); err != nil {
			return fmt.Errorf("写入应用创建审计日志失败: %w", err)
		}
		payload, err := json.Marshal(map[string]any{
			"app_id":       appID,
			"runtime_node": input.NodeID,
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
				RuntimeNodeID: input.NodeID,
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
			slog.WarnContext(ctx, "预创建实例 RAGFlow dataset 失败", "app_id", createdApp.ID, "error", err)
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

	// 显式 NodeID 保留给运维指定节点；为空时复用 onboarding 的容量优先选择规则。
	if input.NodeID == "" {
		chosen, err := s.selectNode(ctx)
		if err != nil {
			return CreateAppForMemberResult{}, err
		}
		input.NodeID = chosen
	} else if _, err := uuid.Parse(input.NodeID); err != nil {
		// 同 OnboardMember：显式节点 ID 非法时还原为成员创建参数错误（旧 pgtype.UUID 解析的隐式校验）。
		return CreateAppForMemberResult{}, fmt.Errorf("%w: 指定的 runtime 节点 ID 非法", ErrMemberCreateInvalid)
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
		if err := store.CreateApp(ctx, sqlc.CreateAppParams{
			ID:            appID,
			OrgID:         org.ID,
			OwnerUserID:   user.ID,
			RuntimeNodeID: null.NewString(input.NodeID, input.NodeID != ""),
			Name:          input.AppName,
			Description:   null.String{},
			Status:        domain.AppStatusDraft,
			ApiKeyStatus:  domain.APIKeyStatusPending,
			VersionID:     null.StringFrom(input.VersionID),
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
		metadata, err := json.Marshal(map[string]any{
			"owner_user_id":   user.ID,
			"channel_type":    channelType,
			"runtime_node_id": input.NodeID,
		})
		if err != nil {
			return fmt.Errorf("序列化应用创建审计元数据失败: %w", err)
		}
		if err := store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
			ID:            auditID,
			ActorID:       null.StringFrom(principal.UserID),
			ActorRole:     principal.Role,
			OrgID:         null.StringFrom(org.ID),
			TargetType:    "app",
			TargetID:      appID,
			Action:        "create_for_existing_member",
			Result:        "succeeded",
			MetadataJson:  metadata,
			DetailMessage: null.StringFrom(fmt.Sprintf("归属成员 %s，渠道 %s", user.DisplayName, channelLabel(channelType))),
		}); err != nil {
			return fmt.Errorf("写入应用创建审计日志失败: %w", err)
		}
		payload, err := json.Marshal(map[string]any{
			"app_id":       appID,
			"runtime_node": input.NodeID,
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
				ID:            appID,
				OrgID:         org.ID,
				OwnerUserID:   user.ID,
				RuntimeNodeID: input.NodeID,
				Name:          input.AppName,
				Status:        domain.AppStatusDraft,
				APIKeyStatus:  domain.APIKeyStatusPending,
				VersionID:     input.VersionID,
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
			slog.WarnContext(ctx, "预创建实例 RAGFlow dataset 失败", "app_id", createdApp.ID, "error", err)
		}
	}
	return result, nil
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
			slog.Warn("解析企业 assistant_version_ids 失败", "org_id", org.ID, "error", err)
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

// channelLabel 把 channel_type 枚举（如 "wechat"）翻译为中文便于审计展示。
// 未知枚举回退到原始字符串，给后端扩展新渠道时保持自描述。
func channelLabel(channelType string) string {
	switch channelType {
	case domain.ChannelTypeWeChat:
		return "微信"
	default:
		return channelType
	}
}
