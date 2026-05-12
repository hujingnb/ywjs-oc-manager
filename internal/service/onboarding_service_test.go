package service

import (
	"context"
	"errors"
	"testing"

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
	assert.Positive(t, store.audits)
	assert.Positive(t, store.jobs)
}

// TestOnboardMemberRollsBackWhenAppCreationFails 验证引导成员回滚回退当应用Creation失败的预期行为场景。
func TestOnboardMemberRollsBackWhenAppCreationFails(t *testing.T) {
	store := newOnboardingStub(t)
	store.appErr = errors.New("duplicate app for owner")
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
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
	})
	require.ErrorIs(t, err, ErrForbidden)
}

// TestOnboardMemberPlatformAdminForbidden 验证引导成员平台管理员禁止访问的异常或拒绝路径场景。
func TestOnboardMemberPlatformAdminForbidden(t *testing.T) {
	tx := &txRunnerStub{store: newOnboardingStub(t)}
	svc := NewMemberOnboardingService(tx, fakeHash, defaultTestSelector())

	_, err := svc.OnboardMember(context.Background(), platformAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
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
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
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
	t             *testing.T
	org           sqlc.Organization
	users         int
	apps          int
	bindings      int
	audits        int
	jobs          int
	staged        counters
	appErr        error
	jobErr        error
	lastAppNodeID string
}

type counters struct{ users, apps, bindings, audits, jobs int }

func newOnboardingStub(t *testing.T) *onboardingStub {
	return &onboardingStub{
		t:   t,
		org: sqlc.Organization{ID: mustUUID(t, testOrgID), Status: domain.StatusActive, Name: "测试组织"},
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
	s.jobs += s.staged.jobs
	s.staged = counters{}
}

func (s *onboardingStub) GetOrganization(_ context.Context, id pgtype.UUID) (sqlc.Organization, error) {
	if id != s.org.ID {
		return sqlc.Organization{}, errors.New("not found")
	}
	return s.org, nil
}

func (s *onboardingStub) CreateUser(_ context.Context, arg sqlc.CreateUserParams) (sqlc.User, error) {
	s.staged.users++
	return sqlc.User{
		ID:       mustUUID(s.t, "00000000-0000-0000-0000-000000000a01"),
		OrgID:    arg.OrgID,
		Username: arg.Username,
		Role:     arg.Role,
		Status:   arg.Status,
	}, nil
}

func (s *onboardingStub) CreateApp(_ context.Context, arg sqlc.CreateAppParams) (sqlc.App, error) {
	if s.appErr != nil {
		return sqlc.App{}, s.appErr
	}
	s.staged.apps++
	s.lastAppNodeID = uuidToString(arg.RuntimeNodeID)
	return sqlc.App{
		ID:           mustUUID(s.t, "00000000-0000-0000-0000-000000000b01"),
		OrgID:        arg.OrgID,
		OwnerUserID:  arg.OwnerUserID,
		Name:         arg.Name,
		Status:       arg.Status,
		PersonaMode:  arg.PersonaMode,
		ApiKeyStatus: arg.ApiKeyStatus,
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
		NodeID: "00000000-0000-0000-0000-000000000099",
	})
	require.NoError(t, err)
	assert.Zero(t, selector.calledN)
}

func orgOnboardingAdmin() auth.Principal {
	return auth.Principal{
		Role:   domain.UserRoleOrgAdmin,
		OrgID:  testOrgID,
		UserID: "00000000-0000-0000-0000-0000000000aa",
	}
}
