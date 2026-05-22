package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// TestOnboardMemberCommitsOnSuccess 验证引导成员提交On成功的成功路径场景。
func TestOnboardMemberCommitsOnSuccess(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	result, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.JobID)
	assert.Equal(t, "alice-bot", result.App.Name)
	assert.Equal(t, "alice", result.Member.Username)
	assert.NotContains(t, result.Member.Username, "test-org-")
	require.True(t, tx.committed)
	assert.Positive(t, store.users)
	assert.Positive(t, store.apps)
	assert.Positive(t, store.bindings)
	require.Len(t, store.auditLogs, 2)
	assert.Equal(t, "member", store.auditLogs[0].TargetType)
	assert.Equal(t, "create_with_app", store.auditLogs[0].Action)
	assert.Equal(t, "app", store.auditLogs[1].TargetType)
	assert.Equal(t, "create", store.auditLogs[1].Action)
	assert.Equal(t, result.App.ID, store.auditLogs[1].TargetID)
	assert.Positive(t, store.jobs)
	// auditLogs[0] = create_with_app; 详情应为「新建成员 <显示名>（含应用 <应用名>）」。
	require.True(t, store.auditLogs[0].DetailMessage.Valid)
	require.Equal(t, "新建成员 Alice（含应用 alice-bot）", store.auditLogs[0].DetailMessage.String)
	// auditLogs[1] = app create; 详情应为「归属成员 Alice，渠道 微信」。
	require.True(t, store.auditLogs[1].DetailMessage.Valid)
	require.Equal(t, "归属成员 Alice，渠道 微信", store.auditLogs[1].DetailMessage.String)
}

// TestOnboardMemberRollsBackWhenAppCreationFails 验证引导成员回滚回退当应用Creation失败的预期行为场景。
func TestOnboardMemberRollsBackWhenAppCreationFails(t *testing.T) {
	store := newOnboardingStub(t)
	store.appErr = errors.New("duplicate app for owner")
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.Error(t, err)
	require.False(t, tx.committed)
}

// TestOnboardMemberRollsBackWhenJobCreationFails 验证引导成员回滚回退当任务Creation失败的预期行为场景。
func TestOnboardMemberRollsBackWhenJobCreationFails(t *testing.T) {
	store := newOnboardingStub(t)
	store.jobErr = errors.New("redis blocked")
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.Error(t, err)
	require.False(t, tx.committed)
}

// TestOnboardMemberRequiresOrgManagement 验证引导成员要求组织Management的预期行为场景。
func TestOnboardMemberRequiresOrgManagement(t *testing.T) {
	tx := &txRunnerStub{store: newOnboardingStub(t)}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	_, err := svc.OnboardMember(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testOrg2ID}, testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.ErrorIs(t, err, ErrForbidden)
}

// TestOnboardMemberPlatformAdminForbidden 验证引导成员平台管理员禁止访问的异常或拒绝路径场景。
func TestOnboardMemberPlatformAdminForbidden(t *testing.T) {
	tx := &txRunnerStub{store: newOnboardingStub(t)}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	_, err := svc.OnboardMember(context.Background(), platformAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.ErrorIs(t, err, ErrForbidden)
}

// TestOnboardMemberRejectsDisabledOrg 验证引导成员拒绝禁用组织的异常或拒绝路径场景。
func TestOnboardMemberRejectsDisabledOrg(t *testing.T) {
	store := newOnboardingStub(t)
	store.org.Status = domain.StatusDisabled
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

// TestCreateAppForMember_PlatformAdminCreatesAfterDelete 验证平台管理员可为无活跃实例的已有成员创建新实例。
func TestCreateAppForMember_PlatformAdminCreatesAfterDelete(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	result, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, uuidToString(store.user.ID), CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		VersionID: testVersionID,
	})

	require.NoError(t, err)
	require.True(t, tx.committed)
	assert.Equal(t, "alice-new-bot", result.App.Name)
	assert.Equal(t, uuidToString(store.user.ID), store.lastAppOwnerID)
	assert.NotEmpty(t, result.JobID)
	require.Len(t, store.auditLogs, 1)
	assert.Equal(t, "app", store.auditLogs[0].TargetType)
	assert.Equal(t, "create_for_existing_member", store.auditLogs[0].Action)
	// 详情格式与 OnboardMember 的 create 一致：归属成员 + 渠道。
	require.True(t, store.auditLogs[0].DetailMessage.Valid)
	require.Equal(t, "归属成员 Alice，渠道 微信", store.auditLogs[0].DetailMessage.String)
	// 创建的应用应绑定指定的助手版本 ID。
	assert.Equal(t, testVersionID, store.lastAppVersionID)
}

// TestCreateAppForMemberInheritsOrgModel 验证为已有成员创建实例时模型继承自组织配置。
func TestCreateAppForMemberInheritsOrgModel(t *testing.T) {
	store := newOnboardingStub(t)
	store.org.ModelID = "qwen2.5:7b"
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	result, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, uuidToString(store.user.ID), CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		VersionID: testVersionID,
	})

	require.NoError(t, err)
	require.NotEmpty(t, result.App.ID)
	// 模型应继承自组织，而非由调用方指定。
	assert.Equal(t, "qwen2.5:7b", store.lastAppModelID)
	assert.Equal(t, "qwen2.5:7b", result.App.ModelID)
}

// TestCreateAppForMember_RejectsExistingActiveApp 验证成员已有未删除实例时拒绝创建新实例。
func TestCreateAppForMember_RejectsExistingActiveApp(t *testing.T) {
	store := newOnboardingStub(t)
	existing := sqlc.App{ID: mustUUID(t, "00000000-0000-0000-0000-000000000b99"), OwnerUserID: store.user.ID}
	store.activeApp = &existing
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, uuidToString(store.user.ID), CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		VersionID: testVersionID,
	})

	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	require.False(t, tx.committed)
}

// TestCreateAppForMember_RejectsCrossOrgUser 验证路径组织与目标用户组织不一致时按不存在处理。
func TestCreateAppForMember_RejectsCrossOrgUser(t *testing.T) {
	store := newOnboardingStub(t)
	store.user.OrgID = mustUUID(t, testOrg2ID)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, uuidToString(store.user.ID), CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		VersionID: testVersionID,
	})

	require.ErrorIs(t, err, ErrNotFound)
	require.False(t, tx.committed)
}

// TestCreateAppForMember_RejectsDisabledUser 验证已下线成员不能创建新的可运行实例。
func TestCreateAppForMember_RejectsDisabledUser(t *testing.T) {
	store := newOnboardingStub(t)
	store.user.Status = domain.StatusDisabled
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, uuidToString(store.user.ID), CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		VersionID: testVersionID,
	})

	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	require.False(t, tx.committed)
}

// TestCreateAppForMember_NoActiveNode 验证自动选节点无容量时返回无可用节点。
func TestCreateAppForMember_NoActiveNode(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, &nodeSelectorStub{})

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, uuidToString(store.user.ID), CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		VersionID: testVersionID,
	})

	require.ErrorIs(t, err, ErrNoNodeAvailable)
	require.False(t, tx.committed)
}

// TestCreateAppForMember_RejectsInvalidExplicitNodeID 验证显式节点 ID 非法时归类为成员创建参数错误。
func TestCreateAppForMember_RejectsInvalidExplicitNodeID(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, uuidToString(store.user.ID), CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		NodeID:    "not-a-uuid",
		VersionID: testVersionID,
	})

	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	require.False(t, tx.committed)
}

// TestCreateAppForMember_MapsOwnerActiveUniqueViolation 验证并发创建命中活跃实例唯一索引时归类为业务冲突。
func TestCreateAppForMember_MapsOwnerActiveUniqueViolation(t *testing.T) {
	store := newOnboardingStub(t)
	store.appErr = &pgconn.PgError{Code: "23505", ConstraintName: "apps_owner_active"}
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, uuidToString(store.user.ID), CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		VersionID: testVersionID,
	})

	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	require.False(t, tx.committed)
}

type txRunnerStub struct {
	store     *onboardingStub
	committed bool
}

func (r *txRunnerStub) WithTx(ctx context.Context, fn func(OnboardingStore) error) error {
	r.committed = false
	r.store.reset()
	if err := fn(r.store); err != nil {
		return err
	}
	r.committed = true
	r.store.commit()
	return nil
}

type onboardingStub struct {
	t                *testing.T
	org              sqlc.Organization
	user             sqlc.User
	activeApp        *sqlc.App
	users            int
	apps             int
	bindings         int
	audits           int
	auditLogs        []sqlc.CreateAuditLogParams
	jobs             int
	staged           counters
	stagedAudits     []sqlc.CreateAuditLogParams
	appErr           error
	jobErr           error
	lastAppNodeID    string
	lastAppOwnerID   string
	lastAppModelID   string
	lastAppVersionID string // 记录最近一次 CreateApp 使用的 VersionID，供断言校验。
}

type counters struct{ users, apps, bindings, audits, jobs int }

// testVersionID 是测试用的固定助手版本 UUID，存入 org.AssistantVersionIds allowlist 供校验通过。
const testVersionID = "00000000-0000-0000-0000-000000000f01"

func newOnboardingStub(t *testing.T) *onboardingStub {
	return &onboardingStub{
		t: t,
		org: sqlc.Organization{
			ID:      mustUUID(t, testOrgID),
			Status:  domain.StatusActive,
			Name:    "测试组织",
			ModelID: "qwen2.5:7b",
			// 预置 allowlist，包含 testVersionID，供创建实例时的版本校验通过。
			AssistantVersionIds: []byte(`["` + testVersionID + `"]`),
		},
		user: sqlc.User{
			ID:          mustUUID(t, "00000000-0000-0000-0000-000000000a11"),
			OrgID:       mustUUID(t, testOrgID),
			Username:    "alice",
			DisplayName: "Alice",
			Role:        domain.UserRoleOrgMember,
			Status:      domain.StatusActive,
		},
	}
}

func (s *onboardingStub) counters() counters {
	return counters{s.users, s.apps, s.bindings, s.audits, s.jobs}
}

func (s *onboardingStub) reset() { s.staged = counters{} }

func (s *onboardingStub) commit() {
	s.users += s.staged.users
	s.apps += s.staged.apps
	s.bindings += s.staged.bindings
	s.audits += s.staged.audits
	s.auditLogs = append(s.auditLogs, s.stagedAudits...)
	s.jobs += s.staged.jobs
	s.staged = counters{}
	s.stagedAudits = nil
}

func (s *onboardingStub) GetOrganization(_ context.Context, id pgtype.UUID) (sqlc.Organization, error) {
	if id != s.org.ID {
		return sqlc.Organization{}, errors.New("not found")
	}
	return s.org, nil
}

func (s *onboardingStub) GetUser(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
	if id != s.user.ID {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return s.user, nil
}

func (s *onboardingStub) GetActiveAppByOwner(_ context.Context, ownerUserID pgtype.UUID) (sqlc.App, error) {
	if s.activeApp == nil || ownerUserID != s.activeApp.OwnerUserID {
		return sqlc.App{}, pgx.ErrNoRows
	}
	return *s.activeApp, nil
}

func (s *onboardingStub) CreateUser(_ context.Context, arg sqlc.CreateUserParams) (sqlc.User, error) {
	s.staged.users++
	return sqlc.User{
		ID:          mustUUID(s.t, "00000000-0000-0000-0000-000000000a01"),
		OrgID:       arg.OrgID,
		Username:    arg.Username,
		DisplayName: arg.DisplayName,
		Role:        arg.Role,
		Status:      arg.Status,
	}, nil
}

func (s *onboardingStub) CreateApp(_ context.Context, arg sqlc.CreateAppParams) (sqlc.App, error) {
	if s.appErr != nil {
		return sqlc.App{}, s.appErr
	}
	s.staged.apps++
	s.lastAppNodeID = uuidToString(arg.RuntimeNodeID)
	s.lastAppOwnerID = uuidToString(arg.OwnerUserID)
	s.lastAppModelID = arg.ModelID
	s.lastAppVersionID = uuidToString(arg.VersionID)
	return sqlc.App{
		ID:           mustUUID(s.t, "00000000-0000-0000-0000-000000000b01"),
		OrgID:        arg.OrgID,
		OwnerUserID:  arg.OwnerUserID,
		Name:         arg.Name,
		Status:       arg.Status,
		ApiKeyStatus: arg.ApiKeyStatus,
		ModelID:      arg.ModelID,
	}, nil
}

func (s *onboardingStub) CreateChannelBinding(_ context.Context, arg sqlc.CreateChannelBindingParams) (sqlc.ChannelBinding, error) {
	s.staged.bindings++
	return sqlc.ChannelBinding{
		ID:          mustUUID(s.t, "00000000-0000-0000-0000-000000000c01"),
		AppID:       arg.AppID,
		ChannelType: arg.ChannelType,
		Status:      arg.Status,
	}, nil
}

func (s *onboardingStub) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error) {
	s.staged.audits++
	s.stagedAudits = append(s.stagedAudits, arg)
	return sqlc.AuditLog{ActorRole: arg.ActorRole, TargetType: arg.TargetType, TargetID: arg.TargetID, Action: arg.Action, Result: arg.Result}, nil
}

func (s *onboardingStub) CreateJob(_ context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error) {
	if s.jobErr != nil {
		return sqlc.Job{}, s.jobErr
	}
	s.staged.jobs++
	return sqlc.Job{
		ID:   mustUUID(s.t, "00000000-0000-0000-0000-000000000d01"),
		Type: arg.Type,
	}, nil
}

// nodeSelectorStub 给 selectNode 路径提供可断言的内存桩。
// 调用 ListActiveNodesWithAppCounts 时会记录调用次数与最后一次返回的节点 id。
type nodeSelectorStub struct {
	nodes      []NodeWithCount
	calledN    int
	listErr    error
	lastChosen string
}

func (s *nodeSelectorStub) ListActiveNodesWithAppCounts(_ context.Context) ([]NodeWithCount, error) {
	s.calledN++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.nodes, nil
}

// defaultTestSelector 给现有 onboarding 用例提供「至少有一个空闲节点」的默认 selector，
// 避免 NodeID 留空触发 ErrNoNodeAvailable 让既有断言被改变。
func defaultTestSelector() *nodeSelectorStub {
	return &nodeSelectorStub{nodes: []NodeWithCount{{NodeID: "00000000-0000-0000-0000-000000000a99", AppCount: 0}}}
}

func ptrInt32(v int32) *int32 { return &v }

// TestOnboardMember_SelectNode_NoActiveNode 验证引导成员Select节点无启用节点的预期行为场景。
func TestOnboardMember_SelectNode_NoActiveNode(t *testing.T) {
	tx := &txRunnerStub{store: newOnboardingStub(t)}
	selector := &nodeSelectorStub{nodes: nil}
	svc := NewMemberOnboardingService(tx, fakeHash, selector)

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.ErrorIs(t, err, ErrNoNodeAvailable)
}

// TestOnboardMember_SelectNode_OnlyNodeAtCapacity 验证引导成员Select节点仅节点At容量的预期行为场景。
func TestOnboardMember_SelectNode_OnlyNodeAtCapacity(t *testing.T) {
	tx := &txRunnerStub{store: newOnboardingStub(t)}
	selector := &nodeSelectorStub{nodes: []NodeWithCount{
		{NodeID: "00000000-0000-0000-0000-000000000a01", MaxApps: ptrInt32(1), AppCount: 1}, // 场景：唯一节点已达容量时选择器应返回无可用节点。
	}}
	svc := NewMemberOnboardingService(tx, fakeHash, selector)

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.ErrorIs(t, err, ErrNoNodeAvailable)
}

// TestOnboardMember_SelectNode_PicksLargestRemaining 验证引导成员Select节点PicksLargestRemaining的预期行为场景。
func TestOnboardMember_SelectNode_PicksLargestRemaining(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	selector := &nodeSelectorStub{nodes: []NodeWithCount{
		{NodeID: "00000000-0000-0000-0000-000000000a01", MaxApps: ptrInt32(5), AppCount: 4},  // 剩 1
		{NodeID: "00000000-0000-0000-0000-000000000a02", MaxApps: ptrInt32(10), AppCount: 3}, // 剩 7
	}}
	svc := NewMemberOnboardingService(tx, fakeHash, selector)

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.NoError(t, err)
	// 通过 selectNode 内排序，n2 应被优先选择；input.NodeID 在 onboarding 内被覆盖后
	// 通过 CreateApp 透传，校验 stub 看到的 RuntimeNodeID 即可。
	require.Equal(t, "00000000-0000-0000-0000-000000000a02", store.lastAppNodeID)
}

// TestOnboardMember_SelectNode_NULLMaxAppsTreatedAsInfinity 验证引导成员Select节点NULL最大应用Treated作为Infinity的边界条件场景。
func TestOnboardMember_SelectNode_NULLMaxAppsTreatedAsInfinity(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	selector := &nodeSelectorStub{nodes: []NodeWithCount{
		{NodeID: "00000000-0000-0000-0000-000000000a01", MaxApps: nil, AppCount: 100},        // 不限
		{NodeID: "00000000-0000-0000-0000-000000000a02", MaxApps: ptrInt32(10), AppCount: 0}, // 剩 10
	}}
	svc := NewMemberOnboardingService(tx, fakeHash, selector)

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.NoError(t, err)
	require.Equal(t, "00000000-0000-0000-0000-000000000a01", store.lastAppNodeID)
}

// TestOnboardMember_ExplicitNodeID_BypassesSelector 验证引导成员显式节点ID绕过选择器的特殊分支或幂等场景。
func TestOnboardMember_ExplicitNodeID_BypassesSelector(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	selector := &nodeSelectorStub{} // 故意空
	svc := NewMemberOnboardingService(tx, fakeHash, selector)

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		NodeID:    "00000000-0000-0000-0000-000000000099",
		VersionID: testVersionID,
	})
	require.NoError(t, err)
	assert.Zero(t, selector.calledN)
}

// TestOnboardMember_RejectsMissingVersionID 验证 VersionID 为空时 OnboardMember 返回参数错误。
func TestOnboardMember_RejectsMissingVersionID(t *testing.T) {
	tx := &txRunnerStub{store: newOnboardingStub(t)}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	// VersionID 留空，应被前置校验拦截，返回 ErrMemberCreateInvalid。
	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

// TestOnboardMember_RejectsVersionNotInAllowlist 验证所选助手版本不在组织 allowlist 内时拒绝创建。
func TestOnboardMember_RejectsVersionNotInAllowlist(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	// 使用一个不在 org.AssistantVersionIds 内的版本 ID，应触发 allowlist 校验失败。
	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username:    "alice",
		DisplayName: "Alice",
		Password:    "pwd",
		AppName:     "alice-bot",
		VersionID:   "00000000-0000-0000-0000-000000000fff", // 不在 allowlist 内
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	require.False(t, tx.committed)
}

// TestOnboardMember_HappyPath_VersionIDRecorded 验证正常路径下创建的应用绑定了正确的助手版本 ID。
func TestOnboardMember_HappyPath_VersionIDRecorded(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	// 正常路径：VersionID 在 allowlist 内，应成功创建并记录版本绑定。
	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.NoError(t, err)
	require.True(t, tx.committed)
	// CreateApp 被调用时传入的 VersionID 应等于 testVersionID。
	assert.Equal(t, testVersionID, store.lastAppVersionID)
}

// TestCreateAppForMember_RejectsMissingVersionID 验证 VersionID 为空时 CreateAppForMember 返回参数错误。
func TestCreateAppForMember_RejectsMissingVersionID(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	// VersionID 留空，应被前置校验拦截，返回 ErrMemberCreateInvalid。
	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, uuidToString(store.user.ID), CreateAppForMemberInput{
		AppName: "alice-new-bot",
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

// TestCreateAppForMember_RejectsVersionNotInAllowlist 验证所选助手版本不在组织 allowlist 内时拒绝创建。
func TestCreateAppForMember_RejectsVersionNotInAllowlist(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	// 使用一个不在 org.AssistantVersionIds 内的版本 ID，应触发 allowlist 校验失败。
	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, uuidToString(store.user.ID), CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		VersionID: "00000000-0000-0000-0000-000000000fff", // 不在 allowlist 内
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	require.False(t, tx.committed)
}

func orgOnboardingAdmin() auth.Principal {
	return auth.Principal{
		Role:   domain.UserRoleOrgAdmin,
		OrgID:  testOrgID,
		UserID: "00000000-0000-0000-0000-0000000000aa",
	}
}
