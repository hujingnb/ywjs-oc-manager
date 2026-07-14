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
	"oc-manager/internal/config"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/ocops"
	"oc-manager/internal/store/sqlc"
)

const (
	testAppServiceAppID = "00000000-0000-0000-0000-000000002001"
	// testSwitchVersionID 是用于 SwitchAppVersion 测试的目标助手版本 id。
	testSwitchVersionID = "00000000-0000-0000-0000-000000003001"
	// testSwitchVersionID2 是第二个助手版本 id，不在组织 allowlist 内，用于拒绝场景测试。
	testSwitchVersionID2 = "00000000-0000-0000-0000-000000003002"
)

// TestComputePlatformPromptPendingRestart 校验「平台 prompt 需重启」判定：
// 每个应用只与自身类型的当前规则 hash 比较；另一类型 hash 或空值均需重启。
func TestComputePlatformPromptPendingRestart(t *testing.T) {
	// 普通实例匹配普通规则 hash 时已是最新版本，不提示重启。
	assert.False(t, computePlatformPromptPendingRestart(sqlc.App{
		AiccHidden:                false,
		AppliedPlatformPromptHash: config.PlatformPromptHash(false),
	}))
	// 普通实例持有 AICC 规则 hash 时属于错误规则版本，必须提示重启。
	assert.True(t, computePlatformPromptPendingRestart(sqlc.App{
		AiccHidden:                false,
		AppliedPlatformPromptHash: config.PlatformPromptHash(true),
	}))
	// AICC 隐藏实例匹配客服规则 hash 时已是最新版本，不提示重启。
	assert.False(t, computePlatformPromptPendingRestart(sqlc.App{
		AiccHidden:                true,
		AppliedPlatformPromptHash: config.PlatformPromptHash(true),
	}))
	// AICC 隐藏实例持有普通规则 hash 时属于错误规则版本，必须提示重启。
	assert.True(t, computePlatformPromptPendingRestart(sqlc.App{
		AiccHidden:                true,
		AppliedPlatformPromptHash: config.PlatformPromptHash(false),
	}))
}

// TestGetAppExposeRuntimeImageOnlyToPlatformAdmin 验证 RuntimeImageRef 和 RuntimeImageSha256
// 仅在平台管理员调用 Get 时返回；组织管理员调用时两字段应为空。
// TestGetAppWebPublishPendingRestart 覆盖 web_publish_pending_restart 三种判定:
// 企业已开通ready+实例未注入→true;实例已注入→false;企业未配置→false。
func TestGetAppWebPublishPendingRestart(t *testing.T) {
	// 子用例:企业已开通(enabled+ready)且实例 web_publish_applied=false → 需重启提示 true。
	t.Run("企业已开通且实例未注入→true", func(t *testing.T) {
		svc, store := newAppServiceWithStore(t)
		app := store.mustSeedApp(t)
		app.WebPublishApplied = false
		store.app = app
		store.webPublishCfg = sqlc.OrgWebPublishConfig{OrgID: store.organization.ID, Enabled: true, ProvisioningStatus: domain.ProvisioningReady}

		res, err := svc.Get(context.Background(), platformAdmin(), testAppServiceAppID)
		require.NoError(t, err)
		assert.True(t, res.WebPublishPendingRestart, "企业已开通但实例未注入应提示需重启")
	})

	// 子用例:实例已注入(web_publish_applied=true)→ 不提示。
	t.Run("实例已注入→false", func(t *testing.T) {
		svc, store := newAppServiceWithStore(t)
		app := store.mustSeedApp(t)
		app.WebPublishApplied = true
		store.app = app
		store.webPublishCfg = sqlc.OrgWebPublishConfig{OrgID: store.organization.ID, Enabled: true, ProvisioningStatus: domain.ProvisioningReady}

		res, err := svc.Get(context.Background(), platformAdmin(), testAppServiceAppID)
		require.NoError(t, err)
		assert.False(t, res.WebPublishPendingRestart, "实例已注入发布能力不应提示")
	})

	// 子用例:企业未配置 web-publish(GetWebPublishConfig 返回 ErrNoRows)→ 不提示。
	t.Run("企业未配置→false", func(t *testing.T) {
		svc, store := newAppServiceWithStore(t)
		app := store.mustSeedApp(t)
		app.WebPublishApplied = false
		store.app = app
		store.webPublishErr = sql.ErrNoRows

		res, err := svc.Get(context.Background(), platformAdmin(), testAppServiceAppID)
		require.NoError(t, err)
		assert.False(t, res.WebPublishPendingRestart, "企业未开通不应提示")
	})
}

func TestGetAppExposeRuntimeImageOnlyToPlatformAdmin(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	app := store.mustSeedApp(t)
	app.RuntimeImageRef = "ghcr.io/foo/hermes:v1.2.3"
	app.RuntimeImageSha256 = "sha256:abcdef1234567890"
	store.app = app

	// 平台管理员应看到两个镜像字段。
	adminResult, err := svc.Get(context.Background(), platformAdmin(), testAppServiceAppID)
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/foo/hermes:v1.2.3", adminResult.RuntimeImageRef)
	assert.Equal(t, "sha256:abcdef1234567890", adminResult.RuntimeImageSha256)

	// 组织管理员不应看到这两个字段（omitempty 保证序列化时也不会输出）。
	orgResult, err := svc.Get(context.Background(), appOrgAdminPrincipal(store.organization), testAppServiceAppID)
	require.NoError(t, err)
	assert.Empty(t, orgResult.RuntimeImageRef)
	assert.Empty(t, orgResult.RuntimeImageSha256)
}

// TestGetAppHidesAICCHiddenApp 覆盖普通应用详情隔离：AICC 隐藏 app 只能走 AICC 管理语义，
// 不应通过 /apps/:appId 作为普通实例暴露。
func TestGetAppHidesAICCHiddenApp(t *testing.T) {
	svc, store := newAppServiceWithStore(t)
	app := store.mustSeedApp(t)
	app.AiccHidden = true
	store.app = app

	_, err := svc.Get(context.Background(), appOrgAdminPrincipal(store.organization), testAppServiceAppID)

	require.ErrorIs(t, err, ErrNotFound)
}

// TestGetAppAllowsAICCHiddenAppForAgentAdmin 覆盖 AICC 专属知识库入口：企业管理员可打开
// 绑定到 AICC 智能体的隐藏实例详情，供前端复用实例知识库上传管理页。
func TestGetAppAllowsAICCHiddenAppForAgentAdmin(t *testing.T) {
	svc, store := newAppServiceWithStore(t)
	app := store.mustSeedApp(t)
	app.AiccHidden = true
	store.app = app
	store.aiccAgent = sqlc.AiccAgent{ID: "agent-1", OrgID: store.organization.ID, AppID: testAppServiceAppID}

	result, err := svc.Get(context.Background(), appOrgAdminPrincipal(store.organization), testAppServiceAppID)

	require.NoError(t, err)
	assert.Equal(t, testAppServiceAppID, result.ID)
	assert.Equal(t, "测试实例", result.Name)
}

// TestGetAppAllowsAICCHiddenAppForPlatformViewer 覆盖平台管理员从企业列表进入 AICC 工作台后，
// 可以只读打开当前客服绑定的隐藏实例详情，供知识库页加载实例上下文。
func TestGetAppAllowsAICCHiddenAppForPlatformViewer(t *testing.T) {
	svc, store := newAppServiceWithStore(t)
	app := store.mustSeedApp(t)
	app.AiccHidden = true
	store.app = app
	store.aiccAgent = sqlc.AiccAgent{ID: "agent-1", OrgID: store.organization.ID, AppID: testAppServiceAppID}

	result, err := svc.Get(context.Background(), platformAdmin(), testAppServiceAppID)

	require.NoError(t, err)
	assert.Equal(t, testAppServiceAppID, result.ID)
	assert.Equal(t, "测试实例", result.Name)
}

func newAppServiceWithStore(t *testing.T) (*AppService, *appServiceStoreStub) {
	t.Helper()
	store := &appServiceStoreStub{
		organization: sqlc.Organization{
			ID:     mustUUID(t, testOrgID),
			Name:   "测试组织",
			Status: domain.StatusActive,
		},
		user: sqlc.User{
			ID:     mustUUID(t, testAdminUID),
			OrgID:  null.StringFrom(mustUUID(t, testOrgID)),
			Role:   domain.UserRoleOrgAdmin,
			Status: domain.StatusActive,
		},
	}
	svc := NewAppService(store)
	return svc, store
}

func appOrgAdminPrincipal(org sqlc.Organization) auth.Principal {
	return auth.Principal{
		Role:   domain.UserRoleOrgAdmin,
		OrgID:  org.ID,
		UserID: testAdminUID,
	}
}

// TestCreateHiddenAICCAppCreatesHiddenAppAndInitializeJob 覆盖 AICC 隐藏 app 创建：写 app、标记隐藏并创建初始化 job。
func TestCreateHiddenAICCAppCreatesHiddenAppAndInitializeJob(t *testing.T) {
	svc, store := newAppServiceWithStore(t)
	store.organization.AssistantVersionIds = []byte(`["` + testSwitchVersionID + `"]`)
	store.user.Locale = null.StringFrom("zh")
	notifier := &fakeNotifier{}
	svc.SetJobNotifier(notifier)

	appID, err := svc.CreateHiddenAICCApp(context.Background(), appOrgAdminPrincipal(store.organization), AICCHiddenAppInput{
		AppID:  "app-aicc-hidden-1",
		OrgID:  store.organization.ID,
		UserID: store.user.ID,
		Name:   "官网售前",
	})

	require.NoError(t, err)
	assert.Equal(t, "app-aicc-hidden-1", appID)
	assert.True(t, store.app.AiccHidden)
	assert.Equal(t, "官网售前", store.app.Name)
	assert.Equal(t, testSwitchVersionID, store.app.VersionID.String)
	assert.Equal(t, "zh", store.app.Locale.String)
	require.Len(t, store.jobs, 1)
	assert.Equal(t, domain.JobTypeAppInitialize, store.jobs[0].Type)
	assert.EqualValues(t, 20, store.jobs[0].MaxAttempts, "AICC 运行时初始化应覆盖 new-api 短暂限流的恢复窗口")
	assert.JSONEq(t, `{"app_id":"app-aicc-hidden-1"}`, string(store.jobs[0].PayloadJson))
	assert.Empty(t, notifier.lastJobID, "AICC agent 写入前不应即时唤醒 worker")
}

// TestCreateHiddenAICCAppRejectsMissingVersionAllowlist 覆盖异常路径：企业未配置模型和技能初始化版本时拒绝创建隐藏 app；
// 客服镜像来自独立配置，不影响该版本依赖。
func TestCreateHiddenAICCAppRejectsMissingVersionAllowlist(t *testing.T) {
	svc, store := newAppServiceWithStore(t)

	_, err := svc.CreateHiddenAICCApp(context.Background(), appOrgAdminPrincipal(store.organization), AICCHiddenAppInput{
		AppID:  "app-aicc-hidden-1",
		OrgID:  store.organization.ID,
		UserID: store.user.ID,
		Name:   "官网售前",
	})

	require.ErrorIs(t, err, ErrVersionNotInAllowlist)
}

// TestCreateHiddenAICCAppRollsBackAppWhenInitializeJobFails 覆盖异常路径：隐藏 app 行已写入但初始化 job 创建失败时，
// service 应软删除该 app，避免留下无法初始化且不可见的孤儿实例。
func TestCreateHiddenAICCAppRollsBackAppWhenInitializeJobFails(t *testing.T) {
	svc, store := newAppServiceWithStore(t)
	store.organization.AssistantVersionIds = []byte(`["` + testSwitchVersionID + `"]`)
	store.jobErr = errors.New("job insert failed")

	_, err := svc.CreateHiddenAICCApp(context.Background(), appOrgAdminPrincipal(store.organization), AICCHiddenAppInput{
		AppID:  "app-aicc-hidden-rollback",
		OrgID:  store.organization.ID,
		UserID: store.user.ID,
		Name:   "官网售前",
	})

	require.Error(t, err)
	assert.True(t, store.app.DeletedAt.Valid)
}

type appServiceStoreStub struct {
	organization sqlc.Organization
	user         sqlc.User
	app          sqlc.App
	aiccAgent    sqlc.AiccAgent
	// versionRevision 是 stub 返回的版本 revision，用于 WithVersion 联查。
	versionRevision int32
	// versionImageID 是 stub 返回的版本 image_id，用于 WithVersion 联查。
	versionImageID string
	jobs           []sqlc.CreateJobParams
	auditLogs      []sqlc.CreateAuditLogParams
	jobErr         error
	auditErr       error
	// setVersionCalls 记录 SetAppVersion 被调用的参数，用于断言写入行为。
	setVersionCalls []sqlc.SetAppVersionParams
	// webPublishCfg / webPublishErr 控制 GetWebPublishConfig 返回值；
	// webPublishErr 默认置 sql.ErrNoRows（企业未配置 web-publish），用于 web_publish_pending_restart 检测。
	webPublishCfg sqlc.OrgWebPublishConfig
	webPublishErr error
}

// GetAICCAgentByAppID 返回隐藏 app 绑定的 AICC 智能体；未配置时模拟数据库无行。
func (s *appServiceStoreStub) GetAICCAgentByAppID(_ context.Context, appID string) (sqlc.AiccAgent, error) {
	if s.aiccAgent.AppID != appID {
		return sqlc.AiccAgent{}, sql.ErrNoRows
	}
	return s.aiccAgent, nil
}

// GetWebPublishConfig 返回预置 web-publish 配置；webPublishErr 非 nil 时优先返回它（默认 sql.ErrNoRows）。
func (s *appServiceStoreStub) GetWebPublishConfig(_ context.Context, _ string) (sqlc.OrgWebPublishConfig, error) {
	if s.webPublishErr != nil {
		return sqlc.OrgWebPublishConfig{}, s.webPublishErr
	}
	if s.webPublishCfg.OrgID == "" {
		return sqlc.OrgWebPublishConfig{}, sql.ErrNoRows
	}
	return s.webPublishCfg, nil
}

func (s *appServiceStoreStub) mustSeedApp(t *testing.T) sqlc.App {
	t.Helper()
	// spec-A2b：runtime_node_id / container_id / container_name 已从 schema 删除，不再填充。
	s.app = sqlc.App{
		ID:           mustUUID(t, testAppServiceAppID),
		OrgID:        s.organization.ID,
		OwnerUserID:  mustUUID(t, testMemUID),
		Name:         "测试实例",
		Status:       domain.AppStatusRunning,
		ApiKeyStatus: domain.APIKeyStatusActive,
	}
	return s.app
}

// CreateApp 为 :exec；service 会传入自生成 ID，stub 记录后供 GetAppWithVersion 读回。
// k8s 模型下 CreateAppParams 不含 RuntimeNodeID，apps 表列在 Phase 3 前仍保留。
func (s *appServiceStoreStub) CreateApp(_ context.Context, arg sqlc.CreateAppParams) error {
	s.app = sqlc.App{
		ID:           arg.ID,
		OrgID:        arg.OrgID,
		OwnerUserID:  arg.OwnerUserID,
		Name:         arg.Name,
		Description:  arg.Description,
		Status:       arg.Status,
		ApiKeyStatus: arg.ApiKeyStatus,
		VersionID:    arg.VersionID,
		Locale:       arg.Locale,
		AiccHidden:   arg.AiccHidden,
	}
	return nil
}

// MarkAppAICCHidden 模拟隐藏 app 补标记，供 AppService 接口编译和隐藏 app 测试复用。
func (s *appServiceStoreStub) MarkAppAICCHidden(_ context.Context, id string) error {
	if s.app.ID != id {
		return sql.ErrNoRows
	}
	s.app.AiccHidden = true
	return nil
}

// GetUser 返回预置用户，用于隐藏 app 创建时快照 locale。
func (s *appServiceStoreStub) GetUser(_ context.Context, id string) (sqlc.User, error) {
	if s.user.ID != id {
		return sqlc.User{}, sql.ErrNoRows
	}
	return s.user, nil
}

// GetAppWithVersion 返回 app 及版本 revision / image_id，模拟联查结果。
func (s *appServiceStoreStub) GetAppWithVersion(_ context.Context, id string) (sqlc.GetAppWithVersionRow, error) {
	if s.app.ID != id {
		return sqlc.GetAppWithVersionRow{}, sql.ErrNoRows
	}
	return sqlc.GetAppWithVersionRow{
		App:             s.app,
		VersionRevision: s.versionRevision,
		VersionImageID:  s.versionImageID,
	}, nil
}

// ListAppsByOrgWithVersion 返回组织下 app 列表及版本信息，模拟联查结果。
func (s *appServiceStoreStub) ListAppsByOrgWithVersion(_ context.Context, arg sqlc.ListAppsByOrgWithVersionParams) ([]sqlc.ListAppsByOrgWithVersionRow, error) {
	if s.app.OrgID == arg.OrgID && !s.app.DeletedAt.Valid {
		return []sqlc.ListAppsByOrgWithVersionRow{
			{
				App:             s.app,
				VersionRevision: s.versionRevision,
				VersionImageID:  s.versionImageID,
			},
		}, nil
	}
	return nil, nil
}

func (s *appServiceStoreStub) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) error {
	s.app.Status = arg.Status
	return nil
}

func (s *appServiceStoreStub) SoftDeleteApp(_ context.Context, _ string) error {
	s.app.DeletedAt = null.TimeFrom(stubNow())
	return nil
}

func (s *appServiceStoreStub) CreateJob(_ context.Context, arg sqlc.CreateJobParams) error {
	if s.jobErr != nil {
		return s.jobErr
	}
	s.jobs = append(s.jobs, arg)
	return nil
}

func (s *appServiceStoreStub) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) error {
	if s.auditErr != nil {
		return s.auditErr
	}
	s.auditLogs = append(s.auditLogs, arg)
	return nil
}

// GetOrganization 返回 stub 预设的组织记录；id 不匹配时返回 sql.ErrNoRows。
func (s *appServiceStoreStub) GetOrganization(_ context.Context, id string) (sqlc.Organization, error) {
	if s.organization.ID != id {
		return sqlc.Organization{}, sql.ErrNoRows
	}
	return s.organization, nil
}

// SetAppVersion 记录调用参数并将 app.VersionID 更新到 stub 状态，模拟数据库写入。
// 同时把 AppliedVersionRevision 清零、AppliedImageRef 置空，与真实 SQL 行为一致：
// 切换版本必然让实例进入需重启态，避免新旧版本 revision 相同导致 version_synced 误判。
func (s *appServiceStoreStub) SetAppVersion(_ context.Context, arg sqlc.SetAppVersionParams) error {
	s.setVersionCalls = append(s.setVersionCalls, arg)
	s.app.VersionID = arg.VersionID
	s.app.AppliedVersionRevision = 0
	s.app.AppliedImageRef = ""
	return nil
}

// UpdateAppLocale 更新 stub 内 app 的 locale 字段，模拟数据库写入行为。
func (s *appServiceStoreStub) UpdateAppLocale(_ context.Context, arg sqlc.UpdateAppLocaleParams) error {
	if s.app.ID != arg.ID {
		return sql.ErrNoRows
	}
	s.app.Locale = arg.Locale
	return nil
}

// stubImageResolver 实现 AppImageResolver 的测试桩：固定把 image_id 映射到 ref。
type stubImageResolver struct {
	// refs 是 image_id → ref 的静态映射；id 不存在时 ResolveRuntimeImage 返回 false。
	refs map[string]string
}

// ResolveRuntimeImage 按 id 查预设映射，模拟配置驱动的解析逻辑。
func (r *stubImageResolver) ResolveRuntimeImage(id string) (string, bool) {
	ref, ok := r.refs[id]
	return ref, ok
}

// TestComputeVersionSynced 覆盖 computeVersionSynced 的核心判断路径。
func TestComputeVersionSynced(t *testing.T) {
	t.Parallel()

	resolver := &stubImageResolver{refs: map[string]string{
		"img-v1": "ghcr.io/foo/hermes:v1.0",
	}}

	tests := []struct {
		name            string
		app             sqlc.App
		versionRevision int32
		versionImageID  string
		resolver        AppImageResolver
		want            bool
	}{
		{
			// 修订一致且镜像 ref 一致：实例与版本完全对齐，无需重启。
			name: "修订一致且镜像匹配时为 true",
			app: sqlc.App{
				AppliedVersionRevision: 3,
				AppliedImageRef:        "ghcr.io/foo/hermes:v1.0",
			},
			versionRevision: 3,
			versionImageID:  "img-v1",
			resolver:        resolver,
			want:            true,
		},
		{
			// 已应用修订落后于版本修订：版本被编辑过，需重启。
			name: "修订不一致时为 false",
			app: sqlc.App{
				AppliedVersionRevision: 2,
				AppliedImageRef:        "ghcr.io/foo/hermes:v1.0",
			},
			versionRevision: 3,
			versionImageID:  "img-v1",
			resolver:        resolver,
			want:            false,
		},
		{
			// 修订一致但容器内运行的镜像 ref 与版本 image_id 解析结果不同：镜像被替换，需重启。
			name: "修订一致但镜像 ref 不匹配时为 false",
			app: sqlc.App{
				AppliedVersionRevision: 3,
				AppliedImageRef:        "ghcr.io/foo/hermes:v0.9",
			},
			versionRevision: 3,
			versionImageID:  "img-v1",
			resolver:        resolver,
			want:            false,
		},
		{
			// resolver 为 nil 时跳过镜像维度，仅靠修订对比；修订一致则视为同步。
			name: "resolver 为 nil 时仅比较修订",
			app: sqlc.App{
				AppliedVersionRevision: 5,
				AppliedImageRef:        "any-ref",
			},
			versionRevision: 5,
			versionImageID:  "img-v1",
			resolver:        nil,
			want:            true,
		},
		{
			// resolver 非 nil 但 image_id 不在配置中（ok=false）：无法解析镜像，视为未同步。
			name: "resolver 无法解析 image_id 时为 false",
			app: sqlc.App{
				AppliedVersionRevision: 4,
				AppliedImageRef:        "ghcr.io/foo/hermes:v1.0",
			},
			versionRevision: 4,
			versionImageID:  "img-unknown", // 不在 resolver.refs 中
			resolver:        resolver,
			want:            false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// 调用核心计算函数，断言返回值与预期一致。
			got := computeVersionSynced(tc.app, tc.versionRevision, tc.versionImageID, tc.resolver)
			assert.Equal(t, tc.want, got, "computeVersionSynced 结果")
		})
	}
}

// TestGetVersionSynced 通过 AppService.Get 端到端验证 version_synced 字段被正确计算。
func TestGetVersionSynced(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)

	// 注入镜像解析器：img-v1 → ghcr.io/foo/hermes:v1.0。
	svc.SetImageResolver(&stubImageResolver{refs: map[string]string{
		"img-v1": "ghcr.io/foo/hermes:v1.0",
	}})

	app := store.mustSeedApp(t)
	// 实例已应用版本 revision=2、镜像 ref 与版本一致。
	app.AppliedVersionRevision = 2
	app.AppliedImageRef = "ghcr.io/foo/hermes:v1.0"
	store.app = app
	store.versionRevision = 2
	store.versionImageID = "img-v1"

	result, err := svc.Get(context.Background(), platformAdmin(), testAppServiceAppID)
	require.NoError(t, err)
	// 修订与镜像均对齐，version_synced 应为 true。
	assert.True(t, result.VersionSynced, "实例已同步版本，version_synced 应为 true")

	// 变更场景：实例的 applied_version_revision 落后于版本最新 revision。
	store.versionRevision = 3 // 版本 revision 推进到 3，实例仍停在 2
	result2, err := svc.Get(context.Background(), platformAdmin(), testAppServiceAppID)
	require.NoError(t, err)
	assert.False(t, result2.VersionSynced, "实例 revision 落后于版本，version_synced 应为 false")
}

// fakeConfigOps 是 configOps 的测试桩：返回预设的 OcConfig，或预设错误（模拟实例不可达）。
type fakeConfigOps struct {
	cfg ocops.OcConfig // Config 成功时返回的配置
	err error          // 非 nil 时模拟 oc-ops 调用失败（实例未运行 / 不可达 / 超时）
}

// Config 按预设返回，模拟实时查询实例 display.language。
func (f *fakeConfigOps) Config(_ context.Context, _ ocops.Endpoint) (ocops.OcConfig, error) {
	if f.err != nil {
		return ocops.OcConfig{}, f.err
	}
	return f.cfg, nil
}

// fakeOcResolver 是 OcOpsResolver 的测试桩：返回固定坐标（默认 Supported + 非空基址），
// 让 AppLocaleStatus 进入实时查询分支。
type fakeOcResolver struct {
	loc OcOpsAppLocation
	err error
}

// Resolve 按预设返回坐标或错误。
func (f *fakeOcResolver) Resolve(_ context.Context, _ string) (OcOpsAppLocation, error) {
	if f.err != nil {
		return OcOpsAppLocation{}, f.err
	}
	return f.loc, nil
}

// readyResolver 返回一个已就绪的坐标桩：Supported=true 且基址非空，保证进入实时查询分支。
func readyResolver() *fakeOcResolver {
	return &fakeOcResolver{loc: OcOpsAppLocation{
		Supported: true,
		Endpoint:  ocops.Endpoint{BaseURL: "http://app-x-ocops.oc-apps.svc:8080"},
	}}
}

// TestAppLocaleStatusNeedsRestart 覆盖：实例运行中、oc-ops 实时 current=zh 与 apps.locale=en
// 不一致 → current="zh"、desired="en"、needs_restart=true（运行中实例语言漂移需重启生效）。
func TestAppLocaleStatusNeedsRestart(t *testing.T) {
	svc, store := newAppServiceWithStore(t)
	app := store.mustSeedApp(t)
	app.Locale = null.StringFrom("en") // 期望语言 en
	store.app = app
	// oc-ops 实时返回 zh，与期望 en 不一致。
	svc.SetOcOps(&fakeConfigOps{cfg: ocops.OcConfig{DisplayLanguage: "zh"}}, readyResolver())

	res, err := svc.AppLocaleStatus(context.Background(), platformAdmin(), testAppServiceAppID)
	require.NoError(t, err)
	require.NotNil(t, res.CurrentLanguage, "实例可达时 current 不应为 nil")
	assert.Equal(t, "zh", *res.CurrentLanguage)
	assert.Equal(t, "en", res.DesiredLanguage)
	assert.True(t, res.NeedsRestart, "current≠desired 时应需重启")
}

// TestAppLocaleStatusInstanceUnreachable 覆盖：oc-ops 调用失败（实例未运行 / 不可达）→
// current=nil、needs_restart=false，但 desired 仍正常返回（保证详情页可渲染）。
func TestAppLocaleStatusInstanceUnreachable(t *testing.T) {
	svc, store := newAppServiceWithStore(t)
	app := store.mustSeedApp(t)
	app.Locale = null.StringFrom("en")
	store.app = app
	// oc-ops 返回错误，模拟实例不可达。
	svc.SetOcOps(&fakeConfigOps{err: errors.New("connection refused")}, readyResolver())

	res, err := svc.AppLocaleStatus(context.Background(), platformAdmin(), testAppServiceAppID)
	require.NoError(t, err, "实例不可达不应报错，详情页要能渲染")
	assert.Nil(t, res.CurrentLanguage, "实例不可达时 current 应为 nil")
	assert.False(t, res.NeedsRestart, "current 为 nil 时不应判定需重启")
	assert.Equal(t, "en", res.DesiredLanguage, "desired 仍应正常返回")
}

// TestAppLocaleStatusForbidden 覆盖：无权访问该实例的成员调用 → ErrForbidden
// （CanViewApp 拒绝非本人非本组织管理员）。
func TestAppLocaleStatusForbidden(t *testing.T) {
	svc, store := newAppServiceWithStore(t)
	store.mustSeedApp(t)
	svc.SetOcOps(&fakeConfigOps{cfg: ocops.OcConfig{DisplayLanguage: "zh"}}, readyResolver())

	// 非实例 owner、非本组织管理员的成员（owner 为 testMemUID，这里用其它 UID）。
	_, err := svc.AppLocaleStatus(context.Background(), auth.Principal{
		Role:   domain.UserRoleOrgMember,
		OrgID:  store.organization.ID,
		UserID: testAdminUID, // 与 owner（testMemUID）不同
	}, testAppServiceAppID)
	require.ErrorIs(t, err, ErrForbidden)
}

// TestAppManagementRejectsAICCHiddenApp 覆盖普通应用管理入口隔离：AICC 隐藏 app 不允许通过
// 切换版本、修改语言或语言状态查询入口访问。
func TestAppManagementRejectsAICCHiddenApp(t *testing.T) {
	// 子场景：隐藏 app 不能通过普通实例版本切换入口修改助手版本。
	t.Run("切换版本返回不存在", func(t *testing.T) {
		svc, store := newAppServiceWithStore(t)
		app := store.mustSeedApp(t)
		app.AiccHidden = true
		store.app = app
		store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)

		_, err := svc.SwitchAppVersion(context.Background(), appOrgAdminPrincipal(store.organization), testAppServiceAppID, testSwitchVersionID)

		require.ErrorIs(t, err, ErrNotFound)
	})

	// 子场景：隐藏 app 不能通过普通实例语言入口触发重启。
	t.Run("修改语言返回不存在", func(t *testing.T) {
		svc, store := newAppServiceWithStore(t)
		app := store.mustSeedApp(t)
		app.AiccHidden = true
		store.app = app

		_, err := svc.UpdateAppLocale(context.Background(), appOrgAdminPrincipal(store.organization), testAppServiceAppID, "zh")

		require.ErrorIs(t, err, ErrNotFound)
		assert.Empty(t, store.jobs)
	})

	// 子场景：隐藏 app 不暴露普通实例语言状态。
	t.Run("语言状态返回不存在", func(t *testing.T) {
		svc, store := newAppServiceWithStore(t)
		app := store.mustSeedApp(t)
		app.AiccHidden = true
		store.app = app
		svc.SetOcOps(&fakeConfigOps{cfg: ocops.OcConfig{DisplayLanguage: "zh"}}, readyResolver())

		_, err := svc.AppLocaleStatus(context.Background(), appOrgAdminPrincipal(store.organization), testAppServiceAppID)

		require.ErrorIs(t, err, ErrNotFound)
	})
}

// mustOrgWithAllowlist 构造一个包含给定版本 id allowlist 的 Organization sqlc 记录。
func mustOrgWithAllowlist(t *testing.T, versionIDs ...string) sqlc.Organization {
	t.Helper()
	raw, err := json.Marshal(versionIDs)
	require.NoError(t, err, "序列化 allowlist 失败")
	return sqlc.Organization{
		ID:                  mustUUID(t, testOrgID),
		Name:                "测试组织",
		Status:              domain.StatusActive,
		AssistantVersionIds: raw,
	}
}

// TestSwitchAppVersionSuccess 验证组织管理员切换到 allowlist 内的版本时成功。
func TestSwitchAppVersionSuccess(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)

	app := store.mustSeedApp(t)
	app.AppliedVersionRevision = 0
	store.app = app
	store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)
	store.versionRevision = 1

	principal := appOrgAdminPrincipal(store.organization)
	result, err := svc.SwitchAppVersion(context.Background(), principal, testAppServiceAppID, testSwitchVersionID)

	require.NoError(t, err)
	assert.Equal(t, testSwitchVersionID, result.VersionID, "返回的实例 VersionID 应等于目标版本")
	assert.False(t, result.VersionSynced, "切换后 applied_* 未更新，version_synced 应为 false")
	require.Len(t, store.setVersionCalls, 1, "SetAppVersion 应被调用一次")
}

// TestSwitchAppVersionSuccessByOwnerMember 验证组织成员作为实例 owner 时可成功切换版本。
func TestSwitchAppVersionSuccessByOwnerMember(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)

	app := store.mustSeedApp(t)
	app.AppliedVersionRevision = 0
	store.app = app
	store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)
	store.versionRevision = 1

	// 构造 owner-member principal：UserID 等于实例的 OwnerUserID（testMemUID）。
	ownerMember := auth.Principal{
		Role:   domain.UserRoleOrgMember,
		OrgID:  testOrgID,
		UserID: testMemUID,
	}

	result, err := svc.SwitchAppVersion(context.Background(), ownerMember, testAppServiceAppID, testSwitchVersionID)

	require.NoError(t, err)
	assert.Equal(t, testSwitchVersionID, result.VersionID, "返回的实例 VersionID 应等于目标版本")
	assert.False(t, result.VersionSynced, "切换后 applied_* 未更新，version_synced 应为 false")
	require.Len(t, store.setVersionCalls, 1, "SetAppVersion 应被调用一次")
}

// TestSwitchAppVersionSuccessByPlatformAdmin 验证平台管理员可跨组织切换任意实例的助手版本。
func TestSwitchAppVersionSuccessByPlatformAdmin(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)

	app := store.mustSeedApp(t)
	app.AppliedVersionRevision = 0
	store.app = app
	store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)
	store.versionRevision = 1

	result, err := svc.SwitchAppVersion(context.Background(), platformAdmin(), testAppServiceAppID, testSwitchVersionID)

	require.NoError(t, err)
	assert.Equal(t, testSwitchVersionID, result.VersionID, "返回的实例 VersionID 应等于目标版本")
	assert.False(t, result.VersionSynced, "切换后 applied_* 未更新，version_synced 应为 false")
	require.Len(t, store.setVersionCalls, 1, "SetAppVersion 应被调用一次")
}

// TestSwitchAppVersionResetsAppliedSoVersionSyncedIsFalse 是针对「同 revision 误判」bug 的回归测试。
func TestSwitchAppVersionResetsAppliedSoVersionSyncedIsFalse(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)

	svc.SetImageResolver(&stubImageResolver{refs: map[string]string{
		"img-v1": "ghcr.io/foo/hermes:v1.0",
	}})

	app := store.mustSeedApp(t)
	app.AppliedVersionRevision = 2
	app.AppliedImageRef = "ghcr.io/foo/hermes:v1.0"
	store.app = app
	store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)
	// 关键碰撞条件：目标版本的 revision 也是 2，image_id 解析结果相同。
	store.versionRevision = 2
	store.versionImageID = "img-v1"

	principal := appOrgAdminPrincipal(store.organization)
	result, err := svc.SwitchAppVersion(context.Background(), principal, testAppServiceAppID, testSwitchVersionID)

	require.NoError(t, err)
	assert.Equal(t, testSwitchVersionID, result.VersionID, "返回的实例 VersionID 应等于目标版本")
	// 回归断言：applied_* 已被清零，version_synced 必须为 false。
	assert.False(t, result.VersionSynced, "新旧版本 revision 相同且镜像相同时，切换后 version_synced 仍应为 false")
	require.Len(t, store.setVersionCalls, 1, "SetAppVersion 应被调用一次")
}

// TestSwitchAppVersionNotInAllowlist 验证目标版本不在组织 allowlist 内时返回 ErrVersionNotInAllowlist。
func TestSwitchAppVersionNotInAllowlist(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)

	store.mustSeedApp(t)
	store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)

	principal := appOrgAdminPrincipal(store.organization)
	_, err := svc.SwitchAppVersion(context.Background(), principal, testAppServiceAppID, testSwitchVersionID2)
	require.ErrorIs(t, err, ErrVersionNotInAllowlist, "allowlist 外的版本应返回 ErrVersionNotInAllowlist")
	assert.Empty(t, store.setVersionCalls, "allowlist 校验失败时不应写入数据库")
}

// TestSwitchAppVersionForbidden 验证无权管理该实例的调用者被拒绝（返回 ErrForbidden）。
func TestSwitchAppVersionForbidden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		principal auth.Principal
	}{
		{
			// 属于其他组织的组织管理员无权管理本组织的实例。
			name: "其他组织的管理员无权管理",
			principal: auth.Principal{
				Role:   domain.UserRoleOrgAdmin,
				OrgID:  "00000000-0000-0000-0000-000000009999",
				UserID: testAdminUID,
			},
		},
		{
			// 组织成员只能管理自己的实例；不是实例 owner 则无权。
			name: "非 owner 组织成员无权管理",
			principal: auth.Principal{
				Role:   domain.UserRoleOrgMember,
				OrgID:  testOrgID,
				UserID: "00000000-0000-0000-0000-000000009998",
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, store := newAppServiceWithStore(t)
			store.mustSeedApp(t)
			store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)

			_, err := svc.SwitchAppVersion(context.Background(), tc.principal, testAppServiceAppID, testSwitchVersionID)
			require.ErrorIs(t, err, ErrForbidden, "无权调用者应返回 ErrForbidden")
		})
	}
}

// TestUpdateAppLocaleSuccess 验证组织管理员可成功修改实例语言，并触发重启 job 与审计日志。
func TestUpdateAppLocaleSuccess(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	store.mustSeedApp(t)

	// 正常路径：传入合法语言 "zh"，期望持久化、入队重启 job、写审计日志。
	result, err := svc.UpdateAppLocale(context.Background(), appOrgAdminPrincipal(store.organization), testAppServiceAppID, "zh")
	require.NoError(t, err)

	// 断言 locale 已写入 stub。
	assert.Equal(t, "zh", result.Locale, "返回结果的 Locale 应为 zh")
	// stub 里 app.Locale 也应更新。
	assert.Equal(t, "zh", store.app.Locale.String)

	// 断言触发了 restart job。
	require.Len(t, store.jobs, 1, "UpdateAppLocale 应入队一个 restart job")
	assert.Equal(t, "app_restart_container", store.jobs[0].Type)

	// 断言审计日志被写入，且 action=update_locale、metadata 含 locale 字段。
	require.Len(t, store.auditLogs, 1, "UpdateAppLocale 应写入一条审计日志")
	assert.Equal(t, "update_locale", store.auditLogs[0].Action)
	var meta map[string]any
	require.NoError(t, json.Unmarshal(store.auditLogs[0].MetadataJson, &meta))
	assert.Equal(t, "zh", meta["locale"], "审计 metadata 应含 locale 字段")
}

// TestUpdateAppLocaleInvalidLocale 验证不支持的语言代码被拒绝。
func TestUpdateAppLocaleInvalidLocale(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	store.mustSeedApp(t)

	// 传入非法语言代码，期望返回 ErrInvalidLocale 且不写库。
	_, err := svc.UpdateAppLocale(context.Background(), appOrgAdminPrincipal(store.organization), testAppServiceAppID, "fr")
	require.ErrorIs(t, err, ErrInvalidLocale, "不支持的语言应返回 ErrInvalidLocale")
	assert.Empty(t, store.jobs, "校验失败时不应入队 job")
}

// TestUpdateAppLocaleForbidden 验证无权用户被拒绝。
func TestUpdateAppLocaleForbidden(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	store.mustSeedApp(t)

	// 非本组织的管理员无权修改语言。
	outsider := auth.Principal{
		Role:   domain.UserRoleOrgAdmin,
		OrgID:  "00000000-0000-0000-0000-000000009999", // 不同组织
		UserID: testAdminUID,
	}
	_, err := svc.UpdateAppLocale(context.Background(), outsider, testAppServiceAppID, "en")
	require.ErrorIs(t, err, ErrForbidden, "非本组织管理员应返回 ErrForbidden")
}

// TestUpdateAppLocaleOwnerMemberAllowed 验证实例 owner（org_member）可修改自己实例的语言。
func TestUpdateAppLocaleOwnerMemberAllowed(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	store.mustSeedApp(t)

	// testMemUID 是 mustSeedApp 设置的 OwnerUserID。
	ownerMember := auth.Principal{
		Role:   domain.UserRoleOrgMember,
		OrgID:  testOrgID,
		UserID: testMemUID,
	}
	result, err := svc.UpdateAppLocale(context.Background(), ownerMember, testAppServiceAppID, "en")
	require.NoError(t, err)
	assert.Equal(t, "en", result.Locale, "owner 成员修改后 Locale 应为 en")
}

// TestSwitchAppVersionAppNotFound 验证实例不存在时返回 ErrNotFound。
func TestSwitchAppVersionAppNotFound(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	store.mustSeedApp(t)
	store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)

	const nonExistentAppID = "00000000-0000-0000-0000-000000009001"
	principal := appOrgAdminPrincipal(store.organization)
	// 传入不存在的实例 id，stub 返回 sql.ErrNoRows，期望 service 映射为 ErrNotFound。
	_, err := svc.SwitchAppVersion(context.Background(), principal, nonExistentAppID, testSwitchVersionID)
	require.ErrorIs(t, err, ErrNotFound, "实例不存在时应返回 ErrNotFound")
}
