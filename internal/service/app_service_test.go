package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
	app := store.mustSeedApp(t, "qwen2.5:7b")
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
			ID:      mustUUID(t, testOrgID),
			Name:    "测试组织",
			Status:  domain.StatusActive,
			ModelID: "qwen2.5:7b",
		},
		user: sqlc.User{
			ID:     mustUUID(t, testAdminUID),
			OrgID:  mustUUID(t, testOrgID),
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
		OrgID:  uuidToString(org.ID),
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

func (s *appServiceStoreStub) mustSeedApp(t *testing.T, modelID string) sqlc.App {
	t.Helper()
	s.app = sqlc.App{
		ID:            mustUUID(t, testAppServiceAppID),
		OrgID:         s.organization.ID,
		OwnerUserID:   mustUUID(t, testMemUID),
		RuntimeNodeID: mustUUID(t, "00000000-0000-0000-0000-000000002002"),
		Name:          "测试实例",
		Status:        domain.AppStatusRunning,
		PersonaMode:   domain.PersonaModeOrgInherited,
		ApiKeyStatus:  domain.APIKeyStatusActive,
		ModelID:       modelID,
	}
	return s.app
}

func (s *appServiceStoreStub) CreateApp(_ context.Context, arg sqlc.CreateAppParams) (sqlc.App, error) {
	s.app = sqlc.App{
		ID:            mustUUIDFromString(testAppServiceAppID),
		OrgID:         arg.OrgID,
		OwnerUserID:   arg.OwnerUserID,
		RuntimeNodeID: arg.RuntimeNodeID,
		Name:          arg.Name,
		Description:   arg.Description,
		Status:        arg.Status,
		PersonaMode:   arg.PersonaMode,
		AppPrompt:     arg.AppPrompt,
		ApiKeyStatus:  arg.ApiKeyStatus,
		ModelID:       arg.ModelID,
	}
	return s.app, nil
}

// GetAppWithVersion 返回 app 及版本 revision / image_id，模拟联查结果。
func (s *appServiceStoreStub) GetAppWithVersion(_ context.Context, id pgtype.UUID) (sqlc.GetAppWithVersionRow, error) {
	if s.app.ID != id {
		return sqlc.GetAppWithVersionRow{}, pgx.ErrNoRows
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

func (s *appServiceStoreStub) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error) {
	s.app.Status = arg.Status
	return s.app, nil
}

func (s *appServiceStoreStub) SoftDeleteApp(_ context.Context, _ pgtype.UUID) (sqlc.App, error) {
	s.app.DeletedAt = pgtype.Timestamptz{Valid: true}
	return s.app, nil
}

func (s *appServiceStoreStub) CreateJob(_ context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error) {
	if s.jobErr != nil {
		return sqlc.Job{}, s.jobErr
	}
	s.jobs = append(s.jobs, arg)
	return sqlc.Job{ID: mustUUIDFromString("00000000-0000-0000-0000-000000002101"), Type: arg.Type}, nil
}

func (s *appServiceStoreStub) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error) {
	if s.auditErr != nil {
		return sqlc.AuditLog{}, s.auditErr
	}
	s.auditLogs = append(s.auditLogs, arg)
	return sqlc.AuditLog{}, nil
}

// GetOrganization 返回 stub 预设的组织记录；id 不匹配时返回 pgx.ErrNoRows。
func (s *appServiceStoreStub) GetOrganization(_ context.Context, id pgtype.UUID) (sqlc.Organization, error) {
	if s.organization.ID != id {
		return sqlc.Organization{}, pgx.ErrNoRows
	}
	return s.organization, nil
}

// SetAppVersion 记录调用参数并将 app.VersionID 更新到 stub 状态，模拟数据库写入。
// 同时把 AppliedVersionRevision 清零、AppliedImageRef 置空，与真实 SQL 行为一致：
// 切换版本必然让实例进入需重启态，避免新旧版本 revision 相同导致 version_synced 误判。
func (s *appServiceStoreStub) SetAppVersion(_ context.Context, arg sqlc.SetAppVersionParams) (sqlc.App, error) {
	s.setVersionCalls = append(s.setVersionCalls, arg)
	s.app.VersionID = pgtype.UUID{Bytes: arg.VersionID.Bytes, Valid: true}
	s.app.AppliedVersionRevision = 0
	s.app.AppliedImageRef = ""
	return s.app, nil
}

func mustUUIDFromString(value string) pgtype.UUID {
	var id pgtype.UUID
	_ = id.Scan(value)
	return id
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
			// 覆盖 return ok && app.AppliedImageRef == ref 中 ok=false 的分支。
			name: "resolver 无法解析 image_id 时为 false",
			app: sqlc.App{
				AppliedVersionRevision: 4,
				AppliedImageRef:        "ghcr.io/foo/hermes:v1.0",
			},
			versionRevision: 4,
			versionImageID:  "img-unknown", // 不在 resolver.refs 中，ResolveRuntimeImage 返回 ok=false
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

	app := store.mustSeedApp(t, "qwen2.5:7b")
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

	// 变更场景：实例的 applied_version_revision 落后于版本最新 revision，
	// 期望 version_synced 通过 service 层传播为 false。
	store.versionRevision = 3 // 版本 revision 推进到 3，实例仍停在 2
	result2, err := svc.Get(context.Background(), platformAdmin(), testAppServiceAppID)
	require.NoError(t, err)
	assert.False(t, result2.VersionSynced, "实例 revision 落后于版本，version_synced 应为 false")
}

// mustOrgWithAllowlist 构造一个包含给定版本 id allowlist 的 Organization sqlc 记录。
// allowlist 以 JSON 数组形式写入 AssistantVersionIds 字段，模拟数据库存储格式。
func mustOrgWithAllowlist(t *testing.T, versionIDs ...string) sqlc.Organization {
	t.Helper()
	raw, err := json.Marshal(versionIDs)
	require.NoError(t, err, "序列化 allowlist 失败")
	return sqlc.Organization{
		ID:                  mustUUID(t, testOrgID),
		Name:                "测试组织",
		Status:              domain.StatusActive,
		ModelID:             "qwen2.5:7b",
		AssistantVersionIds: raw,
	}
}

// TestSwitchAppVersionSuccess 验证组织管理员切换到 allowlist 内的版本时成功返回更新后的实例视图，
// 且 version_synced 为 false（applied_* 仍指向旧版本，需重启生效）。
func TestSwitchAppVersionSuccess(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)

	// 预置实例：applied_version_revision=0，模拟切换前状态。
	app := store.mustSeedApp(t, "qwen2.5:7b")
	app.AppliedVersionRevision = 0
	store.app = app
	// 组织 allowlist 内含目标版本 testSwitchVersionID。
	store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)
	// stub 的 versionRevision 设为 1，使切换后 version_synced=false（applied 仍为 0）。
	store.versionRevision = 1

	principal := appOrgAdminPrincipal(store.organization)
	result, err := svc.SwitchAppVersion(context.Background(), principal, testAppServiceAppID, testSwitchVersionID)

	// 切换成功：无错误，返回的实例 VersionID 为目标版本。
	require.NoError(t, err)
	assert.Equal(t, testSwitchVersionID, result.VersionID, "返回的实例 VersionID 应等于目标版本")
	// applied_version_revision=0 而 versionRevision=1，version_synced 应为 false，提示需重启。
	assert.False(t, result.VersionSynced, "切换后 applied_* 未更新，version_synced 应为 false")
	// 验证 SetAppVersion 被实际调用一次，且参数正确。
	require.Len(t, store.setVersionCalls, 1, "SetAppVersion 应被调用一次")
}

// TestSwitchAppVersionSuccessByOwnerMember 验证组织成员作为实例 owner 时，
// 可通过 CanManageApp 的 owner-member 自服务路径成功切换版本。
func TestSwitchAppVersionSuccessByOwnerMember(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)

	// 预置实例：owner 为 testMemUID，模拟切换前状态（applied_version_revision=0）。
	app := store.mustSeedApp(t, "qwen2.5:7b")
	app.AppliedVersionRevision = 0
	store.app = app
	// 组织 allowlist 内含目标版本 testSwitchVersionID。
	store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)
	// stub versionRevision=1，确保切换后 version_synced=false（applied 仍为 0）。
	store.versionRevision = 1

	// 构造 owner-member principal：角色为 org_member，UserID 等于实例的 OwnerUserID（testMemUID）。
	ownerMember := auth.Principal{
		Role:   domain.UserRoleOrgMember,
		OrgID:  testOrgID,
		UserID: testMemUID, // 与 mustSeedApp 写入的 OwnerUserID 一致，满足 CanManageApp 自服务路径
	}

	result, err := svc.SwitchAppVersion(context.Background(), ownerMember, testAppServiceAppID, testSwitchVersionID)

	// owner-member 切换成功：无错误，返回的实例 VersionID 为目标版本。
	require.NoError(t, err)
	assert.Equal(t, testSwitchVersionID, result.VersionID, "返回的实例 VersionID 应等于目标版本")
	// applied_version_revision=0 而 versionRevision=1，version_synced 应为 false，提示需重启。
	assert.False(t, result.VersionSynced, "切换后 applied_* 未更新，version_synced 应为 false")
	// 验证 SetAppVersion 被实际调用一次，确认写入路径已执行。
	require.Len(t, store.setVersionCalls, 1, "SetAppVersion 应被调用一次")
}

// TestSwitchAppVersionResetsAppliedSoVersionSyncedIsFalse 是针对「同 revision 误判」bug 的回归测试。
// 复现场景：每个 assistant_versions 行各自维护独立的 revision 计数，旧版本 A 与目标版本 B
// 可能恰好都处于 revision=2 且镜像相同。修复前 SetAppVersion 切换时保留 applied_version_revision
// 与 applied_image_ref，computeVersionSynced 会判定 applied_version_revision(2)==B.revision(2)
// 且镜像 ref 相同 → version_synced=true，实例列表/详情不显示「需重启」，而实例实际仍在跑旧版本
// A 的 manifest。修复后 SetAppVersion 切换时清零 applied_*，切换后 version_synced 必为 false。
func TestSwitchAppVersionResetsAppliedSoVersionSyncedIsFalse(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)

	// 注入镜像解析器：目标版本的 image_id 解析为与旧版本完全相同的镜像 ref。
	svc.SetImageResolver(&stubImageResolver{refs: map[string]string{
		"img-v1": "ghcr.io/foo/hermes:v1.0",
	}})

	// 预置实例：模拟切换前已对齐旧版本 A —— applied_version_revision=2、applied_image_ref 与解析结果一致。
	app := store.mustSeedApp(t, "qwen2.5:7b")
	app.AppliedVersionRevision = 2
	app.AppliedImageRef = "ghcr.io/foo/hermes:v1.0"
	store.app = app
	// 组织 allowlist 内含目标版本 testSwitchVersionID。
	store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)
	// 关键碰撞条件：目标版本 B 的 revision 也是 2，image_id 解析出的 ref 与旧版本完全相同。
	store.versionRevision = 2
	store.versionImageID = "img-v1"

	principal := appOrgAdminPrincipal(store.organization)
	result, err := svc.SwitchAppVersion(context.Background(), principal, testAppServiceAppID, testSwitchVersionID)

	// 切换成功：无错误，返回的实例 VersionID 指向目标版本。
	require.NoError(t, err)
	assert.Equal(t, testSwitchVersionID, result.VersionID, "返回的实例 VersionID 应等于目标版本")
	// 回归断言：尽管新旧版本 revision 同为 2、镜像相同，切换后 applied_* 已被清零，
	// version_synced 必须为 false（修复前此处会误判为 true）。
	assert.False(t, result.VersionSynced, "新旧版本 revision 相同且镜像相同时，切换后 version_synced 仍应为 false")
	// 验证 SetAppVersion 被实际调用一次，确认走到了清零写入路径。
	require.Len(t, store.setVersionCalls, 1, "SetAppVersion 应被调用一次")
}

// TestSwitchAppVersionNotInAllowlist 验证目标版本不在组织 allowlist 内时返回 ErrVersionNotInAllowlist。
func TestSwitchAppVersionNotInAllowlist(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)

	// 预置实例，allowlist 只含 testSwitchVersionID，不含 testSwitchVersionID2。
	store.mustSeedApp(t, "qwen2.5:7b")
	store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)

	principal := appOrgAdminPrincipal(store.organization)
	// 尝试切换到 allowlist 外的 testSwitchVersionID2，期望返回 ErrVersionNotInAllowlist。
	_, err := svc.SwitchAppVersion(context.Background(), principal, testAppServiceAppID, testSwitchVersionID2)
	require.ErrorIs(t, err, ErrVersionNotInAllowlist, "allowlist 外的版本应返回 ErrVersionNotInAllowlist")
	// SetAppVersion 不应被调用。
	assert.Empty(t, store.setVersionCalls, "allowlist 校验失败时不应写入数据库")
}

// TestSwitchAppVersionForbidden 验证无权管理该实例的调用者被拒绝（返回 ErrForbidden）。
// 测试用例：组织成员尝试管理不属于自己的实例，或错误组织的管理员。
func TestSwitchAppVersionForbidden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// name 是子测试场景说明。
		name string
		// principal 是没有该实例管理权限的调用者。
		principal auth.Principal
	}{
		{
			// 平台管理员无应用写权限，CanManageApp 始终返回 false。
			name:      "平台管理员无应用写权限",
			principal: platformAdmin(),
		},
		{
			// 属于其他组织的组织管理员无权管理本组织的实例。
			name: "其他组织的管理员无权管理",
			principal: auth.Principal{
				Role:   domain.UserRoleOrgAdmin,
				OrgID:  "00000000-0000-0000-0000-000000009999", // 与实例所属组织不同
				UserID: testAdminUID,
			},
		},
		{
			// 组织成员只能管理自己的实例；testMemUID2 不是实例 owner（owner 为 testMemUID）。
			name: "非 owner 组织成员无权管理",
			principal: auth.Principal{
				Role:   domain.UserRoleOrgMember,
				OrgID:  testOrgID,
				UserID: "00000000-0000-0000-0000-000000009998", // 不是实例 owner
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, store := newAppServiceWithStore(t)
			store.mustSeedApp(t, "qwen2.5:7b")
			store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)

			// 无权调用者尝试切换版本，期望返回 ErrForbidden。
			_, err := svc.SwitchAppVersion(context.Background(), tc.principal, testAppServiceAppID, testSwitchVersionID)
			require.ErrorIs(t, err, ErrForbidden, "无权调用者应返回 ErrForbidden")
		})
	}
}

// TestSwitchAppVersionAppNotFound 验证实例不存在时返回 ErrNotFound。
func TestSwitchAppVersionAppNotFound(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	// store.app 未设置（零值 ID），传入有效但不存在的 appID 触发 pgx.ErrNoRows。
	store.mustSeedApp(t, "qwen2.5:7b")
	store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)

	const nonExistentAppID = "00000000-0000-0000-0000-000000009001"
	principal := appOrgAdminPrincipal(store.organization)
	// 传入不存在的实例 id，stub 返回 pgx.ErrNoRows，期望 service 映射为 ErrNotFound。
	_, err := svc.SwitchAppVersion(context.Background(), principal, nonExistentAppID, testSwitchVersionID)
	require.ErrorIs(t, err, ErrNotFound, "实例不存在时应返回 ErrNotFound")
}
