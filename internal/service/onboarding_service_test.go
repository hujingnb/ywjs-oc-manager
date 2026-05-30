package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	null "github.com/guregu/null/v5"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// TestOnboardMemberCommitsOnSuccess 验证引导成员提交成功的完整事务路径，包括用户、应用、审计、任务均落库。
func TestOnboardMemberCommitsOnSuccess(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

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

// TestOnboardMemberEnsuresKnowledgeDataset 验证成员引导成功创建实例后会预创建实例级 RAGFlow dataset。
func TestOnboardMemberEnsuresKnowledgeDataset(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	kb := &knowledgeDatasetProvisionerStub{}
	svc := NewMemberOnboardingService(tx, fakeHash)
	svc.SetKnowledgeDatasetProvisioner(kb)

	result, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.NoError(t, err)

	require.Len(t, kb.apps, 1)
	assert.Equal(t, result.App.ID, kb.apps[0].ID)
}

// TestOnboardMemberRollsBackWhenAppCreationFails 验证应用创建失败时整个事务回滚，不留悬挂状态。
func TestOnboardMemberRollsBackWhenAppCreationFails(t *testing.T) {
	store := newOnboardingStub(t)
	store.appErr = errors.New("duplicate app for owner")
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.Error(t, err)
	require.False(t, tx.committed)
}

// TestOnboardMemberRollsBackWhenJobCreationFails 验证任务创建失败时整个事务回滚，不留悬挂用户/应用。
func TestOnboardMemberRollsBackWhenJobCreationFails(t *testing.T) {
	store := newOnboardingStub(t)
	store.jobErr = errors.New("redis blocked")
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.Error(t, err)
	require.False(t, tx.committed)
}

// TestOnboardMemberRequiresOrgManagement 验证跨组织管理员无法引导其他组织成员（权限拒绝）。
func TestOnboardMemberRequiresOrgManagement(t *testing.T) {
	tx := &txRunnerStub{store: newOnboardingStub(t)}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.OnboardMember(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testOrg2ID}, testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.ErrorIs(t, err, ErrForbidden)
}

// TestOnboardMemberPlatformAdminForbidden 验证平台管理员不能直接引导成员（必须由组织管理员发起）。
func TestOnboardMemberPlatformAdminForbidden(t *testing.T) {
	tx := &txRunnerStub{store: newOnboardingStub(t)}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.OnboardMember(context.Background(), platformAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.ErrorIs(t, err, ErrForbidden)
}

// TestOnboardMemberRejectsDisabledOrg 验证引导成员时企业已停用返回业务错误，不写入任何数据。
func TestOnboardMemberRejectsDisabledOrg(t *testing.T) {
	store := newOnboardingStub(t)
	store.org.Status = domain.StatusDisabled
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	require.ErrorContains(t, err, "企业已停用")
}

// TestCreateAppForMember_RejectsDisabledOrg 验证已有成员补建实例时企业停用会返回可展示的业务错误。
func TestCreateAppForMember_RejectsDisabledOrg(t *testing.T) {
	store := newOnboardingStub(t)
	store.org.Status = domain.StatusDisabled
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		VersionID: testVersionID,
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	require.ErrorContains(t, err, "企业已停用")
	require.False(t, tx.committed)
}

// TestCreateAppForMember_PlatformAdminCreatesAfterDelete 验证平台管理员可为无活跃实例的已有成员创建新实例。
func TestCreateAppForMember_PlatformAdminCreatesAfterDelete(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	result, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		VersionID: testVersionID,
	})

	require.NoError(t, err)
	require.True(t, tx.committed)
	assert.Equal(t, "alice-new-bot", result.App.Name)
	assert.Equal(t, store.user.ID, store.lastAppOwnerID)
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

// TestCreateAppForMember_RejectsExistingActiveApp 验证成员已有未删除实例时拒绝创建新实例。
func TestCreateAppForMember_RejectsExistingActiveApp(t *testing.T) {
	store := newOnboardingStub(t)
	existing := sqlc.App{ID: mustUUID(t, "00000000-0000-0000-0000-000000000b99"), OwnerUserID: store.user.ID}
	store.activeApp = &existing
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		VersionID: testVersionID,
	})

	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	require.False(t, tx.committed)
}

// TestCreateAppForMember_RejectsCrossOrgUser 验证路径组织与目标用户组织不一致时按不存在处理。
func TestCreateAppForMember_RejectsCrossOrgUser(t *testing.T) {
	store := newOnboardingStub(t)
	store.user.OrgID = null.StringFrom(testOrg2ID)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
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
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		VersionID: testVersionID,
	})

	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	require.False(t, tx.committed)
}

// TestCreateAppForMember_MapsOwnerActiveUniqueViolation 验证并发创建命中活跃实例唯一索引时归类为业务冲突。
// MySQL 侧通过 error message 含 "Duplicate entry" 和 "apps_owner_active" 来检测唯一约束冲突。
func TestCreateAppForMember_MapsOwnerActiveUniqueViolation(t *testing.T) {
	store := newOnboardingStub(t)
	// 模拟 MySQL 唯一约束冲突：错误消息含 "Duplicate entry" 和约束名称。
	store.appErr = errors.New("Duplicate entry '...' for key 'apps_owner_active'")
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
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
	stagedJobs       []sqlc.CreateJobParams // 暂存 job 参数，事务提交后可检查 payload。
	committedJobs    []sqlc.CreateJobParams // 已提交的 job，供测试断言 payload 内容。
	appErr           error
	jobErr           error
	lastAppOwnerID   string
	lastAppVersionID string // 记录最近一次 CreateApp 使用的 VersionID，供断言校验。
}

type counters struct{ users, apps, bindings, audits, jobs int }

// testVersionID 是测试用的固定助手版本 UUID，存入 org.AssistantVersionIds allowlist 供校验通过。
const testVersionID = "00000000-0000-0000-0000-000000000f01"

func newOnboardingStub(t *testing.T) *onboardingStub {
	return &onboardingStub{
		t: t,
		org: sqlc.Organization{
			ID:     mustUUID(t, testOrgID),
			Status: domain.StatusActive,
			Name:   "测试组织",
			// 预置 allowlist，包含 testVersionID，供创建实例时的版本校验通过。
			AssistantVersionIds: []byte(`["` + testVersionID + `"]`),
		},
		user: sqlc.User{
			ID:          mustUUID(t, "00000000-0000-0000-0000-000000000a11"),
			OrgID:       null.StringFrom(mustUUID(t, testOrgID)),
			Username:    "alice",
			DisplayName: "Alice",
			Role:        domain.UserRoleOrgMember,
			Status:      domain.StatusActive,
		},
	}
}

func (s *onboardingStub) reset() {
	s.staged = counters{}
	s.stagedJobs = nil
}

func (s *onboardingStub) commit() {
	s.users += s.staged.users
	s.apps += s.staged.apps
	s.bindings += s.staged.bindings
	s.audits += s.staged.audits
	s.auditLogs = append(s.auditLogs, s.stagedAudits...)
	s.jobs += s.staged.jobs
	s.committedJobs = append(s.committedJobs, s.stagedJobs...)
	s.staged = counters{}
	s.stagedAudits = nil
	s.stagedJobs = nil
}

func (s *onboardingStub) GetOrganization(_ context.Context, id string) (sqlc.Organization, error) {
	if id != s.org.ID {
		return sqlc.Organization{}, errors.New("not found")
	}
	return s.org, nil
}

func (s *onboardingStub) GetUser(_ context.Context, id string) (sqlc.User, error) {
	if id != s.user.ID {
		return sqlc.User{}, sql.ErrNoRows
	}
	return s.user, nil
}

func (s *onboardingStub) GetActiveAppByOwner(_ context.Context, ownerUserID string) (sqlc.App, error) {
	if s.activeApp == nil || ownerUserID != s.activeApp.OwnerUserID {
		return sqlc.App{}, sql.ErrNoRows
	}
	return *s.activeApp, nil
}

// CreateUser 为 :exec；stub 计数后服务用自生成 ID 继续处理，不需要读回。
func (s *onboardingStub) CreateUser(_ context.Context, _ sqlc.CreateUserParams) error {
	s.staged.users++
	return nil
}

// CreateApp 为 :exec；stub 计数并记录 owner ID / version ID，不需要读回。
// k8s 模型下 CreateAppParams 不含 RuntimeNodeID，验证字段无需断言节点。
func (s *onboardingStub) CreateApp(_ context.Context, arg sqlc.CreateAppParams) error {
	if s.appErr != nil {
		return s.appErr
	}
	s.staged.apps++
	s.lastAppOwnerID = arg.OwnerUserID
	// VersionID 为 null.String；取 .String 字段即可（有效时等于 version id）。
	s.lastAppVersionID = arg.VersionID.String
	return nil
}

func (s *onboardingStub) CreateChannelBinding(_ context.Context, _ sqlc.CreateChannelBindingParams) error {
	s.staged.bindings++
	return nil
}

// CreateAuditLog 为 :exec；stub 暂存审计记录，事务提交时合并。
func (s *onboardingStub) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) error {
	s.staged.audits++
	s.stagedAudits = append(s.stagedAudits, arg)
	return nil
}

// CreateJob 为 :exec；stub 计数并暂存 job 参数，供测试断言 payload。
func (s *onboardingStub) CreateJob(_ context.Context, arg sqlc.CreateJobParams) error {
	if s.jobErr != nil {
		return s.jobErr
	}
	s.staged.jobs++
	s.stagedJobs = append(s.stagedJobs, arg)
	return nil
}

// TestOnboardMember_RejectsMissingVersionID 验证 VersionID 为空时 OnboardMember 返回参数错误。
func TestOnboardMember_RejectsMissingVersionID(t *testing.T) {
	tx := &txRunnerStub{store: newOnboardingStub(t)}
	svc := NewMemberOnboardingService(tx, fakeHash)

	// VersionID 留空，应被前置校验拦截，返回 ErrMemberCreateInvalid。
	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

// TestOnboardMember_RejectsVersionNotInAllowlist 验证所选助手版本不在企业 allowlist 内时拒绝创建。
func TestOnboardMember_RejectsVersionNotInAllowlist(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

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
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.NoError(t, err)
	require.True(t, tx.committed)
	assert.Equal(t, testVersionID, store.lastAppVersionID)
}

// TestCreateAppForMember_RejectsMissingVersionID 验证 VersionID 为空时 CreateAppForMember 返回参数错误。
func TestCreateAppForMember_RejectsMissingVersionID(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName: "alice-new-bot",
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

// TestCreateAppForMember_RejectsVersionNotInAllowlist 验证所选助手版本不在企业 allowlist 内时拒绝创建。
func TestCreateAppForMember_RejectsVersionNotInAllowlist(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		VersionID: "00000000-0000-0000-0000-000000000fff", // 不在 allowlist 内
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	require.False(t, tx.committed)
}

// TestOnboardMember_JobPayloadOnlyContainsAppID 验证 k8s 模型下入队 payload 只含 app_id，不含 runtime_node。
func TestOnboardMember_JobPayloadOnlyContainsAppID(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	result, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})
	require.NoError(t, err)
	require.Len(t, store.committedJobs, 1)

	// payload 应只含 app_id，不含 runtime_node；k8s 调度器负责落点。
	var payload map[string]any
	require.NoError(t, json.Unmarshal(store.committedJobs[0].PayloadJson, &payload))
	assert.Equal(t, result.App.ID, payload["app_id"])
	assert.NotContains(t, payload, "runtime_node", "k8s 模型下 payload 不应包含 runtime_node")
}

// TestCreateAppForMember_JobPayloadOnlyContainsAppID 验证 k8s 模型下补建实例入队 payload 只含 app_id，不含 runtime_node。
func TestCreateAppForMember_JobPayloadOnlyContainsAppID(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	result, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		VersionID: testVersionID,
	})
	require.NoError(t, err)
	require.Len(t, store.committedJobs, 1)

	// payload 应只含 app_id，不含 runtime_node；k8s 调度器负责落点。
	var payload map[string]any
	require.NoError(t, json.Unmarshal(store.committedJobs[0].PayloadJson, &payload))
	assert.Equal(t, result.App.ID, payload["app_id"])
	assert.NotContains(t, payload, "runtime_node", "k8s 模型下 payload 不应包含 runtime_node")
}

func orgOnboardingAdmin() auth.Principal {
	return auth.Principal{
		Role:   domain.UserRoleOrgAdmin,
		OrgID:  testOrgID,
		UserID: "00000000-0000-0000-0000-0000000000aa",
	}
}
