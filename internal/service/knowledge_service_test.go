// Package service 的 knowledge_service_test 覆盖组织和应用知识库服务的权限、路径和同步状态逻辑。
package service

import (
	"context"
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

func TestKnowledgeServiceSaveOrgRequiresOrgManager(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveOrgFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testKnowledgeOrg2}, testKnowledgeOrg, "doc.md", strings.NewReader("hi"), 2)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

func TestKnowledgeServiceSaveOrgWritesFile(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "doc.md", strings.NewReader("hello"), 5)
	require.NoError(t, err)
	listing, err := svc.ListOrg(context.Background(), platformAdmin(), testKnowledgeOrg, "")
	require.NoError(t, err)
	require.Len(t, listing.Entries, 1)
	assert.Equal(t, "doc.md", listing.Entries[0].Name)
}

func TestKnowledgeServiceSaveAppRespectsOwnership(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveAppFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "stranger"}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "doc.md", strings.NewReader("hi"), 2)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)

	err = svc.SaveAppFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "doc.md", strings.NewReader("hi"), 2)
	require.NoError(t, err)
}

func TestKnowledgeServicePlatformAdminCannotWriteOrgOrAppKnowledge(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveOrgFile(context.Background(), platformAdmin(), testKnowledgeOrg, "doc.md", strings.NewReader("hi"), 2)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)

	err = svc.SaveAppFile(context.Background(), platformAdmin(), testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "doc.md", strings.NewReader("hi"), 2)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

func TestKnowledgeServiceListAppRequiresAccess(t *testing.T) {
	svc := newKnowledgeService(t)

	_, err := svc.ListApp(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "stranger"}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "")
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

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

func TestKnowledgeServiceDeleteOrgRequiresManager(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.DeleteOrgFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "x"}, testKnowledgeOrg, "doc.md")
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

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

func TestKnowledgeServicePlatformAdminCannotGetOrgSyncStatus(t *testing.T) {
	svc := newKnowledgeService(t)
	svc.SetSyncStatusSource(knowledgeStatusSourceStub{})

	_, err := svc.GetOrgSyncStatus(context.Background(), platformAdmin(), testKnowledgeOrg)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

func TestKnowledgeServiceOrgAdminCanGetOrgSyncStatus(t *testing.T) {
	svc := newKnowledgeService(t)
	svc.SetSyncStatusSource(knowledgeStatusSourceStub{})

	_, err := svc.GetOrgSyncStatus(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg)
	require.NoError(t, err)
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
