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
		{"platform_admin 跨组织可写组织知识库", domain.UserRolePlatformAdmin, orgA, orgB, true}, // 场景：platform_admin 可跨组织维护知识库（上传公共制度文档 / 运维场景）
		{"platform_admin 同组织可写组织知识库", domain.UserRolePlatformAdmin, orgA, orgA, true}, // 场景：platform_admin 对自身归属组织也可写
		{"org_admin 同组织可写组织知识库", domain.UserRoleOrgAdmin, orgA, orgA, true},           // 场景：org_admin 同组织可写组织知识库
		{"org_admin 跨组织不可写组织知识库", domain.UserRoleOrgAdmin, orgA, orgB, false},         // 场景：org_admin 跨组织不可写组织知识库
		{"org_member 同组织不可写组织知识库", domain.UserRoleOrgMember, orgA, orgA, false},       // 场景：org_member 同组织不可写组织知识库
		{"org_member 跨组织不可写组织知识库", domain.UserRoleOrgMember, orgA, orgB, false},       // 场景：org_member 跨组织不可写组织知识库
	}
	runOrgCases(t, CanWriteOrgKnowledge, writeCases)
}

// TestCanUpdateOrgKnowledgeQuota 验证企业知识库容量只能由平台管理员修改。
func TestCanUpdateOrgKnowledgeQuota(t *testing.T) {
	assert.True(t, CanUpdateOrgKnowledgeQuota(Principal{Role: domain.UserRolePlatformAdmin}))
	assert.False(t, CanUpdateOrgKnowledgeQuota(Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1"}))
	assert.False(t, CanUpdateOrgKnowledgeQuota(Principal{Role: domain.UserRoleOrgMember, OrgID: "org-1"}))
}

// TestCanManageIndustryKnowledge 验证行业知识库是平台级资源，仅平台管理员可管理。
func TestCanManageIndustryKnowledge(t *testing.T) {
	assert.True(t, CanManageIndustryKnowledge(Principal{Role: domain.UserRolePlatformAdmin}))
	assert.False(t, CanManageIndustryKnowledge(Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1"}))
	assert.False(t, CanManageIndustryKnowledge(Principal{Role: domain.UserRoleOrgMember, OrgID: "org-1"}))
	assert.False(t, CanManageIndustryKnowledge(Principal{}))
}

// TestCanManageKnowledgeRAGFlowDataset 验证 RAGFlow dataset 运维信息只允许平台管理员查看和修改。
func TestCanManageKnowledgeRAGFlowDataset(t *testing.T) {
	// 平台管理员可执行 RAGFlow dataset 运维操作。
	assert.True(t, CanManageKnowledgeRAGFlowDataset(Principal{Role: domain.UserRolePlatformAdmin}))
	// 组织管理员不能查看远端 dataset ID 或触发整库重解析。
	assert.False(t, CanManageKnowledgeRAGFlowDataset(Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1"}))
	// 普通组织成员不能接触平台级 dataset 运维入口。
	assert.False(t, CanManageKnowledgeRAGFlowDataset(Principal{Role: domain.UserRoleOrgMember, OrgID: "org-1"}))
	// 空主体不具备任何 dataset 运维权限。
	assert.False(t, CanManageKnowledgeRAGFlowDataset(Principal{}))
}

// TestCanUpdateAppKnowledgeQuota 验证实例知识库容量允许平台管理员和本企业管理员修改。
func TestCanUpdateAppKnowledgeQuota(t *testing.T) {
	assert.True(t, CanUpdateAppKnowledgeQuota(Principal{Role: domain.UserRolePlatformAdmin}, "org-1"))
	assert.True(t, CanUpdateAppKnowledgeQuota(Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1"}, "org-1"))
	assert.False(t, CanUpdateAppKnowledgeQuota(Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-2"}, "org-1"))
	assert.False(t, CanUpdateAppKnowledgeQuota(Principal{Role: domain.UserRoleOrgMember, OrgID: "org-1"}, "org-1"))
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

// TestCanTriggerRuntimeOperation 验证触发运行时操作的权限边界。
func TestCanTriggerRuntimeOperation(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 可触发任意应用运行操作", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：平台管理员跨组织可触发运行时操作（启停/重启）
		{"org_admin 同组织可触发运行操作", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},            // 场景：org_admin 同组织可触发运行操作
		{"org_admin 跨组织不可触发", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},              // 场景：org_admin 跨组织不可触发
		{"org_member 仅可触发自己应用的运行操作", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},       // 场景：org_member 仅可触发自己应用的运行操作
		{"org_member 不可触发他人应用的运行操作", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},      // 场景：org_member 不可触发他人应用的运行操作
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
		{"org_admin 可为本组织成员创建实例", domain.UserRoleOrgAdmin, orgA, orgA, true},            // 场景：组织管理员仍保留本组织创建实例能力。
		{"org_admin 不可为其他组织成员创建实例", domain.UserRoleOrgAdmin, orgA, orgB, false},         // 场景：组织管理员不能越过组织边界。
		{"org_member 不可创建实例", domain.UserRoleOrgMember, orgA, orgA, false},              // 场景：普通成员没有实例创建权限。
		{"未知角色不可创建实例", "unknown", orgA, orgA, false},                                    // 场景：未知角色降级为无权限。
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
		// 外组织管理员不可写（组织边界隔离）
		{"org_admin 跨组织不可写 kanban", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false}, // 场景：org_admin 访问非本组织应用，应被拒绝
		// 平台管理员跨组织可写（与 CanViewApp 一致）
		{"platform_admin 跨组织可写 kanban", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：platform_admin 跨组织可写 kanban
	}
	runAppCases(t, CanManageAppKanban, cases)
}

// TestCanViewAppCron 验证 Cron 读权限与应用详情可见性保持一致。
func TestCanViewAppCron(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 跨组织可看 cron", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：平台管理员沿用应用详情跨组织观察能力
		{"org_admin 同组织可看 cron", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},           // 场景：组织管理员可查看本组织应用 Cron
		{"org_admin 跨组织不可看 cron", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},         // 场景：组织管理员不能越过组织边界
		{"org_member 拥有者本人可看 cron", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},       // 场景：普通成员只能查看自己拥有的应用 Cron
		{"org_member 非拥有者不可看 cron", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},      // 场景：普通成员不可查看同组织他人应用 Cron
	}
	runAppCases(t, CanViewAppCron, cases)
}

// TestCanManageAppCron 验证 Cron 写权限按批准范围与读权限一致。
func TestCanManageAppCron(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 跨组织可管理 cron", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：平台管理员可管理其可见实例的 Cron
		{"org_admin 同组织可管理 cron", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},           // 场景：组织管理员可管理本组织应用 Cron
		{"org_admin 跨组织不可管理 cron", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},         // 场景：组织管理员不能管理其他组织应用 Cron
		{"org_member 拥有者本人可管理 cron", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},       // 场景：应用拥有者可管理自己的 Cron
		{"org_member 非拥有者不可管理 cron", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},      // 场景：普通成员不能管理同组织他人应用 Cron
	}
	runAppCases(t, CanManageAppCron, cases)
}

// TestCanManageAssistantVersion 验证仅平台管理员可写助手版本。
func TestCanManageAssistantVersion(t *testing.T) {
	assert.True(t, CanManageAssistantVersion(Principal{Role: domain.UserRolePlatformAdmin}))
	assert.False(t, CanManageAssistantVersion(Principal{Role: domain.UserRoleOrgAdmin}))
	assert.False(t, CanManageAssistantVersion(Principal{Role: domain.UserRoleOrgMember}))
}

// TestCanViewAssistantVersion 验证三角色均可查看助手版本，未知角色不可。
func TestCanViewAssistantVersion(t *testing.T) {
	// 平台管理员维护版本目录，可查看。
	assert.True(t, CanViewAssistantVersion(Principal{Role: domain.UserRolePlatformAdmin}))
	// 组织管理员创建实例时需读取版本，可查看。
	assert.True(t, CanViewAssistantVersion(Principal{Role: domain.UserRoleOrgAdmin}))
	// 组织成员需在应用概览中查看绑定版本名称，可查看。
	assert.True(t, CanViewAssistantVersion(Principal{Role: domain.UserRoleOrgMember}))
	// 未知角色无权查看助手版本。
	assert.False(t, CanViewAssistantVersion(Principal{Role: "unknown"}))
}

// TestCanListMembers 验证成员列表读权限：仅平台管理员和组织管理员可访问，普通成员不可。
func TestCanListMembers(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可查列表", domain.UserRolePlatformAdmin, orgA, orgB, true}, // 场景：平台管理员跨组织可查成员列表
		{"org_admin 本组织可查列表", domain.UserRoleOrgAdmin, orgA, orgA, true},           // 场景：组织管理员在本组织内可查成员列表
		{"org_admin 跨组织不可查", domain.UserRoleOrgAdmin, orgA, orgB, false},           // 场景：组织管理员不可跨组织查成员列表
		{"org_member 本组织不可查列表", domain.UserRoleOrgMember, orgA, orgA, false},       // 场景：普通成员不可查看本组织成员列表
		{"未知角色不可查", "unknown", orgA, orgA, false},                                  // 场景：未知角色不可查
	}
	runOrgCases(t, CanListMembers, cases)
}

// TestCanSwitchAppVersion 验证应用版本切换权限：平台管理员可跨组织切换，组织管理员限本组织，成员限 owner。
func TestCanSwitchAppVersion(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 任意应用可切换版本", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：平台管理员跨组织可切换版本
		{"org_admin 本组织可切换版本", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},            // 场景：组织管理员可切换本组织应用版本
		{"org_admin 跨组织不可切换", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},            // 场景：组织管理员不可跨组织切换版本
		{"org_member 自己应用可切换", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},           // 场景：成员可切换自己拥有的应用版本
		{"org_member 他人应用不可切换", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},         // 场景：成员不可切换他人拥有的应用版本
		{"未知角色不可切换", "unknown", orgA, userA, orgA, userA, false},                                   // 场景：未知角色不可切换
	}
	runAppCases(t, CanSwitchAppVersion, cases)
}

// TestCanManagePlatformSkill 验证平台库 skill 管理权限：仅平台管理员可管理，org_admin / org_member 一律拒绝。
func TestCanManagePlatformSkill(t *testing.T) {
	// platform_admin 有权管理平台库 skill
	assert.True(t, CanManagePlatformSkill(Principal{Role: domain.UserRolePlatformAdmin}))
	// org_admin 无权管理平台库 skill
	assert.False(t, CanManagePlatformSkill(Principal{Role: domain.UserRoleOrgAdmin}))
	// org_member 无权管理平台库 skill
	assert.False(t, CanManagePlatformSkill(Principal{Role: domain.UserRoleOrgMember}))
}

// TestCanDownloadSkillArchive 验证下载 skill 归档权限：仅平台管理员，org_admin / org_member / 空角色一律拒绝。
func TestCanDownloadSkillArchive(t *testing.T) {
	// platform_admin 可下载归档
	assert.True(t, CanDownloadSkillArchive(Principal{Role: domain.UserRolePlatformAdmin}))
	// org_admin 不可下载
	assert.False(t, CanDownloadSkillArchive(Principal{Role: domain.UserRoleOrgAdmin}))
	// org_member 不可下载
	assert.False(t, CanDownloadSkillArchive(Principal{Role: domain.UserRoleOrgMember}))
	// 空角色（未认证）不可下载
	assert.False(t, CanDownloadSkillArchive(Principal{}))
}

// TestCanManageAppSkill 验证实例 skill 管理权限：平台管理员可管理任意实例（含跨组织）；
// 本组织 org_admin、应用 owner 本人可管理；跨组织成员和非 owner 的普通成员不可管理。
func TestCanManageAppSkill(t *testing.T) {
	cases := []memberCase{
		// owner 本人可管理自己应用的 skill
		{"owner 本人可管理实例 skill", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},
		// 同组织其他成员不可管理
		{"org_member 非 owner 不可管理实例 skill", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},
		// 本组织 org_admin 可管理本组织内任意应用的 skill
		{"org_admin 同组织可管理实例 skill", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},
		// 跨组织 org_admin 不可管理
		{"org_admin 跨组织不可管理实例 skill", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},
		// platform_admin 可管理任意实例的 skill（含跨组织），删除保护仍由 service 层 ErrAppSkillProtected 兜底
		{"platform_admin 可管理任意实例 skill", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true},
	}
	runAppCases(t, CanManageAppSkill, cases)
}
