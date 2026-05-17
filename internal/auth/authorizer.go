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
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin, domain.UserRoleOrgMember:
		return p.OrgID == orgID
	default:
		return false
	}
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

// CanManageMember 判断主体能否对目标成员执行写操作（创建、角色调整、状态切换、密码重置）。
// 平台管理员只保留跨组织成员观察能力，不直接介入组织成员生命周期。
func CanManageMember(p Principal, memberOrgID string) bool {
	switch p.Role {
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
	switch p.Role {
	case domain.UserRoleOrgAdmin, domain.UserRoleOrgMember, domain.UserRolePlatformAdmin:
		return p.UserID == memberUserID
	default:
		return false
	}
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

// CanViewAppAudit 判断主体是否可查看指定应用的审计记录。
// 审计读取属于观察能力：平台管理员可跨组织查看，组织管理员可查看本组织应用，
// 组织成员只能查看自己拥有的应用，避免借 target 审计窥探同组织其他成员。
func CanViewAppAudit(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanViewApp(p, appOrgID, appOwnerUserID)
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

// 应用资源（业务别名） ---------------------------------------------
// 下面按“读权限”和“写/运行时权限”拆分应用相关谓词：
// 读取类谓词保留 CanViewApp 的跨组织观察语义，写入/运行时类谓词则收紧到组织管理员或 owner 成员。

// CanManageApp 判断主体是否可对应用执行管理写操作（如渠道绑定）。
// 平台管理员只有跨组织观察权限，不继承任何应用写权限；
// 组织管理员仅能管理本组织应用，组织成员仅能管理自己拥有的应用。
func CanManageApp(p Principal, appOrgID, appOwnerUserID string) bool {
	switch p.Role {
	case domain.UserRoleOrgAdmin:
		return p.OrgID == appOrgID
	case domain.UserRoleOrgMember:
		return p.UserID == appOwnerUserID
	default:
		return false
	}
}

// CanReadOrgKnowledge 判断主体是否可读取组织知识库。
// 组织知识库读取沿用组织读权限：平台管理员可跨组织观察，本组织管理员/成员可读本组织。
func CanReadOrgKnowledge(p Principal, orgID string) bool {
	return CanViewOrg(p, orgID)
}

// CanWriteOrgKnowledge 判断主体是否可写入组织知识库。
// 组织知识库写入只允许本组织管理员，平台管理员不可绕过组织边界直接写入。
func CanWriteOrgKnowledge(p Principal, orgID string) bool {
	return p.Role == domain.UserRoleOrgAdmin && p.OrgID == orgID
}

// CanViewOrgKnowledgeSyncStatus 判断主体是否可查看组织知识库同步状态。
// 同步状态属于组织知识库的运维读视图，当前只开放给本组织管理员；
// 单独保留该谓词，避免调用方把“查看状态”和“触发重试”混用成同一个权限语义。
func CanViewOrgKnowledgeSyncStatus(p Principal, orgID string) bool {
	return p.Role == domain.UserRoleOrgAdmin && p.OrgID == orgID
}

// CanRetryOrgKnowledgeSync 判断主体是否可重试组织知识库同步。
// 同步重试会改变组织知识库状态，因此与组织知识库写权限保持一致。
func CanRetryOrgKnowledgeSync(p Principal, orgID string) bool {
	return CanWriteOrgKnowledge(p, orgID)
}

// CanWriteAppKnowledge 判断主体是否可写入指定应用的知识库。
// 应用知识库写入属于应用写操作，沿用应用管理权限；
// 平台管理员仍可读，但不能直接写入任意组织应用的知识库。
func CanWriteAppKnowledge(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanManageApp(p, appOrgID, appOwnerUserID)
}

// CanReadAppKnowledge 判断主体是否可读取指定应用的知识库。
// 应用知识库读取沿用应用读权限，平台管理员保留跨组织观察能力。
func CanReadAppKnowledge(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanViewApp(p, appOrgID, appOwnerUserID)
}

// CanTriggerRuntimeOperation 判断主体是否可对应用触发运行时操作（启停/重启等）。
// 运行时操作会直接影响应用状态，因此沿用应用管理权限；
// 调用方仍需在此之前额外校验 user.status != disabled，disabled 账号不得触发任何运行时操作。
func CanTriggerRuntimeOperation(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanManageApp(p, appOrgID, appOwnerUserID)
}

// CanCreateAppForOrg 判断主体是否可在指定组织下创建应用。
// 当前仅允许本组织管理员通过 onboarding 等入口创建，避免平台管理员绕过组织侧写权限。
func CanCreateAppForOrg(p Principal, orgID string) bool {
	return p.Role == domain.UserRoleOrgAdmin && p.OrgID == orgID
}

// CanCreateAppForMember 判断主体是否可为指定组织内的已有成员创建实例。
// 平台管理员负责跨组织运维复建；组织管理员只允许在本组织内创建；
// 普通成员不能自行创建或复建实例。
func CanCreateAppForMember(p Principal, orgID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == orgID
	default:
		return false
	}
}

// CanViewOrgUsage 判断主体是否可查看组织聚合用量。
// 组织聚合视角只开放给平台管理员和本组织管理员，普通成员需降级到成员/应用视角。
func CanViewOrgUsage(p Principal, orgID string) bool {
	return p.Role == domain.UserRolePlatformAdmin || (p.Role == domain.UserRoleOrgAdmin && p.OrgID == orgID)
}

// CanViewMemberUsage 判断主体是否可查看成员用量。
// 平台管理员可跨组织查看，组织管理员仅限本组织，普通成员仅可查看自己的成员维度。
func CanViewMemberUsage(p Principal, orgID, memberID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == orgID
	case domain.UserRoleOrgMember:
		return p.OrgID == orgID && p.UserID == memberID
	default:
		return false
	}
}

// CanViewOrgAudit 判断主体是否可查看组织审计列表。
// 组织级审计属于管理面能力，仅平台管理员和本组织管理员可查看。
func CanViewOrgAudit(p Principal, orgID string) bool {
	return p.Role == domain.UserRolePlatformAdmin || (p.Role == domain.UserRoleOrgAdmin && p.OrgID == orgID)
}

// CanViewOwnAudit 判断主体是否可查看”我的审计”视角。
// 该视角必须能落到受支持的具体操作者，因此要求主体属于已知角色且具备非空 userID。
func CanViewOwnAudit(p Principal) bool {
	if p.UserID == "" {
		return false
	}
	switch p.Role {
	case domain.UserRolePlatformAdmin, domain.UserRoleOrgAdmin, domain.UserRoleOrgMember:
		return true
	default:
		return false
	}
}

// CanViewRecharges 判断主体是否可查看指定组织的充值记录。
// 平台管理员可查任意组织；组织管理员仅可查自己所属组织的充值记录。
func CanViewRecharges(p Principal, orgID string) bool {
	return p.Role == domain.UserRolePlatformAdmin ||
		(p.Role == domain.UserRoleOrgAdmin && p.OrgID == orgID)
}
