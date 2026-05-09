package auth

import (
	"testing"

	"oc-manager/internal/domain"
)

const (
	orgA  = "org-A"
	orgB  = "org-B"
	userA = "user-A"
	userB = "user-B"
)

type orgCase struct {
	name      string
	role      string
	pOrgID    string
	targetOrg string
	want      bool
}

func runOrgCases(t *testing.T, fn func(Principal, string) bool, cases []orgCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Principal{UserID: userA, OrgID: c.pOrgID, Role: c.role}
			if got := fn(p, c.targetOrg); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestCanManageOrg(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可管", domain.UserRolePlatformAdmin, orgA, orgB, true},
		{"org_admin 同组织可管", domain.UserRoleOrgAdmin, orgA, orgA, true},
		{"org_admin 跨组织不可管", domain.UserRoleOrgAdmin, orgA, orgB, false},
		{"org_member 同组织也不可管", domain.UserRoleOrgMember, orgA, orgA, false},
		{"未知角色不可管", "unknown", orgA, orgA, false},
	}
	runOrgCases(t, CanManageOrg, cases)
}

func TestCanViewOrg(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可读", domain.UserRolePlatformAdmin, orgA, orgB, true},
		{"org_admin 同组织可读", domain.UserRoleOrgAdmin, orgA, orgA, true},
		{"org_admin 跨组织不可读", domain.UserRoleOrgAdmin, orgA, orgB, false},
		{"org_member 同组织可读", domain.UserRoleOrgMember, orgA, orgA, true},
		{"org_member 跨组织不可读", domain.UserRoleOrgMember, orgA, orgB, false},
	}
	runOrgCases(t, CanViewOrg, cases)
}

type memberCase struct {
	name       string
	role       string
	pOrgID     string
	pUserID    string
	targetOrg  string
	targetUser string
	want       bool
}

func TestCanViewMember(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 任意成员可看", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true},
		{"org_admin 同组织可看", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},
		{"org_admin 跨组织不可看", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},
		{"org_member 仅看自己", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},
		{"org_member 不可看他人", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Principal{UserID: c.pUserID, OrgID: c.pOrgID, Role: c.role}
			if got := CanViewMember(p, c.targetOrg, c.targetUser); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestCanManageMember(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可管成员", domain.UserRolePlatformAdmin, orgA, orgB, true},
		{"org_admin 同组织可管成员", domain.UserRoleOrgAdmin, orgA, orgA, true},
		{"org_admin 跨组织不可管", domain.UserRoleOrgAdmin, orgA, orgB, false},
		{"org_member 一律不可管", domain.UserRoleOrgMember, orgA, orgA, false},
	}
	runOrgCases(t, CanManageMember, cases)
}

func TestCanEditMember(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 任意可编辑", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true},
		{"org_admin 同组织可编辑", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},
		{"org_admin 跨组织不可编辑", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},
		{"org_member 仅可编辑自己", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},
		{"org_member 不可编辑他人", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Principal{UserID: c.pUserID, OrgID: c.pOrgID, Role: c.role}
			if got := CanEditMember(p, c.targetOrg, c.targetUser); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestCanViewApp(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 任意应用可看", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true},
		{"org_admin 同组织应用可看", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},
		{"org_admin 跨组织不可看", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},
		{"org_member 仅看自己拥有的", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},
		{"org_member 不可看同组织他人", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Principal{UserID: c.pUserID, OrgID: c.pOrgID, Role: c.role}
			if got := CanViewApp(p, c.targetOrg, c.targetUser); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestCanViewOrgPersona(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可读 persona", domain.UserRolePlatformAdmin, orgA, orgB, true},
		{"org_admin 同组织可读 persona", domain.UserRoleOrgAdmin, orgA, orgA, true},
		{"org_admin 跨组织不可读 persona", domain.UserRoleOrgAdmin, orgA, orgB, false},
		{"org_member 同组织可读 persona", domain.UserRoleOrgMember, orgA, orgA, true},
		{"org_member 跨组织不可读 persona", domain.UserRoleOrgMember, orgA, orgB, false},
	}
	runOrgCases(t, CanViewOrgPersona, cases)
}

func TestCanManageOrgPersona(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可管 persona", domain.UserRolePlatformAdmin, orgA, orgB, true},
		{"org_admin 同组织可管 persona", domain.UserRoleOrgAdmin, orgA, orgA, true},
		{"org_admin 跨组织不可管 persona", domain.UserRoleOrgAdmin, orgA, orgB, false},
		{"org_member 同组织也不可管 persona", domain.UserRoleOrgMember, orgA, orgA, false},
		{"未知角色不可管 persona", "unknown", orgA, orgA, false},
	}
	runOrgCases(t, CanManageOrgPersona, cases)
}

func runAppCases(t *testing.T, fn func(Principal, string, string) bool, cases []memberCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Principal{UserID: c.pUserID, OrgID: c.pOrgID, Role: c.role}
			if got := fn(p, c.targetOrg, c.targetUser); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestCanManageApp(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 跨组织可管应用", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true},
		{"org_admin 同组织可管应用", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},
		{"org_admin 跨组织不可管", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},
		{"org_member 仅可管自己应用", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},
		{"org_member 不可管他人应用", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},
	}
	runAppCases(t, CanManageApp, cases)
}

func TestCanWriteAppKnowledge(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 跨组织可写知识库", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true},
		{"org_admin 同组织可写知识库", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},
		{"org_admin 跨组织不可写", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},
		{"org_member 仅可写自己应用知识库", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},
		{"org_member 不可写他人应用知识库", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},
	}
	runAppCases(t, CanWriteAppKnowledge, cases)
}

func TestCanReadAppKnowledge(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 跨组织可读知识库", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true},
		{"org_admin 同组织可读知识库", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},
		{"org_admin 跨组织不可读", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},
		{"org_member 仅可读自己应用知识库", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},
		{"org_member 不可读他人应用知识库", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},
	}
	runAppCases(t, CanReadAppKnowledge, cases)
}

func TestCanTriggerRuntimeOperation(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 跨组织可触发运行操作", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true},
		{"org_admin 同组织可触发运行操作", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},
		{"org_admin 跨组织不可触发", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},
		{"org_member 仅可触发自己应用的运行操作", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},
		{"org_member 不可触发他人应用的运行操作", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},
	}
	runAppCases(t, CanTriggerRuntimeOperation, cases)
}
