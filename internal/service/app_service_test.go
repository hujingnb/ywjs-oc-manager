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
	jobs         []sqlc.CreateJobParams
	auditLogs    []sqlc.CreateAuditLogParams
	jobErr       error
	auditErr     error
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
