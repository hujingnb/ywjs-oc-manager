// Package auth 已含 Principal / TokenManager 等身份相关原语。
// authorizer.go 把所有「角色 + 资源归属」权限谓词集中在此，service 层不再定义本地 canX 函数。
package auth

import "oc-manager/internal/domain"

// 组织资源 ----------------------------------------------------------

// CanManageOrg 判断主体能否对指定组织执行写操作（成员管理、状态调整等）。
func CanManageOrg(p Principal, orgID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == orgID
	default:
		return false
	}
}

// CanViewOrg 判断主体能否查看指定组织内的资源（读路径）。
func CanViewOrg(p Principal, orgID string) bool {
	if p.Role == domain.UserRolePlatformAdmin {
		return true
	}
	return p.OrgID == orgID
}

// 成员资源 ----------------------------------------------------------

// CanViewMember 判断主体能否查看目标成员明细。
// 普通成员只能查看自己；组织管理员可查本组织成员；平台管理员不限。
func CanViewMember(p Principal, memberOrgID, memberUserID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == memberOrgID
	case domain.UserRoleOrgMember:
		return p.UserID == memberUserID
	default:
		return false
	}
}

// CanManageMember 判断主体能否对目标成员执行写操作（角色调整、状态切换、密码重置）。
func CanManageMember(p Principal, memberOrgID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == memberOrgID
	default:
		return false
	}
}

// CanEditMember 判断主体能否更新目标成员资料（含本人编辑自身）。
func CanEditMember(p Principal, memberOrgID, memberUserID string) bool {
	if CanManageMember(p, memberOrgID) {
		return true
	}
	return p.UserID == memberUserID
}

// 应用资源 ----------------------------------------------------------

// CanViewApp 判断主体能否查看指定应用。
func CanViewApp(p Principal, appOrgID, appOwnerUserID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == appOrgID
	case domain.UserRoleOrgMember:
		return p.UserID == appOwnerUserID
	default:
		return false
	}
}

// Persona 资源 ----------------------------------------------------
// 当前规则与组织读/写谓词完全等价；保留独立函数以便未来 persona
// 单独演进权限规则时只改这两处，不动调用方。

// CanViewOrgPersona 等价于 CanViewOrg，保留位置以便未来差异化。
func CanViewOrgPersona(p Principal, orgID string) bool {
	return CanViewOrg(p, orgID)
}

// CanManageOrgPersona 等价于 CanManageOrg，保留位置以便未来差异化。
func CanManageOrgPersona(p Principal, orgID string) bool {
	return CanManageOrg(p, orgID)
}
