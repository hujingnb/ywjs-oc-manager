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
	// 审计迁移：不再写冻结中文文案，改用 metadata 存储结构化参数供前端按语言渲染。
	// auditLogs[0] = create_with_app; metadata 应包含 member_name 和 app_name。
	require.False(t, store.auditLogs[0].DetailMessage.Valid, "create_with_app 不应写入冻结文案")
	var memberMeta map[string]any
	require.NoError(t, json.Unmarshal(store.auditLogs[0].MetadataJson, &memberMeta))
	require.Equal(t, "Alice", memberMeta["member_name"], "metadata.member_name 应为显示名")
	require.Equal(t, "alice-bot", memberMeta["app_name"], "metadata.app_name 应为应用名")
	// auditLogs[1] = app create; metadata 应包含 member_name/app_name/channel_type/owner_user_id。
	require.False(t, store.auditLogs[1].DetailMessage.Valid, "app create 不应写入冻结文案")
	var appMeta map[string]any
	require.NoError(t, json.Unmarshal(store.auditLogs[1].MetadataJson, &appMeta))
	require.Equal(t, "Alice", appMeta["member_name"], "metadata.member_name 应为归属成员显示名")
	require.Equal(t, "alice-bot", appMeta["app_name"], "metadata.app_name 应为应用名")
	require.Equal(t, domain.ChannelTypeWeChat, appMeta["channel_type"], "metadata.channel_type 应为渠道类型 code")
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
	// 审计迁移：不再写冻结中文文案，改用 metadata 存储结构化参数供前端按语言渲染。
	// create_for_existing_member 的 metadata 应包含 member_name/app_name/channel_type/owner_user_id。
	require.False(t, store.auditLogs[0].DetailMessage.Valid, "create_for_existing_member 不应写入冻结文案")
	require.NotEmpty(t, store.auditLogs[0].MetadataJson, "create_for_existing_member 应写入 metadata")
	var appMeta map[string]any
	require.NoError(t, json.Unmarshal(store.auditLogs[0].MetadataJson, &appMeta))
	require.Equal(t, domain.ChannelTypeWeChat, appMeta["channel_type"], "metadata.channel_type 应为渠道类型 code")
	require.NotEmpty(t, appMeta["member_name"], "metadata.member_name 应为归属成员名")
	require.Equal(t, "alice-new-bot", appMeta["app_name"], "metadata.app_name 应为应用名")
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
	t                    *testing.T
	org                  sqlc.Organization
	user                 sqlc.User
	// creatorUser 是操作者（创建者 principal）的用户记录，用于模拟 GetUser(principal.UserID)。
	// 设置后 GetUser 会同时匹配 user.ID 和 creatorUser.ID。
	creatorUser          *sqlc.User
	activeApp            *sqlc.App
	activeAppCount       int64 // 模拟 CountActiveAppsByOrg 返回的企业未删除实例数。
	users                int
	apps                 int
	bindings             int
	audits               int
	auditLogs            []sqlc.CreateAuditLogParams
	jobs                 int
	staged               counters
	stagedAudits         []sqlc.CreateAuditLogParams
	stagedJobs           []sqlc.CreateJobParams // 暂存 job 参数，事务提交后可检查 payload。
	committedJobs        []sqlc.CreateJobParams // 已提交的 job，供测试断言 payload 内容。
	appErr               error
	jobErr               error
	lastAppOwnerID             string
	lastAppVersionID           string      // 记录最近一次 CreateApp 使用的 VersionID，供断言校验。
	lastAppLocale              null.String // 记录最近一次 CreateApp 使用的 Locale，供断言校验。
	lastAppKnowledgeQuotaBytes int64       // 记录最近一次 CreateApp 使用的知识库配额，供断言校验继承企业默认值。
	lastCreateUserParams       sqlc.CreateUserParams // 记录最近一次 CreateUser 的完整入参，供断言校验。
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
	// 优先匹配操作者（创建者）记录，再匹配被创建成员记录。
	if s.creatorUser != nil && id == s.creatorUser.ID {
		return *s.creatorUser, nil
	}
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

// CountActiveAppsByOrg 返回 stub 预置的企业未删除实例数，供实例上限校验测试。
func (s *onboardingStub) CountActiveAppsByOrg(_ context.Context, _ string) (int64, error) {
	return s.activeAppCount, nil
}

// CreateUser 为 :exec；stub 计数并捕获入参，服务用自生成 ID 继续处理，不需要读回。
// lastCreateUserParams 供测试断言 Locale 等字段是否被正确赋值。
func (s *onboardingStub) CreateUser(_ context.Context, arg sqlc.CreateUserParams) error {
	s.staged.users++
	s.lastCreateUserParams = arg
	return nil
}

// CreateApp 为 :exec；stub 计数并记录 owner ID / version ID / locale，不需要读回。
// k8s 模型下 CreateAppParams 不含 RuntimeNodeID，验证字段无需断言节点。
func (s *onboardingStub) CreateApp(_ context.Context, arg sqlc.CreateAppParams) error {
	if s.appErr != nil {
		return s.appErr
	}
	s.staged.apps++
	s.lastAppOwnerID = arg.OwnerUserID
	// VersionID 为 null.String；取 .String 字段即可（有效时等于 version id）。
	s.lastAppVersionID = arg.VersionID.String
	// Locale 快照：记录创建时传入的 locale，供断言校验。
	s.lastAppLocale = arg.Locale
	// 记录传入的知识库配额，验证新实例继承所属企业的默认配额。
	s.lastAppKnowledgeQuotaBytes = arg.KnowledgeQuotaBytes
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

// TestCreateAppForMember_RejectsWhenInstanceLimitReached 验证企业已达实例上限时补建实例被拒。
// 边界：当前未删除实例数 == 上限（3），应返回 ErrInstanceLimitReached 且事务回滚。
func TestCreateAppForMember_RejectsWhenInstanceLimitReached(t *testing.T) {
	store := newOnboardingStub(t)
	store.org.MaxInstanceCount = null.IntFrom(3) // 企业上限 3
	store.activeAppCount = 3                      // 当前已 3 个未删除实例，达到上限
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName: "alice-new-bot", VersionID: testVersionID,
	})

	require.ErrorIs(t, err, ErrInstanceLimitReached)
	require.False(t, tx.committed)
}

// TestCreateAppForMember_AllowsBelowInstanceLimit 验证未达上限时可正常补建实例。
// 边界：当前 2 个 < 上限 3，应创建成功。
func TestCreateAppForMember_AllowsBelowInstanceLimit(t *testing.T) {
	store := newOnboardingStub(t)
	store.org.MaxInstanceCount = null.IntFrom(3)
	store.activeAppCount = 2
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	result, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName: "alice-new-bot", VersionID: testVersionID,
	})

	require.NoError(t, err)
	require.True(t, tx.committed)
	assert.Equal(t, "alice-new-bot", result.App.Name)
}

// TestCreateAppForMember_AllowsWhenLimitUnset 验证上限为 NULL（不限制）时即便实例数很大也可创建。
func TestCreateAppForMember_AllowsWhenLimitUnset(t *testing.T) {
	store := newOnboardingStub(t)
	store.org.MaxInstanceCount = null.Int{} // NULL = 不限制
	store.activeAppCount = 999
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName: "alice-new-bot", VersionID: testVersionID,
	})

	require.NoError(t, err)
	require.True(t, tx.committed)
}

// TestOnboardMember_RejectsWhenInstanceLimitReached 验证 onboard 成员（建成员+实例）入口同样受上限约束。
func TestOnboardMember_RejectsWhenInstanceLimitReached(t *testing.T) {
	store := newOnboardingStub(t)
	store.org.MaxInstanceCount = null.IntFrom(2)
	store.activeAppCount = 2
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "bob", DisplayName: "Bob", Password: "secret-123",
		AppName: "bob-bot", VersionID: testVersionID,
	})

	require.ErrorIs(t, err, ErrInstanceLimitReached)
	require.False(t, tx.committed)
}

// TestOnboardMember_LocaleSnapshotUsesDefaultWhenOwnerHasNoLocale 验证 OnboardMember 创建实例时，
// 新成员尚无语言偏好，应使用平台默认语言作为 app.locale。
func TestOnboardMember_LocaleSnapshotUsesDefaultWhenOwnerHasNoLocale(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	// 传入平台默认语言 "zh"。
	svc := NewMemberOnboardingService(tx, fakeHash, "zh")

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "carol", DisplayName: "Carol", Password: "secret-456",
		AppName: "carol-bot", VersionID: testVersionID,
	})

	require.NoError(t, err)
	// 新成员无语言偏好，app.locale 应回退平台默认 "zh"。
	require.True(t, store.lastAppLocale.Valid, "locale 应被写入（非 NULL）")
	assert.Equal(t, "zh", store.lastAppLocale.String, "app.locale 应等于平台默认语言")
}

// TestCreateAppForMember_LocaleSnapshotOwnerLocale 验证 CreateAppForMember 创建实例时，
// owner 已设置语言偏好，快照其 locale 到 app.locale。
func TestCreateAppForMember_LocaleSnapshotOwnerLocale(t *testing.T) {
	store := newOnboardingStub(t)
	// owner 已设置语言偏好 "en"。
	store.user.Locale = null.StringFrom("en")
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, "zh")

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName:   "en-bot",
		VersionID: testVersionID,
	})

	require.NoError(t, err)
	// owner locale="en"，app.locale 应快照为 "en"（不使用平台默认 zh）。
	require.True(t, store.lastAppLocale.Valid, "locale 应被写入（非 NULL）")
	assert.Equal(t, "en", store.lastAppLocale.String, "app.locale 应等于 owner 的语言偏好")
}

// TestCreateAppForMember_LocaleSnapshotFallsBackToDefaultWhenOwnerLocaleEmpty 验证 CreateAppForMember
// 创建实例时，owner 未设置语言偏好，回退到平台默认语言。
func TestCreateAppForMember_LocaleSnapshotFallsBackToDefaultWhenOwnerLocaleEmpty(t *testing.T) {
	store := newOnboardingStub(t)
	// owner 无语言偏好（Locale 为 NULL）。
	store.user.Locale = null.String{}
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash, "en")

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName:   "default-bot",
		VersionID: testVersionID,
	})

	require.NoError(t, err)
	// owner locale 未设置，app.locale 应回退平台默认 "en"。
	require.True(t, store.lastAppLocale.Valid, "locale 应被写入（非 NULL）")
	assert.Equal(t, "en", store.lastAppLocale.String, "owner locale 未设置时应回退平台默认")
}

// TestOnboardMemberInheritsCreatorLocale 验证 OnboardMember 时，
// 创建者（操作 principal）的 users.locale 被传播到新成员 Locale 与新实例 apps.locale。
// 场景：创建者 locale=zh → 新成员 users.locale 与新实例 apps.locale 均为 zh。
func TestOnboardMemberInheritsCreatorLocale(t *testing.T) {
	store := newOnboardingStub(t)
	// 设置操作者（创建者）的 locale 为 "zh"；creatorUser.ID 对应 orgOnboardingAdmin().UserID。
	creatorID := "00000000-0000-0000-0000-0000000000aa"
	store.creatorUser = &sqlc.User{
		ID:     creatorID,
		Locale: null.StringFrom("zh"),
	}
	tx := &txRunnerStub{store: store}
	// 平台默认 "en"，但创建者已设置 "zh"，应优先使用创建者语言。
	svc := NewMemberOnboardingService(tx, fakeHash, "en")

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "dave", DisplayName: "Dave", Password: "pwd-abc",
		AppName: "dave-bot", VersionID: testVersionID,
	})
	require.NoError(t, err)
	require.True(t, tx.committed)

	// 新成员 CreateUser 应使用创建者 locale "zh"。
	assert.Equal(t, null.StringFrom("zh"), store.lastCreateUserParams.Locale,
		"新成员 users.locale 应等于创建者 locale")
	// 新实例 CreateApp 应使用同一 locale "zh"。
	require.True(t, store.lastAppLocale.Valid, "apps.locale 应被写入（非 NULL）")
	assert.Equal(t, "zh", store.lastAppLocale.String,
		"新实例 apps.locale 应等于创建者 locale")
}

// TestOnboardMember_InheritsOrgDefaultAppKnowledgeQuota 验证新成员实例继承所属企业的个人知识库默认配额。
// 覆盖正常路径：企业默认配额非 1GB 时，新建实例的 knowledge_quota_bytes 应等于企业设置值而非 DB 默认。
func TestOnboardMember_InheritsOrgDefaultAppKnowledgeQuota(t *testing.T) {
	store := newOnboardingStub(t)
	// 设置企业默认知识库配额为 8GB，验证新建实例是否继承该值。
	store.org.DefaultAppKnowledgeQuotaBytes = 8 * 1024 * 1024 * 1024
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
		VersionID: testVersionID,
	})

	require.NoError(t, err)
	require.True(t, tx.committed)
	// 新实例 knowledge_quota_bytes 应等于企业 default_app_knowledge_quota_bytes，而非 DB 默认 1GB。
	assert.Equal(t, int64(8*1024*1024*1024), store.lastAppKnowledgeQuotaBytes)
}

// TestCreateAppForMember_InheritsOrgDefaultAppKnowledgeQuota 验证为已有成员补建实例时同样继承企业默认配额。
// 覆盖正常路径：CreateAppForMember 路径下 knowledge_quota_bytes 亦来自企业设置值。
func TestCreateAppForMember_InheritsOrgDefaultAppKnowledgeQuota(t *testing.T) {
	store := newOnboardingStub(t)
	// 设置企业默认知识库配额为 8GB，验证补建实例是否继承该值。
	store.org.DefaultAppKnowledgeQuotaBytes = 8 * 1024 * 1024 * 1024
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName:   "alice-new-bot",
		VersionID: testVersionID,
	})

	require.NoError(t, err)
	require.True(t, tx.committed)
	// 补建实例 knowledge_quota_bytes 应等于企业 default_app_knowledge_quota_bytes，而非 DB 默认 1GB。
	assert.Equal(t, int64(8*1024*1024*1024), store.lastAppKnowledgeQuotaBytes)
}

// TestOnboardMemberFallsBackToDefaultLocale 验证 OnboardMember 时，
// 创建者 locale 为空，新成员与新实例应回落平台默认语言。
// 场景：创建者 locale 为空 → 新成员与新实例 locale 均回落平台默认 "en"。
func TestOnboardMemberFallsBackToDefaultLocale(t *testing.T) {
	store := newOnboardingStub(t)
	// 设置创建者，但 Locale 为空（未设置语言偏好）。
	creatorID := "00000000-0000-0000-0000-0000000000aa"
	store.creatorUser = &sqlc.User{
		ID:     creatorID,
		Locale: null.String{}, // locale 无效（NULL）
	}
	tx := &txRunnerStub{store: store}
	// 平台默认 "en"；创建者无语言设置，应回落此默认。
	svc := NewMemberOnboardingService(tx, fakeHash, "en")

	_, err := svc.OnboardMember(context.Background(), orgOnboardingAdmin(), testOrgID, OnboardMemberInput{
		Username: "eve", DisplayName: "Eve", Password: "pwd-xyz",
		AppName: "eve-bot", VersionID: testVersionID,
	})
	require.NoError(t, err)
	require.True(t, tx.committed)

	// 新成员 CreateUser 应回落平台默认 "en"。
	assert.Equal(t, null.StringFrom("en"), store.lastCreateUserParams.Locale,
		"创建者 locale 为空时新成员 users.locale 应回落平台默认")
	// 新实例 CreateApp 应同样回落平台默认 "en"。
	require.True(t, store.lastAppLocale.Valid, "apps.locale 应被写入（非 NULL）")
	assert.Equal(t, "en", store.lastAppLocale.String,
		"创建者 locale 为空时新实例 apps.locale 应回落平台默认")
}
