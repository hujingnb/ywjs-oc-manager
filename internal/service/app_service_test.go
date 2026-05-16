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

// TestUpdateModelRejectsOutsideAllowlist 验证实例模型必须属于组织 allowlist。
func TestUpdateModelRejectsOutsideAllowlist(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	store.organization.EnabledModels = []byte(`["qwen2.5:7b"]`)
	app := store.mustSeedApp(t, "qwen2.5:7b")

	_, err := svc.UpdateModel(context.Background(), appOrgAdminPrincipal(store.organization), uuidToString(app.ID), "deepseek-r1:14b")

	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	assert.Empty(t, store.jobs)
}

// TestUpdateModelCreatesRestartJobForContainer 验证有容器实例修改模型后提交重启任务。
func TestUpdateModelCreatesRestartJobForContainer(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	notifier := &fakeNotifier{}
	svc.SetJobNotifier(notifier)
	store.organization.EnabledModels = []byte(`["qwen2.5:7b","deepseek-r1:14b"]`)
	app := store.mustSeedApp(t, "qwen2.5:7b")
	app.ContainerID = pgtype.Text{String: "container-1", Valid: true}
	store.app = app

	result, err := svc.UpdateModel(context.Background(), appOrgAdminPrincipal(store.organization), uuidToString(app.ID), "deepseek-r1:14b")

	require.NoError(t, err)
	assert.Equal(t, "deepseek-r1:14b", result.App.ModelID)
	assert.True(t, result.RequiresRestart)
	assert.NotEmpty(t, result.RestartJobID)
	assert.Equal(t, result.RestartJobID, notifier.lastJobID)
	require.Len(t, store.jobs, 1)
	assert.Equal(t, domain.JobTypeAppRestartContainer, store.jobs[0].Type)
	require.Len(t, store.auditLogs, 1)
	assert.Equal(t, "update_model", store.auditLogs[0].Action)
}

// TestUpdateModelSurvivesNotifierError 验证 Redis 入队失败不回滚模型修改和重启任务。
func TestUpdateModelSurvivesNotifierError(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	svc.SetJobNotifier(&fakeNotifier{err: assert.AnError})
	store.organization.EnabledModels = []byte(`["qwen2.5:7b","deepseek-r1:14b"]`)
	app := store.mustSeedApp(t, "qwen2.5:7b")
	app.ContainerID = pgtype.Text{String: "container-1", Valid: true}
	store.app = app

	result, err := svc.UpdateModel(context.Background(), appOrgAdminPrincipal(store.organization), uuidToString(app.ID), "deepseek-r1:14b")

	require.NoError(t, err)
	assert.Equal(t, "deepseek-r1:14b", result.App.ModelID)
	assert.True(t, result.RequiresRestart)
	require.Len(t, store.jobs, 1)
}

// TestUpdateModelRollsBackWhenRestartJobFails 验证重启任务创建失败时回滚模型修改。
func TestUpdateModelRollsBackWhenRestartJobFails(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	store.jobErr = assert.AnError
	store.organization.EnabledModels = []byte(`["qwen2.5:7b","deepseek-r1:14b"]`)
	app := store.mustSeedApp(t, "qwen2.5:7b")
	app.ContainerID = pgtype.Text{String: "container-1", Valid: true}
	store.app = app

	_, err := svc.UpdateModel(context.Background(), appOrgAdminPrincipal(store.organization), uuidToString(app.ID), "deepseek-r1:14b")

	require.Error(t, err)
	assert.Equal(t, "qwen2.5:7b", store.app.ModelID)
	assert.Empty(t, store.jobs)
	assert.Empty(t, store.auditLogs)
}

// TestUpdateModelSameModelIsNoop 验证重复提交相同模型不会创建重启任务。
func TestUpdateModelSameModelIsNoop(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	store.organization.EnabledModels = []byte(`["qwen2.5:7b"]`)
	app := store.mustSeedApp(t, "qwen2.5:7b")
	app.ContainerID = pgtype.Text{String: "container-1", Valid: true}
	store.app = app

	result, err := svc.UpdateModel(context.Background(), appOrgAdminPrincipal(store.organization), uuidToString(app.ID), "qwen2.5:7b")

	require.NoError(t, err)
	assert.Equal(t, "qwen2.5:7b", result.App.ModelID)
	assert.False(t, result.RequiresRestart)
	assert.Empty(t, result.RestartJobID)
	assert.Empty(t, store.jobs)
	assert.Empty(t, store.auditLogs)
}

// TestUpdateModelRejectsDisabledPrincipal 验证已禁用用户不能修改实例模型。
func TestUpdateModelRejectsDisabledPrincipal(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	store.user.Status = domain.StatusDisabled
	store.organization.EnabledModels = []byte(`["qwen2.5:7b","deepseek-r1:14b"]`)
	app := store.mustSeedApp(t, "qwen2.5:7b")

	_, err := svc.UpdateModel(context.Background(), appOrgAdminPrincipal(store.organization), uuidToString(app.ID), "deepseek-r1:14b")

	require.ErrorIs(t, err, ErrForbidden)
	assert.Empty(t, store.jobs)
	assert.Equal(t, "qwen2.5:7b", store.app.ModelID)
}

// TestUpdateModelWithoutContainerOnlySavesModel 验证未创建容器的实例只保存模型不提交重启任务。
func TestUpdateModelWithoutContainerOnlySavesModel(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	store.organization.EnabledModels = []byte(`["qwen2.5:7b","deepseek-r1:14b"]`)
	app := store.mustSeedApp(t, "qwen2.5:7b")

	result, err := svc.UpdateModel(context.Background(), appOrgAdminPrincipal(store.organization), uuidToString(app.ID), "deepseek-r1:14b")

	require.NoError(t, err)
	assert.Equal(t, "deepseek-r1:14b", result.App.ModelID)
	assert.False(t, result.RequiresRestart)
	assert.Empty(t, result.RestartJobID)
	assert.Empty(t, store.jobs)
}

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
			ID:            mustUUID(t, testOrgID),
			Name:          "测试组织",
			Status:        domain.StatusActive,
			EnabledModels: []byte(`["qwen2.5:7b"]`),
		},
		user: sqlc.User{
			ID:     mustUUID(t, testAdminUID),
			OrgID:  mustUUID(t, testOrgID),
			Role:   domain.UserRoleOrgAdmin,
			Status: domain.StatusActive,
		},
	}
	svc := NewAppService(store)
	svc.SetTxRunner(store)
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
	jobs         []sqlc.CreateJobParams
	auditLogs    []sqlc.CreateAuditLogParams
	jobErr       error
	auditErr     error
}

func (s *appServiceStoreStub) WithAppTx(ctx context.Context, fn func(AppStore) error) error {
	snapshotApp := s.app
	snapshotJobs := append([]sqlc.CreateJobParams(nil), s.jobs...)
	snapshotAuditLogs := append([]sqlc.CreateAuditLogParams(nil), s.auditLogs...)
	if err := fn(s); err != nil {
		s.app = snapshotApp
		s.jobs = snapshotJobs
		s.auditLogs = snapshotAuditLogs
		return err
	}
	_ = ctx
	return nil
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

func (s *appServiceStoreStub) GetApp(_ context.Context, id pgtype.UUID) (sqlc.App, error) {
	if s.app.ID != id {
		return sqlc.App{}, pgx.ErrNoRows
	}
	return s.app, nil
}

func (s *appServiceStoreStub) GetUser(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
	if s.user.ID != id {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return s.user, nil
}

func (s *appServiceStoreStub) GetActiveAppByOwner(_ context.Context, ownerUserID pgtype.UUID) (sqlc.App, error) {
	if s.app.OwnerUserID == ownerUserID && !s.app.DeletedAt.Valid {
		return s.app, nil
	}
	return sqlc.App{}, pgx.ErrNoRows
}

func (s *appServiceStoreStub) ListAppsByOrg(_ context.Context, arg sqlc.ListAppsByOrgParams) ([]sqlc.App, error) {
	if s.app.OrgID == arg.OrgID && !s.app.DeletedAt.Valid {
		return []sqlc.App{s.app}, nil
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

func (s *appServiceStoreStub) GetOrganization(_ context.Context, id pgtype.UUID) (sqlc.Organization, error) {
	if s.organization.ID != id {
		return sqlc.Organization{}, pgx.ErrNoRows
	}
	return s.organization, nil
}

func (s *appServiceStoreStub) SetAppModel(_ context.Context, arg sqlc.SetAppModelParams) (sqlc.App, error) {
	if s.app.ID != arg.ID {
		return sqlc.App{}, pgx.ErrNoRows
	}
	s.app.ModelID = arg.ModelID
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
