package service

import (
	"context"
	"errors"
	"strings"
	"testing"

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
	if !errors.Is(err, ErrKnowledgeForbidden) {
		t.Fatalf("error = %v, want ErrKnowledgeForbidden", err)
	}
}

func TestKnowledgeServiceSaveOrgWritesFile(t *testing.T) {
	svc := newKnowledgeService(t)

	if err := svc.SaveOrgFile(context.Background(), platformAdmin(), testKnowledgeOrg, "doc.md", strings.NewReader("hello"), 5); err != nil {
		t.Fatalf("SaveOrgFile() error = %v", err)
	}
	listing, err := svc.ListOrg(context.Background(), platformAdmin(), testKnowledgeOrg, "")
	if err != nil {
		t.Fatalf("ListOrg() error = %v", err)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "doc.md" {
		t.Fatalf("listing = %+v", listing)
	}
}

func TestKnowledgeServiceSaveAppRespectsOwnership(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveAppFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "stranger"}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "doc.md", strings.NewReader("hi"), 2)
	if !errors.Is(err, ErrKnowledgeForbidden) {
		t.Fatalf("error = %v, want ErrKnowledgeForbidden", err)
	}

	if err := svc.SaveAppFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "doc.md", strings.NewReader("hi"), 2); err != nil {
		t.Fatalf("SaveAppFile() error = %v", err)
	}
}

func TestKnowledgeServiceListAppRequiresAccess(t *testing.T) {
	svc := newKnowledgeService(t)

	_, err := svc.ListApp(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "stranger"}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "")
	if !errors.Is(err, ErrKnowledgeForbidden) {
		t.Fatalf("error = %v, want ErrKnowledgeForbidden", err)
	}
}

func TestKnowledgeServiceDeleteOrgRequiresManager(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.DeleteOrgFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "x"}, testKnowledgeOrg, "doc.md")
	if !errors.Is(err, ErrKnowledgeForbidden) {
		t.Fatalf("error = %v, want ErrKnowledgeForbidden", err)
	}
}

func newKnowledgeService(t *testing.T) *KnowledgeService {
	t.Helper()
	root, err := files.NewSafeRoot(t.TempDir(), 1024)
	if err != nil {
		t.Fatalf("NewSafeRoot() error = %v", err)
	}
	return NewKnowledgeService(files.NewKnowledgeMaster(root))
}
