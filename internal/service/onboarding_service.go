package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

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
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	CreateUser(ctx context.Context, arg sqlc.CreateUserParams) (sqlc.User, error)
	CreateApp(ctx context.Context, arg sqlc.CreateAppParams) (sqlc.App, error)
	CreateChannelBinding(ctx context.Context, arg sqlc.CreateChannelBindingParams) (sqlc.ChannelBinding, error)
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
}

// TxRunner 用同一事务函数覆盖 OnboardingStore 全部写入。
// 调用 Begin 失败、fn 返回错误或 Commit 失败时调用方都不应继续，service 直接返回错误。
type TxRunner interface {
	WithTx(ctx context.Context, fn func(OnboardingStore) error) error
}

// MemberOnboardingService 把成员-应用-渠道绑定-审计-任务放在同一个事务里完成。
// 任意一步失败都要让整个事务回滚，避免“成员创建成功但应用没创建”这种悬挂状态。
type MemberOnboardingService struct {
	tx           TxRunner
	hashPassword PasswordHasher
	selector     NodeSelector
}

// NewMemberOnboardingService 创建 onboarding 服务。selector 可以为 nil；
// 此时 input.NodeID 为空会直接返 ErrNoNodeAvailable（生产部署应注入 SQLNodeSelector）。
func NewMemberOnboardingService(tx TxRunner, hash PasswordHasher, selector NodeSelector) *MemberOnboardingService {
	return &MemberOnboardingService{tx: tx, hashPassword: hash, selector: selector}
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
	AppPrompt   string
	PersonaMode string
	ChannelType string
	NodeID      string // 可选：指定要部署的 runtime 节点 ID。
}

// OnboardMemberResult 是事务成功后的视图。
type OnboardMemberResult struct {
	Member MemberResult `json:"member"`
	App    AppResult    `json:"app"`
	JobID  string       `json:"job_id"`
}

// OnboardMember 在事务里创建用户、应用、渠道绑定、审计、app_initialize job。
// 平台管理员可以为任何启用组织的成员发起；组织管理员仅能为自己组织发起。
func (s *MemberOnboardingService) OnboardMember(ctx context.Context, principal auth.Principal, orgID string, input OnboardMemberInput) (OnboardMemberResult, error) {
	if !canManageOrg(principal, orgID) {
		return OnboardMemberResult{}, ErrForbidden
	}
	if input.Username == "" || input.Password == "" || input.DisplayName == "" || input.AppName == "" {
		return OnboardMemberResult{}, fmt.Errorf("%w: 用户名、密码、显示名、应用名不能为空", ErrMemberCreateInvalid)
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
	personaMode := input.PersonaMode
	if personaMode == "" {
		personaMode = domain.PersonaModeOrgInherited
	}
	orgUUID, err := parseUUID(orgID)
	if err != nil {
		return OnboardMemberResult{}, ErrNotFound
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
	}

	var result OnboardMemberResult
	txErr := s.tx.WithTx(ctx, func(store OnboardingStore) error {
		org, err := store.GetOrganization(ctx, orgUUID)
		if err != nil {
			return fmt.Errorf("查询组织失败: %w", err)
		}
		if org.Status != domain.StatusActive {
			return fmt.Errorf("%w: 组织已停用", ErrMemberCreateInvalid)
		}
		user, err := store.CreateUser(ctx, sqlc.CreateUserParams{
			OrgID:        org.ID,
			Username:     input.Username,
			PasswordHash: hashedPassword,
			DisplayName:  input.DisplayName,
			Role:         role,
			Status:       domain.StatusActive,
		})
		if err != nil {
			return fmt.Errorf("创建成员失败: %w", err)
		}
		nodeUUID, err := optionalUUID(input.NodeID)
		if err != nil {
			return fmt.Errorf("非法 runtime node id: %w", err)
		}
		app, err := store.CreateApp(ctx, sqlc.CreateAppParams{
			OrgID:         org.ID,
			OwnerUserID:   user.ID,
			RuntimeNodeID: nodeUUID,
			Name:          input.AppName,
			Description:   pgtype.Text{},
			Status:        domain.AppStatusDraft,
			PersonaMode:   personaMode,
			AppPrompt:     pgtype.Text{String: input.AppPrompt, Valid: input.AppPrompt != ""},
			ApiKeyStatus:  domain.APIKeyStatusPending,
		})
		if err != nil {
			return fmt.Errorf("创建应用失败: %w", err)
		}
		if _, err := store.CreateChannelBinding(ctx, sqlc.CreateChannelBindingParams{
			AppID:       app.ID,
			ChannelType: channelType,
			Status:      domain.ChannelStatusUnbound,
		}); err != nil {
			return fmt.Errorf("创建渠道绑定失败: %w", err)
		}
		actorUUID, _ := optionalUUID(principal.UserID)
		if _, err := store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
			ActorID:    actorUUID,
			ActorRole:  principal.Role,
			OrgID:      org.ID,
			TargetType: "member",
			TargetID:   uuidToString(user.ID),
			Action:     "create_with_app",
			Result:     "succeeded",
		}); err != nil {
			return fmt.Errorf("写入审计日志失败: %w", err)
		}
		payload, err := json.Marshal(map[string]any{
			"app_id":       uuidToString(app.ID),
			"runtime_node": input.NodeID,
		})
		if err != nil {
			return fmt.Errorf("序列化 job payload 失败: %w", err)
		}
		job, err := store.CreateJob(ctx, sqlc.CreateJobParams{
			Type:        domain.JobTypeAppInitialize,
			Priority:    100,
			RunAfter:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
			MaxAttempts: 5,
			PayloadJson: payload,
		})
		if err != nil {
			return fmt.Errorf("创建初始化任务失败: %w", err)
		}
		result = OnboardMemberResult{
			Member: toMemberResult(user),
			App:    toAppResult(app),
			JobID:  uuidToString(job.ID),
		}
		return nil
	})
	if txErr != nil {
		return OnboardMemberResult{}, txErr
	}
	return result, nil
}

// optionalUUID 解析可选 UUID。
// 空字符串返回 invalid pgtype.UUID 而不是错误，方便上层把“未指定节点”作为合法情况处理。
func optionalUUID(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	id, err := parseUUID(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}

// errOnboardingFailed 仅用于内部测试中识别事务回滚路径。
var errOnboardingFailed = errors.New("onboarding 事务失败")
