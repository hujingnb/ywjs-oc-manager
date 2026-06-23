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

// CanListMembers 判断主体能否获取组织成员列表。
// 成员列表属于组织管理视角，普通成员无需访问他人信息，仅管理员可查。
func CanListMembers(p Principal, orgID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == orgID
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

// CanSwitchAppVersion 判断主体是否可切换应用绑定的助手版本。
// 版本切换是运维操作，平台管理员需介入版本统一管理；与渠道绑定、知识库写入等
// 纯组织侧操作不同，故单独建谓词而非扩展 CanManageApp。
func CanSwitchAppVersion(p Principal, appOrgID, appOwnerUserID string) bool {
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

// CanReadOrgKnowledge 判断主体是否可读取组织知识库。
// 组织知识库读取沿用组织读权限：平台管理员可跨组织观察，本组织管理员/成员可读本组织。
func CanReadOrgKnowledge(p Principal, orgID string) bool {
	return CanViewOrg(p, orgID)
}

// CanWriteOrgKnowledge 判断主体是否可写入组织知识库。
// 与 ragflow-knowledge-design 一致：允许组织管理员管本组织，平台管理员可跨组织维护知识库
// （上传公共制度文档、运维补充资料等场景需要平台侧介入；不放开会强迫将平台管理员降级为
// 某个组织的临时管理员，权限模型反而更乱）。
func CanWriteOrgKnowledge(p Principal, orgID string) bool {
	if p.Role == domain.UserRolePlatformAdmin {
		return true
	}
	return p.Role == domain.UserRoleOrgAdmin && p.OrgID == orgID
}

// CanUpdateOrgKnowledgeQuota 判断主体是否可编辑企业知识库容量。
// 容量属于平台侧租户配置，只允许平台管理员修改。
func CanUpdateOrgKnowledgeQuota(p Principal) bool {
	return p.Role == domain.UserRolePlatformAdmin
}

// CanManageIndustryKnowledge 判断主体是否可管理平台级行业知识库。
// 行业库是全局平台资源，不归属企业，只允许平台管理员创建、编辑、删除和管理文件。
func CanManageIndustryKnowledge(p Principal) bool {
	return p.Role == domain.UserRolePlatformAdmin
}

// CanManageKnowledgeRAGFlowDataset 判断主体是否可查看和修改知识库对应的 RAGFlow dataset 运维信息。
// 该能力会暴露远端 dataset ID 并触发整库重解析，只允许平台管理员使用。
func CanManageKnowledgeRAGFlowDataset(p Principal) bool {
	return p.Role == domain.UserRolePlatformAdmin
}

// CanUpdateAppLocale 判断主体是否可修改实例语言（hermes 对终端用户说话的语言）。
// 语言变更会触发容器重启（配置写入 manifest 后生效），属于有运行时影响的写操作；
// 平台管理员可运维兜底，本组织管理员可管本组织实例，实例 owner 可管自己的实例。
func CanUpdateAppLocale(p Principal, appOrgID, appOwnerUserID string) bool {
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

// CanUpdateAppKnowledgeQuota 判断主体是否可编辑实例知识库容量。
// 平台管理员可运维兜底；企业管理员仅能调整本企业实例；普通成员不可修改容量。
func CanUpdateAppKnowledgeQuota(p Principal, appOrgID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == appOrgID
	default:
		return false
	}
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
// 平台管理员需要介入实例运维（如强制重启故障实例），故此处与 CanManageApp 分离。
// 注：调用方仍需在此之前额外校验 user.status != disabled，disabled 账号不得触发运行时操作。
func CanTriggerRuntimeOperation(p Principal, appOrgID, appOwnerUserID string) bool {
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

// 任务看板 -----------------------------------------------------------

// CanViewAppKanban 判断 principal 能否查看应用的任务看板。
// 与查看应用详情同权限：平台管理员、本组织管理员、应用拥有者本人。
func CanViewAppKanban(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanViewApp(p, appOrgID, appOwnerUserID)
}

// CanManageAppKanban 判断 principal 能否对任务看板做写操作（评论 / 完成 / 阻塞等）。
// spec §7.4 规定：所有能查看实例详情的角色都可读写任务看板，因此委托 CanViewApp。
// 与 CanManageApp 的关键差异：CanManageApp 不允许 platform_admin 写应用配置；
// 而 CanManageAppKanban 委托 CanViewApp，有意保留 platform_admin 的写权限——
// 这是 spec §7.4 的设计决策，所有能查看实例详情的角色都能读写任务看板。
func CanManageAppKanban(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanViewApp(p, appOrgID, appOwnerUserID)
}

// 实例会话 ----------------------------------------------------------

// CanViewAppConversations 判断 principal 能否查看实例会话。
// 语义与 CanViewApp 一致：平台管理员、实例所属组织的 org_admin、实例 owner 本人可看。
func CanViewAppConversations(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanViewApp(p, appOrgID, appOwnerUserID)
}

// CanManageAppConversations 判断 principal 能否在实例会话里发消息 / 新建 / 删除 / 重命名会话。
// 当前与查看权限等价（委托 CanViewApp，与 kanban 的 CanManageAppKanban 同模式），
// 保留独立函数以便将来收紧写权限。
func CanManageAppConversations(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanViewApp(p, appOrgID, appOwnerUserID)
}

// Cron 任务 ---------------------------------------------------------

// CanViewAppCron 判断 principal 能否查看应用的 Cron 任务。
// Cron 读权限与应用详情一致：平台管理员、本组织管理员、应用拥有者本人可读。
func CanViewAppCron(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanViewApp(p, appOrgID, appOwnerUserID)
}

// CanManageAppCron 判断 principal 能否对应用 Cron 执行写操作（创建、更新、启停、运行、删除）。
// 已批准的权限范围要求所有能查看实例详情的角色都可管理 Cron，因此当前委托 CanViewApp；
// 单独保留谓词，便于未来 Cron 写权限收紧时只改权限层，不改 service/handler 调用点。
func CanManageAppCron(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanViewApp(p, appOrgID, appOwnerUserID)
}

// 助手版本资源 ----------------------------------------------------------

// CanManageAssistantVersion 判断主体能否创建/编辑/删除助手版本。
// 助手版本是平台级目录，仅平台管理员可写。
func CanManageAssistantVersion(p Principal) bool {
	return p.Role == domain.UserRolePlatformAdmin
}

// CanViewPlatformUsage 返回 principal 是否有权查看平台用量数据（包括组织分布）。
// 仅 platform_admin 有此权限。
func CanViewPlatformUsage(p Principal) bool {
	return p.Role == domain.UserRolePlatformAdmin
}

// CanViewAssistantVersion 判断主体能否查看助手版本。
// 平台管理员维护目录；组织管理员创建实例时需要读取版本；
// 组织成员需要在应用概览中查看自己实例绑定的版本名称，故同样开放。
func CanViewAssistantVersion(p Principal) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin, domain.UserRoleOrgAdmin, domain.UserRoleOrgMember:
		return true
	default:
		return false
	}
}

// 平台库 skill -----------------------------------------------------------

// CanManagePlatformSkill 判断是否可上传/删除平台库 skill：仅平台管理员。
func CanManagePlatformSkill(p Principal) bool {
	return p.Role == domain.UserRolePlatformAdmin
}

// CanViewPlatformSkillMarket 判断是否可浏览平台库 skill 市场：所有已登录用户均可查看。
// 市场是只读展示接口，无论角色均可浏览，写操作（上传/删除）仍需 CanManagePlatformSkill。
func CanViewPlatformSkillMarket(p Principal) bool {
	return p.Role != ""
}

// CanDownloadSkillArchive 判断是否可从详情页下载 skill 归档（平台技能 tar / ClawHub zip）：
// 仅平台管理员。下载会拿到完整归档原始字节，属受限运维操作，故权限比「浏览市场」更严。
func CanDownloadSkillArchive(p Principal) bool {
	return p.Role == domain.UserRolePlatformAdmin
}

// CanManageAppSkill 判断是否可管理某 app 的 skill：平台管理员可管理任意实例；
// 否则与应用写权限同款（owner 本人 / 本 org 的 org_admin）。
// 注意：删除「当前版本必需的 skill」对所有角色仍禁止，由 service 层 ErrAppSkillProtected 保证。
func CanManageAppSkill(p Principal, appOrgID, appOwnerUserID string) bool {
	if p.Role == domain.UserRolePlatformAdmin {
		return true
	}
	return CanManageApp(p, appOrgID, appOwnerUserID)
}

// 定制技能工单 ----------------------------------------------------------

// CanSubmitSkillTicket 判断能否提交定制需求工单:企业成员或企业管理员(平台管理员不提需求)。
func CanSubmitSkillTicket(p Principal) bool {
	return p.Role == domain.UserRoleOrgAdmin || p.Role == domain.UserRoleOrgMember
}

// CanManageSkillTicket 判断能否处理/交付/拒绝工单:仅平台管理员。
func CanManageSkillTicket(p Principal) bool {
	return p.Role == domain.UserRolePlatformAdmin
}

// CanViewSkillTicket 判断能否查看某工单详情/对话:提交者本人或平台管理员。
// 工单对企业内其他人不可见(可见的只有交付出来的 skill)。
func CanViewSkillTicket(p Principal, ticketRequesterUserID string) bool {
	return p.Role == domain.UserRolePlatformAdmin || p.UserID == ticketRequesterUserID
}
