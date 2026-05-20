// Package auth 的权限测试覆盖角色、组织归属和资源 owner 的组合边界。
package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"oc-manager/internal/domain"
)

const (
	// orgA/orgB 与 userA/userB 组成跨组织、跨成员的最小权限矩阵。
	orgA  = "org-A"
	orgB  = "org-B"
	userA = "user-A"
	userB = "user-B"
)

type orgCase struct {
	name string
	// role 是当前操作者角色，覆盖平台管理员、组织管理员、成员和未知角色。
	role string
	// pOrgID 是当前操作者所属组织，targetOrg 是被访问资源所属组织。
	pOrgID    string
	targetOrg string
	want      bool
}

func runOrgCases(t *testing.T, fn func(Principal, string) bool, cases []orgCase) {
	t.Helper()
	for _, c := range cases {
		// 当前子测试覆盖表格用例中该名称对应的输入组合、边界条件和期望结果。
		t.Run(c.name, func(t *testing.T) {
			p := Principal{UserID: userA, OrgID: c.pOrgID, Role: c.role}
			got := fn(p, c.targetOrg)
			assert.Equal(t, c.want, got)
		})
	}
}

// TestCanManageOrg 验证管理权限组织的预期行为场景。
func TestCanManageOrg(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可管", domain.UserRolePlatformAdmin, orgA, orgB, true}, // 场景：platform_admin 跨组织可管
		{"org_admin 同组织可管", domain.UserRoleOrgAdmin, orgA, orgA, true},           // 场景：org_admin 同组织可管
		{"org_admin 跨组织不可管", domain.UserRoleOrgAdmin, orgA, orgB, false},         // 场景：org_admin 跨组织不可管
		{"org_member 同组织也不可管", domain.UserRoleOrgMember, orgA, orgA, false},      // 场景：org_member 同组织也不可管
		{"未知角色不可管", "unknown", orgA, orgA, false},                                // 场景：未知角色不可管
	}
	runOrgCases(t, CanManageOrg, cases)
}

// TestCanViewOrg 验证查看权限组织的预期行为场景。
func TestCanViewOrg(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可读", domain.UserRolePlatformAdmin, orgA, orgB, true}, // 场景：platform_admin 跨组织可读
		{"org_admin 同组织可读", domain.UserRoleOrgAdmin, orgA, orgA, true},           // 场景：org_admin 同组织可读
		{"org_admin 跨组织不可读", domain.UserRoleOrgAdmin, orgA, orgB, false},         // 场景：org_admin 跨组织不可读
		{"org_member 同组织可读", domain.UserRoleOrgMember, orgA, orgA, true},         // 场景：org_member 同组织可读
		{"org_member 跨组织不可读", domain.UserRoleOrgMember, orgA, orgB, false},       // 场景：org_member 跨组织不可读
		{"未知角色同组织不可读", "unknown", orgA, orgA, false},                             // 场景：未知角色同组织不可读
	}
	runOrgCases(t, CanViewOrg, cases)
}

type memberCase struct {
	name string
	// role/pOrgID/pUserID 描述当前操作者身份，target* 描述目标成员或应用资源。
	role       string
	pOrgID     string
	pUserID    string
	targetOrg  string
	targetUser string
	want       bool
}

// TestCanViewMember 验证查看权限成员的预期行为场景。
func TestCanViewMember(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 任意成员可看", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：platform_admin 任意成员可看
		{"org_admin 同组织可看", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},            // 场景：org_admin 同组织可看
		{"org_admin 跨组织不可看", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},          // 场景：org_admin 跨组织不可看
		{"org_member 仅看自己", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},           // 场景：org_member 仅看自己
		{"org_member 不可看他人", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},         // 场景：org_member 不可看他人
	}
	for _, c := range cases {
		// 当前子测试覆盖表格用例中该名称对应的输入组合、边界条件和期望结果。
		t.Run(c.name, func(t *testing.T) {
			p := Principal{UserID: c.pUserID, OrgID: c.pOrgID, Role: c.role}
			got := CanViewMember(p, c.targetOrg, c.targetUser)
			assert.Equal(t, c.want, got)
		})
	}
}

// TestCanManageMember 验证管理权限成员的预期行为场景。
func TestCanManageMember(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织只读成员不可管", domain.UserRolePlatformAdmin, orgA, orgB, false}, // 场景：platform_admin 跨组织只读成员不可管
		{"org_admin 同组织可管成员", domain.UserRoleOrgAdmin, orgA, orgA, true},               // 场景：org_admin 同组织可管成员
		{"org_admin 跨组织不可管", domain.UserRoleOrgAdmin, orgA, orgB, false},               // 场景：org_admin 跨组织不可管
		{"org_member 一律不可管", domain.UserRoleOrgMember, orgA, orgA, false},              // 场景：org_member 一律不可管
	}
	runOrgCases(t, CanManageMember, cases)
}

// TestCanEditMember 验证权限判断编辑成员的预期行为场景。
func TestCanEditMember(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 不可编辑组织成员", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, false}, // 场景：platform_admin 不可编辑组织成员
		{"org_admin 同组织可编辑", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},              // 场景：org_admin 同组织可编辑
		{"org_admin 跨组织不可编辑", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},            // 场景：org_admin 跨组织不可编辑
		{"org_member 仅可编辑自己", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},            // 场景：org_member 仅可编辑自己
		{"org_member 不可编辑他人", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},           // 场景：org_member 不可编辑他人
		{"未知角色即使命中本人也不可编辑", "unknown", orgA, userA, orgB, userA, false},                            // 场景：未知角色即使命中本人也不可编辑
	}
	for _, c := range cases {
		// 当前子测试覆盖表格用例中该名称对应的输入组合、边界条件和期望结果。
		t.Run(c.name, func(t *testing.T) {
			p := Principal{UserID: c.pUserID, OrgID: c.pOrgID, Role: c.role}
			got := CanEditMember(p, c.targetOrg, c.targetUser)
			assert.Equal(t, c.want, got)
		})
	}
}

// TestCanViewApp 验证查看权限应用的预期行为场景。
func TestCanViewApp(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 任意应用可看", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：platform_admin 任意应用可看
		{"org_admin 同组织应用可看", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},          // 场景：org_admin 同组织应用可看
		{"org_admin 跨组织不可看", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},          // 场景：org_admin 跨组织不可看
		{"org_member 仅看自己拥有的", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},        // 场景：org_member 仅看自己拥有的
		{"org_member 不可看同组织他人", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},      // 场景：org_member 不可看同组织他人
	}
	for _, c := range cases {
		// 当前子测试覆盖表格用例中该名称对应的输入组合、边界条件和期望结果。
		t.Run(c.name, func(t *testing.T) {
			p := Principal{UserID: c.pUserID, OrgID: c.pOrgID, Role: c.role}
			got := CanViewApp(p, c.targetOrg, c.targetUser)
			assert.Equal(t, c.want, got)
		})
	}
}

// TestCanViewAppAudit 验证查看权限应用审计的预期行为场景。
func TestCanViewAppAudit(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 可看任意应用审计", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：platform_admin 可看任意应用审计
		{"org_admin 可看本组织应用审计", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},          // 场景：org_admin 可看本组织应用审计
		{"org_admin 不可看跨组织应用审计", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},        // 场景：org_admin 不可看跨组织应用审计
		{"org_member 仅可看自己应用审计", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},        // 场景：org_member 仅可看自己应用审计
		{"org_member 不可看同组织他人应用审计", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},    // 场景：org_member 不可看同组织他人应用审计
	}
	runAppCases(t, CanViewAppAudit, cases)
}

// TestCanViewOrgPersona 验证查看权限组织人设的预期行为场景。
func TestCanViewOrgPersona(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可读 persona", domain.UserRolePlatformAdmin, orgA, orgB, true}, // 场景：platform_admin 跨组织可读 persona
		{"org_admin 同组织可读 persona", domain.UserRoleOrgAdmin, orgA, orgA, true},           // 场景：org_admin 同组织可读 persona
		{"org_admin 跨组织不可读 persona", domain.UserRoleOrgAdmin, orgA, orgB, false},         // 场景：org_admin 跨组织不可读 persona
		{"org_member 同组织可读 persona", domain.UserRoleOrgMember, orgA, orgA, true},         // 场景：org_member 同组织可读 persona
		{"org_member 跨组织不可读 persona", domain.UserRoleOrgMember, orgA, orgB, false},       // 场景：org_member 跨组织不可读 persona
	}
	runOrgCases(t, CanViewOrgPersona, cases)
}

// TestCanManageOrgPersona 验证管理权限组织人设的预期行为场景。
func TestCanManageOrgPersona(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可管 persona", domain.UserRolePlatformAdmin, orgA, orgB, true}, // 场景：platform_admin 跨组织可管 persona
		{"org_admin 同组织可管 persona", domain.UserRoleOrgAdmin, orgA, orgA, true},           // 场景：org_admin 同组织可管 persona
		{"org_admin 跨组织不可管 persona", domain.UserRoleOrgAdmin, orgA, orgB, false},         // 场景：org_admin 跨组织不可管 persona
		{"org_member 同组织也不可管 persona", domain.UserRoleOrgMember, orgA, orgA, false},      // 场景：org_member 同组织也不可管 persona
		{"未知角色不可管 persona", "unknown", orgA, orgA, false},                                // 场景：未知角色不可管 persona
	}
	runOrgCases(t, CanManageOrgPersona, cases)
}

func runAppCases(t *testing.T, fn func(Principal, string, string) bool, cases []memberCase) {
	t.Helper()
	for _, c := range cases {
		// 当前子测试覆盖表格用例中该名称对应的输入组合、边界条件和期望结果。
		t.Run(c.name, func(t *testing.T) {
			p := Principal{UserID: c.pUserID, OrgID: c.pOrgID, Role: c.role}
			got := fn(p, c.targetOrg, c.targetUser)
			assert.Equal(t, c.want, got)
		})
	}
}

// TestCanManageApp 验证管理权限应用的预期行为场景。
func TestCanManageApp(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 跨组织不可管应用", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, false}, // 场景：platform_admin 跨组织不可管应用
		{"org_admin 同组织可管应用", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},             // 场景：org_admin 同组织可管应用
		{"org_admin 跨组织不可管", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},             // 场景：org_admin 跨组织不可管
		{"org_member 仅可管自己应用", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},           // 场景：org_member 仅可管自己应用
		{"org_member 不可管他人应用", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},          // 场景：org_member 不可管他人应用
	}
	runAppCases(t, CanManageApp, cases)
}

// TestOrgKnowledgePredicates 验证组织知识库权限谓词的预期行为场景。
func TestOrgKnowledgePredicates(t *testing.T) {
	readCases := []orgCase{
		{"platform_admin 跨组织可读组织知识库", domain.UserRolePlatformAdmin, orgA, orgB, true}, // 场景：platform_admin 跨组织可读组织知识库
		{"org_admin 同组织可读组织知识库", domain.UserRoleOrgAdmin, orgA, orgA, true},           // 场景：org_admin 同组织可读组织知识库
		{"org_admin 跨组织不可读组织知识库", domain.UserRoleOrgAdmin, orgA, orgB, false},         // 场景：org_admin 跨组织不可读组织知识库
		{"org_member 同组织可读组织知识库", domain.UserRoleOrgMember, orgA, orgA, true},         // 场景：org_member 同组织可读组织知识库
		{"org_member 跨组织不可读组织知识库", domain.UserRoleOrgMember, orgA, orgB, false},       // 场景：org_member 跨组织不可读组织知识库
	}
	runOrgCases(t, CanReadOrgKnowledge, readCases)

	writeCases := []orgCase{
		{"platform_admin 不可写组织知识库", domain.UserRolePlatformAdmin, orgA, orgB, false}, // 场景：platform_admin 不可写组织知识库
		{"org_admin 同组织可写组织知识库", domain.UserRoleOrgAdmin, orgA, orgA, true},          // 场景：org_admin 同组织可写组织知识库
		{"org_admin 跨组织不可写组织知识库", domain.UserRoleOrgAdmin, orgA, orgB, false},        // 场景：org_admin 跨组织不可写组织知识库
		{"org_member 同组织不可写组织知识库", domain.UserRoleOrgMember, orgA, orgA, false},      // 场景：org_member 同组织不可写组织知识库
		{"org_member 跨组织不可写组织知识库", domain.UserRoleOrgMember, orgA, orgB, false},      // 场景：org_member 跨组织不可写组织知识库
	}
	runOrgCases(t, CanWriteOrgKnowledge, writeCases)
	runOrgCases(t, CanViewOrgKnowledgeSyncStatus, writeCases)
	runOrgCases(t, CanRetryOrgKnowledgeSync, writeCases)
}

// TestCanWriteAppKnowledge 验证写入权限应用知识库的预期行为场景。
func TestCanWriteAppKnowledge(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 跨组织不可写应用知识库", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, false}, // 场景：platform_admin 跨组织不可写应用知识库
		{"org_admin 同组织可写知识库", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},               // 场景：org_admin 同组织可写知识库
		{"org_admin 跨组织不可写", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},                // 场景：org_admin 跨组织不可写
		{"org_member 仅可写自己应用知识库", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},           // 场景：org_member 仅可写自己应用知识库
		{"org_member 不可写他人应用知识库", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},          // 场景：org_member 不可写他人应用知识库
	}
	runAppCases(t, CanWriteAppKnowledge, cases)
}

// TestCanReadAppKnowledge 验证读取权限应用知识库的预期行为场景。
func TestCanReadAppKnowledge(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 跨组织可读知识库", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：platform_admin 跨组织可读知识库
		{"org_admin 同组织可读知识库", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},           // 场景：org_admin 同组织可读知识库
		{"org_admin 跨组织不可读", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},            // 场景：org_admin 跨组织不可读
		{"org_member 仅可读自己应用知识库", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},       // 场景：org_member 仅可读自己应用知识库
		{"org_member 不可读他人应用知识库", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},      // 场景：org_member 不可读他人应用知识库
	}
	runAppCases(t, CanReadAppKnowledge, cases)
}

// TestCanTriggerRuntimeOperation 验证触发权限运行时Operation的预期行为场景。
func TestCanTriggerRuntimeOperation(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 跨组织不可触发运行操作", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, false}, // 场景：platform_admin 跨组织不可触发运行操作
		{"org_admin 同组织可触发运行操作", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},             // 场景：org_admin 同组织可触发运行操作
		{"org_admin 跨组织不可触发", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},               // 场景：org_admin 跨组织不可触发
		{"org_member 仅可触发自己应用的运行操作", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},        // 场景：org_member 仅可触发自己应用的运行操作
		{"org_member 不可触发他人应用的运行操作", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},       // 场景：org_member 不可触发他人应用的运行操作
	}
	runAppCases(t, CanTriggerRuntimeOperation, cases)
}

// TestCanCreateAppForOrg 验证创建权限应用针对组织的预期行为场景。
func TestCanCreateAppForOrg(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 不可为任意组织创建应用", domain.UserRolePlatformAdmin, orgA, orgA, false}, // 场景：platform_admin 不可为任意组织创建应用
		{"org_admin 同组织可创建应用", domain.UserRoleOrgAdmin, orgA, orgA, true},               // 场景：org_admin 同组织可创建应用
		{"org_admin 跨组织不可创建应用", domain.UserRoleOrgAdmin, orgA, orgB, false},             // 场景：org_admin 跨组织不可创建应用
		{"org_member 同组织不可创建应用", domain.UserRoleOrgMember, orgA, orgA, false},           // 场景：org_member 同组织不可创建应用
	}
	runOrgCases(t, CanCreateAppForOrg, cases)
}

// TestCanCreateAppForMember 验证为已有成员创建实例的权限边界。
func TestCanCreateAppForMember(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 可为任意组织成员创建实例", domain.UserRolePlatformAdmin, orgA, orgB, true}, // 场景：平台管理员跨组织为已有成员重建实例。
		{"org_admin 可为本组织成员创建实例", domain.UserRoleOrgAdmin, orgA, orgA, true},           // 场景：组织管理员仍保留本组织创建实例能力。
		{"org_admin 不可为其他组织成员创建实例", domain.UserRoleOrgAdmin, orgA, orgB, false},       // 场景：组织管理员不能越过组织边界。
		{"org_member 不可创建实例", domain.UserRoleOrgMember, orgA, orgA, false},                // 场景：普通成员没有实例创建权限。
		{"未知角色不可创建实例", "unknown", orgA, orgA, false},                                  // 场景：未知角色降级为无权限。
	}
	runOrgCases(t, CanCreateAppForMember, cases)
}

// TestCanViewOrgUsage 验证查看权限组织用量的预期行为场景。
func TestCanViewOrgUsage(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可看组织用量", domain.UserRolePlatformAdmin, orgA, orgB, true}, // 场景：platform_admin 跨组织可看组织用量
		{"org_admin 同组织可看组织用量", domain.UserRoleOrgAdmin, orgA, orgA, true},           // 场景：org_admin 同组织可看组织用量
		{"org_admin 跨组织不可看组织用量", domain.UserRoleOrgAdmin, orgA, orgB, false},         // 场景：org_admin 跨组织不可看组织用量
		{"org_member 同组织不可看组织用量", domain.UserRoleOrgMember, orgA, orgA, false},       // 场景：org_member 同组织不可看组织用量
	}
	runOrgCases(t, CanViewOrgUsage, cases)
}

// TestCanViewMemberUsage 验证查看权限成员用量的预期行为场景。
func TestCanViewMemberUsage(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 可看任意成员用量", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：platform_admin 可看任意成员用量
		{"org_admin 同组织可看成员用量", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},          // 场景：org_admin 同组织可看成员用量
		{"org_admin 跨组织不可看成员用量", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},        // 场景：org_admin 跨组织不可看成员用量
		{"org_member 仅可看自己成员用量", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},        // 场景：org_member 仅可看自己成员用量
		{"org_member 不可看他人成员用量", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},       // 场景：org_member 不可看他人成员用量
	}
	runAppCases(t, CanViewMemberUsage, cases)
}

// TestCanViewOrgAudit 验证查看权限组织审计的预期行为场景。
func TestCanViewOrgAudit(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可看组织审计", domain.UserRolePlatformAdmin, orgA, orgB, true}, // 场景：platform_admin 跨组织可看组织审计
		{"org_admin 同组织可看组织审计", domain.UserRoleOrgAdmin, orgA, orgA, true},           // 场景：org_admin 同组织可看组织审计
		{"org_admin 跨组织不可看组织审计", domain.UserRoleOrgAdmin, orgA, orgB, false},         // 场景：org_admin 跨组织不可看组织审计
		{"org_member 同组织不可看组织审计", domain.UserRoleOrgMember, orgA, orgA, false},       // 场景：org_member 同组织不可看组织审计
	}
	runOrgCases(t, CanViewOrgAudit, cases)
}

// TestCanViewOwnAudit 验证查看权限本人审计的预期行为场景。
func TestCanViewOwnAudit(t *testing.T) {
	cases := []struct {
		name string
		p    Principal
		want bool
	}{
		{"platform_admin 有 userID 可看自己的审计", Principal{UserID: userA, Role: domain.UserRolePlatformAdmin}, true},      // 场景：platform_admin 有 userID 可看自己的审计
		{"org_admin 有 userID 可看自己的审计", Principal{UserID: userA, OrgID: orgA, Role: domain.UserRoleOrgAdmin}, true},   // 场景：org_admin 有 userID 可看自己的审计
		{"org_member 有 userID 可看自己的审计", Principal{UserID: userA, OrgID: orgA, Role: domain.UserRoleOrgMember}, true}, // 场景：org_member 有 userID 可看自己的审计
		{"未知角色即使有 userID 也不可看自己的审计", Principal{UserID: userA, Role: "unknown"}, false},                               // 场景：未知角色即使有 userID 也不可看自己的审计
		{"空 userID 不可看自己的审计", Principal{Role: domain.UserRoleOrgMember, OrgID: orgA}, false},                         // 场景：空 userID 不可看自己的审计
	}
	for _, c := range cases {
		// 当前子测试覆盖表格用例中该名称对应的输入组合、边界条件和期望结果。
		t.Run(c.name, func(t *testing.T) {
			got := CanViewOwnAudit(c.p)
			assert.Equal(t, c.want, got)
		})
	}
}

// TestCanViewAppKanban 验证 Kanban 读权限三层角色判断。
func TestCanViewAppKanban(t *testing.T) {
	cases := []memberCase{
		// 平台管理员：跨组织可看
		{"platform_admin 跨组织可看 kanban", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：platform_admin 跨组织可看 kanban
		// 本组织管理员：可看本组织应用
		{"org_admin 同组织可看 kanban", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true}, // 场景：org_admin 同组织可看 kanban
		// 外组织管理员：不可看
		{"org_admin 跨组织不可看 kanban", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false}, // 场景：org_admin 跨组织不可看 kanban
		// 应用拥有者本人：可看
		{"org_member 拥有者本人可看 kanban", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true}, // 场景：org_member 拥有者本人可看 kanban
		// 非拥有者的普通成员：不可看
		{"org_member 非拥有者不可看 kanban", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false}, // 场景：org_member 非拥有者不可看 kanban
	}
	runAppCases(t, CanViewAppKanban, cases)
}

// TestCanManageAppKanban 验证 Kanban 写权限：与读权限一致（所有可见角色可写）。
func TestCanManageAppKanban(t *testing.T) {
	cases := []memberCase{
		// 应用拥有者本人可写
		{"org_member 拥有者本人可写 kanban", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true}, // 场景：org_member 拥有者本人可写 kanban
		// 外组织成员不可写
		{"org_member 外组织不可写 kanban", domain.UserRoleOrgMember, orgA, userA, orgB, userB, false}, // 场景：org_member 外组织不可写 kanban
		// 本组织管理员可写
		{"org_admin 同组织可写 kanban", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true}, // 场景：org_admin 同组织可写 kanban
		// 平台管理员跨组织可写（与 CanViewApp 一致）
		{"platform_admin 跨组织可写 kanban", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：platform_admin 跨组织可写 kanban
	}
	runAppCases(t, CanManageAppKanban, cases)
}
