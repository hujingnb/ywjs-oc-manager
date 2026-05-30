package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

const (
	testAppServiceAppID = "00000000-0000-0000-0000-000000002001"
	// testSwitchVersionID 是用于 SwitchAppVersion 测试的目标助手版本 id。
	testSwitchVersionID = "00000000-0000-0000-0000-000000003001"
	// testSwitchVersionID2 是第二个助手版本 id，不在组织 allowlist 内，用于拒绝场景测试。
	testSwitchVersionID2 = "00000000-0000-0000-0000-000000003002"
)

// TestGetAppExposeRuntimeImageOnlyToPlatformAdmin 验证 RuntimeImageRef 和 RuntimeImageSha256
// 仅在平台管理员调用 Get 时返回；组织管理员调用时两字段应为空。
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

type appServiceStoreStub struct {
	organization sqlc.Organization
	user         sqlc.User
	app          sqlc.App
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
	}
	return nil
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
