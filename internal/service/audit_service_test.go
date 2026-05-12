// Package service 的 audit_service_test 覆盖审计事件记录的必填字段、上下文操作者和存储错误处理。
package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// TestAuditServiceRecordRequiresMandatoryFields 验证审计服务记录要求必填字段的预期行为场景。
func TestAuditServiceRecordRequiresMandatoryFields(t *testing.T) {
	store := &auditStoreStub{}
	svc := NewAuditService(store)

	_, err := svc.Record(context.Background(), AuditEvent{ActorRole: domain.UserRolePlatformAdmin})
	require.Error(t, err)
}

// TestAuditServiceRecordPersistsMetadata 验证审计服务记录持久化当前用户接口tadata的预期行为场景。
func TestAuditServiceRecordPersistsMetadata(t *testing.T) {
	store := &auditStoreStub{}
	svc := NewAuditService(store)

	result, err := svc.Record(context.Background(), AuditEvent{
		ActorRole:  domain.UserRolePlatformAdmin,
		TargetType: "organization",
		TargetID:   "00000000-0000-0000-0000-000000000101",
		Action:     "create",
		Result:     "succeeded",
		Metadata:   map[string]any{"name": "测试组织"},
	})
	require.NoError(t, err)
	if result.TargetType != "organization" || store.created.TargetType != "organization" {
		t.Fatalf("expected target persisted, got %+v", store.created)
	}
	require.NotEqual(t, 0, len(store.created.MetadataJson))
}

// TestAuditServiceListByOrgRequiresAccess 验证审计服务列表通过组织要求Access的预期行为场景。
func TestAuditServiceListByOrgRequiresAccess(t *testing.T) {
	svc := NewAuditService(&auditStoreStub{})

	_, err := svc.ListByOrg(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "00000000-0000-0000-0000-000000000aaa"}, "00000000-0000-0000-0000-000000000bbb", 0, 0)
	require.ErrorIs(t, err, ErrForbidden)
}

// TestAuditServiceListByOrgClampsLimit 验证审计服务列表通过组织限制Limit的边界条件场景。
func TestAuditServiceListByOrgClampsLimit(t *testing.T) {
	store := &auditStoreStub{}
	svc := NewAuditService(store)

	_, err := svc.ListByOrg(context.Background(), platformAdmin(), testOrgID, 5000, 0)
	require.NoError(t, err)
	require.Equal(t, int32(200), store.lastByOrg.Limit)
}

// TestAuditServiceListByTargetFiltersOrgScope 验证审计服务列表通过目标Filters组织scope的预期行为场景。
func TestAuditServiceListByTargetFiltersOrgScope(t *testing.T) {
	store := &auditStoreStub{
		apps: map[string]sqlc.App{
			testAuditAppID: {
				ID:          mustUUID(t, testAuditAppID),
				OrgID:       mustUUID(t, testOrgID),
				OwnerUserID: mustUUID(t, testMemUID),
			},
		},
		byTarget: []sqlc.AuditLog{
			{TargetType: "app", TargetID: testAuditAppID, OrgID: mustOptionalUUID(t, testOrgID)},
			{TargetType: "app", TargetID: testAuditAppID, OrgID: mustOptionalUUID(t, testOrg2ID)},
		},
	}
	svc := NewAuditService(store)

	results, err := svc.ListByTarget(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testOrgID}, "app", testAuditAppID, 0, 0)
	require.NoError(t, err)
	if len(results) != 1 || results[0].OrgID != testOrgID {
		t.Fatalf("results = %+v, want only own org", results)
	}
}

// TestAuditServiceListByTargetAllowsMemberOwnApp 验证审计服务列表通过目标允许成员本人应用的预期行为场景。
func TestAuditServiceListByTargetAllowsMemberOwnApp(t *testing.T) {
	store := &auditStoreStub{
		apps: map[string]sqlc.App{
			testAuditAppID: {
				ID:          mustUUID(t, testAuditAppID),
				OrgID:       mustUUID(t, testOrgID),
				OwnerUserID: mustUUID(t, testMemUID),
			},
		},
		byTarget: []sqlc.AuditLog{
			{TargetType: "app", TargetID: testAuditAppID, OrgID: mustOptionalUUID(t, testOrgID)},
		},
	}
	svc := NewAuditService(store)

	results, err := svc.ListByTarget(context.Background(), auth.Principal{
		UserID: testMemUID,
		OrgID:  testOrgID,
		Role:   domain.UserRoleOrgMember,
	}, "app", testAuditAppID, 0, 0)

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testAuditAppID, results[0].TargetID)
}

// TestAuditServiceListByTargetRejectsMemberOtherApp 验证审计服务列表通过目标拒绝成员其他应用的异常或拒绝路径场景。
func TestAuditServiceListByTargetRejectsMemberOtherApp(t *testing.T) {
	store := &auditStoreStub{
		apps: map[string]sqlc.App{
			testAuditAppID: {
				ID:          mustUUID(t, testAuditAppID),
				OrgID:       mustUUID(t, testOrgID),
				OwnerUserID: mustUUID(t, testAdminUID),
			},
		},
	}
	svc := NewAuditService(store)

	_, err := svc.ListByTarget(context.Background(), auth.Principal{
		UserID: testMemUID,
		OrgID:  testOrgID,
		Role:   domain.UserRoleOrgMember,
	}, "app", testAuditAppID, 0, 0)

	require.ErrorIs(t, err, ErrForbidden)
}

func mustOptionalUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	id := mustUUID(t, value)
	id.Valid = true
	return id
}

const testAuditAppID = "00000000-0000-0000-0000-0000000000c1"

type auditStoreStub struct {
	created   sqlc.CreateAuditLogParams
	byOrg     []sqlc.AuditLog
	byTarget  []sqlc.AuditLog
	lastByOrg sqlc.ListAuditLogsByOrgParams
	apps      map[string]sqlc.App
}

func (s *auditStoreStub) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error) {
	s.created = arg
	return sqlc.AuditLog{
		ActorRole:    arg.ActorRole,
		OrgID:        arg.OrgID,
		TargetType:   arg.TargetType,
		TargetID:     arg.TargetID,
		Action:       arg.Action,
		Result:       arg.Result,
		MetadataJson: arg.MetadataJson,
	}, nil
}

func (s *auditStoreStub) ListAuditLogsByOrg(_ context.Context, arg sqlc.ListAuditLogsByOrgParams) ([]sqlc.AuditLog, error) {
	s.lastByOrg = arg
	return s.byOrg, nil
}

func (s *auditStoreStub) ListAuditLogsByTarget(_ context.Context, _ sqlc.ListAuditLogsByTargetParams) ([]sqlc.AuditLog, error) {
	return s.byTarget, nil
}

func (s *auditStoreStub) GetApp(_ context.Context, id pgtype.UUID) (sqlc.App, error) {
	app, ok := s.apps[uuidToString(id)]
	if !ok {
		return sqlc.App{}, pgx.ErrNoRows
	}
	return app, nil
}
