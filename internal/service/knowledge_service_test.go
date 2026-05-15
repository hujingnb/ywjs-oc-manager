// Package service 的 knowledge_service_test 覆盖组织和应用知识库服务的权限、路径和同步状态逻辑。
package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/files"
)

const (
	testKnowledgeOrg   = "00000000-0000-0000-0000-000000000e01"
	testKnowledgeOrg2  = "00000000-0000-0000-0000-000000000e02"
	testKnowledgeApp   = "00000000-0000-0000-0000-000000000e03"
	testKnowledgeOwner = "00000000-0000-0000-0000-000000000e04"
)

// TestKnowledgeServiceSaveOrgRequiresOrgManager 验证知识库服务保存组织要求组织Manager的预期行为场景。
func TestKnowledgeServiceSaveOrgRequiresOrgManager(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveOrgFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testKnowledgeOrg2}, testKnowledgeOrg, "doc.md", strings.NewReader("hi"), 2)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestKnowledgeServiceSaveOrgWritesFile 验证知识库服务保存组织写入文件的成功路径场景。
func TestKnowledgeServiceSaveOrgWritesFile(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "doc.md", strings.NewReader("hello"), 5)
	require.NoError(t, err)
	listing, err := svc.ListOrg(context.Background(), platformAdmin(), testKnowledgeOrg, "")
	require.NoError(t, err)
	require.Len(t, listing.Entries, 1)
	assert.Equal(t, "doc.md", listing.Entries[0].Name)
}

// TestKnowledgeServiceSaveAppRespectsOwnership 验证知识库服务保存应用遵守Ownership的预期行为场景。
func TestKnowledgeServiceSaveAppRespectsOwnership(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveAppFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "stranger"}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "doc.md", strings.NewReader("hi"), 2)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)

	err = svc.SaveAppFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "doc.md", strings.NewReader("hi"), 2)
	require.NoError(t, err)
}

// TestKnowledgeServicePlatformAdminCannotWriteOrgOrAppKnowledge 验证知识库服务平台管理员Cannot写入组织或应用知识库的预期行为场景。
func TestKnowledgeServicePlatformAdminCannotWriteOrgOrAppKnowledge(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveOrgFile(context.Background(), platformAdmin(), testKnowledgeOrg, "doc.md", strings.NewReader("hi"), 2)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)

	err = svc.SaveAppFile(context.Background(), platformAdmin(), testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "doc.md", strings.NewReader("hi"), 2)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestKnowledgeServiceListAppRequiresAccess 验证知识库服务列表应用要求Access的预期行为场景。
func TestKnowledgeServiceListAppRequiresAccess(t *testing.T) {
	svc := newKnowledgeService(t)

	_, err := svc.ListApp(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "stranger"}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "")
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestKnowledgeServicePlatformAdminCanReadOrgAndAppKnowledge 验证知识库服务平台管理员读取权限组织并应用知识库的预期行为场景。
func TestKnowledgeServicePlatformAdminCanReadOrgAndAppKnowledge(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "doc.md", strings.NewReader("hello"), 5)
	require.NoError(t, err)
	err = svc.SaveAppFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "app.md", strings.NewReader("hello"), 5)
	require.NoError(t, err)

	orgListing, err := svc.ListOrg(context.Background(), platformAdmin(), testKnowledgeOrg, "")
	require.NoError(t, err)
	require.Len(t, orgListing.Entries, 1)
	assert.Equal(t, "doc.md", orgListing.Entries[0].Name)

	appListing, err := svc.ListApp(context.Background(), platformAdmin(), testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "")
	require.NoError(t, err)
	require.Len(t, appListing.Entries, 1)
	assert.Equal(t, "app.md", appListing.Entries[0].Name)
}

// TestKnowledgeServiceDeleteOrgRequiresManager 验证知识库服务删除组织要求Manager的预期行为场景。
func TestKnowledgeServiceDeleteOrgRequiresManager(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.DeleteOrgFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "x"}, testKnowledgeOrg, "doc.md")
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestKnowledgeServicePlatformAdminCannotDeleteOrgOrRetrySync 验证知识库服务平台管理员Cannot删除组织或重试同步的预期行为场景。
func TestKnowledgeServicePlatformAdminCannotDeleteOrgOrRetrySync(t *testing.T) {
	svc := newKnowledgeService(t)
	svc.SetRetryDispatcher(knowledgeRetryDispatcherStub{})

	err := svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "doc.md", strings.NewReader("hello"), 5)
	require.NoError(t, err)

	err = svc.DeleteOrgFile(context.Background(), platformAdmin(), testKnowledgeOrg, "doc.md")
	require.ErrorIs(t, err, ErrKnowledgeForbidden)

	err = svc.RetryOrgNodeSync(context.Background(), platformAdmin(), testKnowledgeOrg, "node-1")
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestKnowledgeServicePlatformAdminCannotGetOrgSyncStatus 验证知识库服务平台管理员Cannot获取组织同步状态的预期行为场景。
func TestKnowledgeServicePlatformAdminCannotGetOrgSyncStatus(t *testing.T) {
	svc := newKnowledgeService(t)
	svc.SetSyncStatusSource(knowledgeStatusSourceStub{})

	_, err := svc.GetOrgSyncStatus(context.Background(), platformAdmin(), testKnowledgeOrg)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestKnowledgeServiceOrgAdminCanGetOrgSyncStatus 验证知识库服务组织管理员权限判断获取组织同步状态的预期行为场景。
func TestKnowledgeServiceOrgAdminCanGetOrgSyncStatus(t *testing.T) {
	svc := newKnowledgeService(t)
	svc.SetSyncStatusSource(knowledgeStatusSourceStub{})

	_, err := svc.GetOrgSyncStatus(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg)
	require.NoError(t, err)
}

// TestKnowledgeServiceSaveOrgRecordsDispatchFailure 验证主副本写入成功但 dispatcher
// 入队失败时:1) 主流程不报错,接口仍返回成功;2) audit_logs 留下 failed 记录,
// 让运维能从审计页面发现"知识库已落主副本但未通知节点"的中间态。
func TestKnowledgeServiceSaveOrgRecordsDispatchFailure(t *testing.T) {
	svc := newKnowledgeService(t)
	auditor := &fakeKnowledgeAuditor{}
	svc.SetAuditor(auditor)
	svc.SetSyncDispatcher(failingDispatcher{err: errors.New("redis down")})

	// 主副本仍然写成功,接口对用户返回 nil:服务层契约保留"主副本是事实来源,同步是异步事项"。
	err := svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "doc.md", strings.NewReader("hello"), 5)
	require.NoError(t, err)

	// audit 留下一条 result=failed、action=dispatch_org_upload_file 的记录,排障可循。
	require.Len(t, auditor.events, 1)
	ev := auditor.events[0]
	assert.Equal(t, "knowledge_sync", ev.TargetType)
	assert.Equal(t, "failed", ev.Result)
	assert.Equal(t, "dispatch_org_upload_file", ev.Action)
	assert.Equal(t, testKnowledgeOrg, ev.OrgID)
	assert.Contains(t, ev.ErrorMessage, "redis down")
}

// TestKnowledgeServiceDeleteAppRecordsDispatchFailure 覆盖应用级删除走相同
// 审计路径:确保不同 scope(app)+ change_type(delete)组合都不被静默吞掉。
func TestKnowledgeServiceDeleteAppRecordsDispatchFailure(t *testing.T) {
	svc := newKnowledgeService(t)
	auditor := &fakeKnowledgeAuditor{}
	svc.SetAuditor(auditor)
	svc.SetSyncDispatcher(failingDispatcher{err: errors.New("queue full")})

	// 先用 owner 身份写入一条主副本,确保 Delete 不因为目标缺失而提前返回。
	owner := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}
	require.NoError(t, svc.SaveAppFile(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "a.md", strings.NewReader("x"), 1))
	auditor.events = nil // 清掉 Save 那次入队失败留下的痕迹,只断言 Delete 那次。

	err := svc.DeleteAppFile(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "a.md")
	require.NoError(t, err)

	require.Len(t, auditor.events, 1)
	ev := auditor.events[0]
	assert.Equal(t, "dispatch_app_delete_file", ev.Action)
	assert.Equal(t, "failed", ev.Result)
	assert.Equal(t, testKnowledgeApp, ev.TargetID) // app scope 时 target_id 用 app_id,方便按应用筛选。
}

// failingDispatcher 给所有 Dispatch* 方法返回固定 err,用于触发 audit 路径。
type failingDispatcher struct{ err error }

func (f failingDispatcher) DispatchOrgChange(_ context.Context, _, _, _, _ string) error {
	return f.err
}

func (f failingDispatcher) DispatchAppChange(_ context.Context, _, _, _, _, _ string) error {
	return f.err
}

// fakeKnowledgeAuditor 收集 service 投递过来的 AuditEvent,供单测断言。
type fakeKnowledgeAuditor struct {
	events []AuditEvent
}

func (f *fakeKnowledgeAuditor) Record(_ context.Context, event AuditEvent) (AuditResult, error) {
	f.events = append(f.events, event)
	return AuditResult{}, nil
}

func newKnowledgeService(t *testing.T) *KnowledgeService {
	t.Helper()
	root, err := files.NewSafeRoot(t.TempDir(), 1024)
	require.NoError(t, err)
	return NewKnowledgeService(files.NewKnowledgeMaster(root))
}

func orgKnowledgeAdmin() auth.Principal {
	return auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testKnowledgeOrg, UserID: "00000000-0000-0000-0000-000000000e05"}
}

type knowledgeRetryDispatcherStub struct{}

func (knowledgeRetryDispatcherStub) RetryOrgNode(_ context.Context, _, _ string) error {
	return nil
}

type knowledgeStatusSourceStub struct{}

func (knowledgeStatusSourceStub) ListByOrg(_ context.Context, _ string) ([]SyncStatusResult, error) {
	return []SyncStatusResult{}, nil
}
