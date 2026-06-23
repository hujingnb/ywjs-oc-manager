// Package service 的 audit_service_test 覆盖审计事件记录的必填字段、上下文操作者和存储错误处理。
package service

import (
	"context"
	"database/sql"
	"testing"

	null "github.com/guregu/null/v5"

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
		byTarget: []sqlc.ListAuditLogsByTargetRow{
			{TargetType: "app", TargetID: testAuditAppID, OrgID: mustOptionalUUID(t, testOrgID)},  // 场景：目标应用所属组织内的审计记录应允许返回。
			{TargetType: "app", TargetID: testAuditAppID, OrgID: mustOptionalUUID(t, testOrg2ID)}, // 场景：跨组织同目标审计记录用于验证组织范围过滤。
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
		byTarget: []sqlc.ListAuditLogsByTargetRow{
			{TargetType: "app", TargetID: testAuditAppID, OrgID: mustOptionalUUID(t, testOrgID)}, // 场景：成员查看自己应用审计时返回同组织目标记录。
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

// TestAuditServiceListByOrgPopulatesNameColumns 验证审计列表查询返回 actor / target 名称、软删除标记和详情字符串的预期行为场景。
func TestAuditServiceListByOrgPopulatesNameColumns(t *testing.T) {
	// 场景：actor / target 名称、软删除标记、详情字符串均被透传到 AuditResult。
	// ActorDeleted / TargetDeleted 是 interface{}，MySQL 返回 int64（0/1）；
	// 此处用 bool 直接测试 ifaceToBool 的 bool 分支。
	store := &auditStoreStub{
		byOrg: []sqlc.ListAuditLogsByOrgRow{
			{
				TargetType:    "app",
				TargetID:      testAuditAppID,
				OrgID:         mustOptionalUUID(t, testOrgID),
				ActorRole:     domain.UserRoleOrgAdmin,
				DetailMessage: null.StringFrom("gpt-4o → claude-opus-4-7"),
				ActorName:     "张三",
				ActorDeleted:  false,
				TargetName:    "客服小助手",
				TargetDeleted: true,
			},
		},
	}
	svc := NewAuditService(store)

	results, err := svc.ListByOrg(context.Background(), platformAdmin(), testOrgID, 0, 0)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "张三", results[0].ActorName)
	require.False(t, results[0].ActorDeleted)
	require.Equal(t, "客服小助手", results[0].TargetName)
	require.True(t, results[0].TargetDeleted)
	require.Equal(t, "gpt-4o → claude-opus-4-7", results[0].ActionDetail)
}

// TestAuditServiceRecordPersistsDetailMessage 验证 Record 在 DetailMessage 非空时仍会透传到 CreateAuditLog，
// 以兼容少数仍传旧字段的历史代码路径；新代码不应再使用 DetailMessage，改用 Metadata。
func TestAuditServiceRecordPersistsDetailMessage(t *testing.T) {
	// 场景：少数旧路径仍填写 DetailMessage 时，落库参数应携带相同字符串（历史兼容）。
	store := &auditStoreStub{}
	svc := NewAuditService(store)

	_, err := svc.Record(context.Background(), AuditEvent{
		ActorRole:     domain.UserRolePlatformAdmin,
		TargetType:    "organization",
		TargetID:      "00000000-0000-0000-0000-000000000101",
		Action:        "recharge",
		Result:        "succeeded",
		DetailMessage: "+5000.00 元，备注 vip 续费",
	})
	require.NoError(t, err)
	// 历史兼容：DetailMessage 不为空时应仍透传到 detail_message 列。
	require.True(t, store.created.DetailMessage.Valid)
	require.Equal(t, "+5000.00 元，备注 vip 续费", store.created.DetailMessage.String)
}

// TestAuditServiceRecordNoDetailMessageNewPaths 验证迁移后新写入路径不再设置 detail_message，
// 而是通过 Metadata 传递结构化参数。
func TestAuditServiceRecordNoDetailMessageNewPaths(t *testing.T) {
	// 场景：新写入路径（如充值、skill 操作）不再填 DetailMessage，
	// 改用 Metadata 存储动态参数，确保历史文案不冻结到数据库。
	store := &auditStoreStub{}
	svc := NewAuditService(store)

	_, err := svc.Record(context.Background(), AuditEvent{
		ActorRole:  domain.UserRolePlatformAdmin,
		TargetType: "organization",
		TargetID:   "00000000-0000-0000-0000-000000000101",
		Action:     "recharge",
		Result:     "succeeded",
		// 新路径：仅传 Metadata，不传 DetailMessage。
		Metadata: map[string]any{
			"amount": 5000,
			"remark": "vip 续费",
		},
	})
	require.NoError(t, err)
	// 新记录不应写入冻结文案。
	require.False(t, store.created.DetailMessage.Valid, "新写入路径不应设置 detail_message")
	// Metadata 应序列化写入 metadata_json。
	require.NotEmpty(t, store.created.MetadataJson, "Metadata 应写入 metadata_json")
}

// mustOptionalUUID 返回 null.String 表示一个有效的 UUID 值（MySQL 侧 CHAR(36) 可空列）。
func mustOptionalUUID(t *testing.T, value string) null.String {
	t.Helper()
	return null.StringFrom(value)
}

const testAuditAppID = "00000000-0000-0000-0000-0000000000c1"

type auditStoreStub struct {
	created   sqlc.CreateAuditLogParams
	byOrg     []sqlc.ListAuditLogsByOrgRow
	byTarget  []sqlc.ListAuditLogsByTargetRow
	lastByOrg sqlc.ListAuditLogsByOrgParams
	apps      map[string]sqlc.App
	// lastLog 存储最近一次写入的审计日志行，供 GetAuditLog 读回。
	lastLog sqlc.AuditLog
}

// CreateAuditLog 为 :exec；stub 记录参数供测试断言，同时构建完整行供 GetAuditLog 读回。
func (s *auditStoreStub) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) error {
	s.created = arg
	s.lastLog = sqlc.AuditLog{
		ID:            arg.ID,
		ActorID:       arg.ActorID,
		ActorRole:     arg.ActorRole,
		OrgID:         arg.OrgID,
		TargetType:    arg.TargetType,
		TargetID:      arg.TargetID,
		Action:        arg.Action,
		Result:        arg.Result,
		MetadataJson:  arg.MetadataJson,
		DetailMessage: arg.DetailMessage,
	}
	return nil
}

func (s *auditStoreStub) ListAuditLogsByOrg(_ context.Context, arg sqlc.ListAuditLogsByOrgParams) ([]sqlc.ListAuditLogsByOrgRow, error) {
	s.lastByOrg = arg
	return s.byOrg, nil
}

func (s *auditStoreStub) ListAuditLogsByTarget(_ context.Context, _ sqlc.ListAuditLogsByTargetParams) ([]sqlc.ListAuditLogsByTargetRow, error) {
	return s.byTarget, nil
}

func (s *auditStoreStub) GetApp(_ context.Context, id string) (sqlc.App, error) {
	app, ok := s.apps[id]
	if !ok {
		return sqlc.App{}, sql.ErrNoRows
	}
	return app, nil
}

func (s *auditStoreStub) GetAuditLog(_ context.Context, id string) (sqlc.AuditLog, error) {
	if s.lastLog.ID == id {
		return s.lastLog, nil
	}
	return sqlc.AuditLog{}, sql.ErrNoRows
}
