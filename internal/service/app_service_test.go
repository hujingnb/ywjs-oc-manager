package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

const testAppServiceAppID = "00000000-0000-0000-0000-000000002001"

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
