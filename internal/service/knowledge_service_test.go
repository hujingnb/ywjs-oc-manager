package service

import (
	"context"
	"strings"
	"testing"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/files"
	"github.com/stretchr/testify/require"
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

	err := svc.SaveOrgFile(context.Background(), platformAdmin(), testKnowledgeOrg, "doc.md", strings.NewReader("hello"), 5)
	require.NoError(t, err)
	listing, err := svc.ListOrg(context.Background(), platformAdmin(), testKnowledgeOrg, "")
	require.NoError(t, err)
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "doc.md" {
		t.Fatalf("listing = %+v", listing)
	}
}

func TestKnowledgeServiceSaveAppRespectsOwnership(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveAppFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "stranger"}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "doc.md", strings.NewReader("hi"), 2)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)

	err = svc.SaveAppFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "doc.md", strings.NewReader("hi"), 2)
	require.NoError(t, err)
}

func TestKnowledgeServiceListAppRequiresAccess(t *testing.T) {
	svc := newKnowledgeService(t)

	_, err := svc.ListApp(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "stranger"}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "")
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

func TestKnowledgeServiceDeleteOrgRequiresManager(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.DeleteOrgFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "x"}, testKnowledgeOrg, "doc.md")
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

func newKnowledgeService(t *testing.T) *KnowledgeService {
	t.Helper()
	root, err := files.NewSafeRoot(t.TempDir(), 1024)
	require.NoError(t, err)
	return NewKnowledgeService(files.NewKnowledgeMaster(root))
}
