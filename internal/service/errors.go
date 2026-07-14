// Package service 的所有可被 handler 层 errors.Is 检查的 sentinel error。
// 业务模块需要新增错误时优先扩展本文件，不要在各 service 文件内再定义本地 sentinel。
package service

import "errors"

// 通用错误 ----------------------------------------------------------

// ErrForbidden 表示主体已认证但无权访问目标资源。
var ErrForbidden = errors.New("无权执行该操作")

// ErrNotFound 表示资源不存在，handler 层统一映射为 404。
var ErrNotFound = errors.New("资源不存在")

// ErrConflict 表示资源冲突，如唯一性约束冲突或状态冲突。
var ErrConflict = errors.New("资源冲突")

// ErrInvalidArgument 表示请求参数通过了 HTTP 绑定但未通过业务边界校验。
var ErrInvalidArgument = errors.New("请求参数不合法")

// ErrQuotaExceeded 表示租户级数量或容量配额已用尽，handler 层映射为 409 Conflict。
var ErrQuotaExceeded = errors.New("已达到配额上限")

// ErrRateLimited 表示匿名或高频请求超过限流阈值，handler 层映射为 429。
var ErrRateLimited = errors.New("请求过于频繁")

// ErrAICCRateLimiterUnavailable 表示 AICC 匿名入口依赖的 Redis 限流存储不可用。
// 与真正超限不同，访客稍后重试即可，不应映射为 429 或未分类 500。
var ErrAICCRateLimiterUnavailable = errors.New("aicc rate limiter unavailable")

// ErrAICCSessionStoreUnavailable 表示 AICC 会话存储暂时不可访问。
var ErrAICCSessionStoreUnavailable = errors.New("aicc session store unavailable")

// ErrAICCConsentRequired 表示 AICC 公开会话要求访客先同意隐私说明。
var ErrAICCConsentRequired = errors.New("aicc consent required")

// ErrAICCLeadRequired 表示 AICC 公开会话存在未完成的必填留资字段。
var ErrAICCLeadRequired = errors.New("aicc lead required")

// ErrAICCOffline 表示公开访问的 AICC 智能体不存在、未启用或已下线。
var ErrAICCOffline = errors.New("aicc offline")

// ErrAICCInvalidSession 表示访客 session token 不存在或不可用。
var ErrAICCInvalidSession = errors.New("aicc invalid session")

// ErrAICCInvalidMessage 表示访客反馈引用的消息不存在、不可用或不是助手回复。
var ErrAICCInvalidMessage = errors.New("aicc invalid message")

// ErrAICCImageUnavailable 表示 AICC 图片对象存储未启用或不可用。
var ErrAICCImageUnavailable = errors.New("aicc image unavailable")

// ErrAICCDomainForbidden 表示网页挂件来源域名不在智能体允许列表内。
var ErrAICCDomainForbidden = errors.New("aicc domain forbidden")

// ErrAICCSensitiveWord 表示访客消息命中了智能体运营侧配置的敏感词拦截。
var ErrAICCSensitiveWord = errors.New("aicc sensitive word")

// ErrAICCMessageLimitExceeded 表示当前公开会话已达到运营侧配置的访客消息上限。
var ErrAICCMessageLimitExceeded = errors.New("aicc message limit exceeded")

// ErrAICCQueueBusy 表示全局持久化任务队列已满；必须在创建访客消息前返回 429，避免孤儿消息。
var ErrAICCQueueBusy = errors.New("aicc queue busy")

// ErrAICCConcurrencyLimited 表示任一运行时并发层级已满，任务应进入既有退避重试。
var ErrAICCConcurrencyLimited = errors.New("aicc concurrency limited")

// ErrAICCVisitorBlocked 表示当前访客命中了智能体有效封禁名单。
var ErrAICCVisitorBlocked = errors.New("aicc visitor blocked")

// ErrInvalidResourceRange 表示资源趋势查询的时间范围或聚合粒度非法，handler 层应映射为 400。
var ErrInvalidResourceRange = errors.New("资源查询范围不合法")

// 认证 -------------------------------------------------------------
// 来源：原 auth_service.go

// ErrInvalidCredentials 对外统一表示登录失败，避免泄露用户名是否存在或密码是否错误。
var ErrInvalidCredentials = errors.New("用户名或密码错误")

// ErrUserDisabled 表示用户状态为 disabled；登录、刷新和高风险运行操作都会拒绝。
var ErrUserDisabled = errors.New("用户已被禁用")

// ErrOrgDisabled 表示用户所属企业已停用；企业成员登录和刷新会被拒绝。
var ErrOrgDisabled = errors.New("企业已被禁用")

// ErrInvalidToken 表示 access / refresh token 格式、签名、过期或撤销状态无效。
var ErrInvalidToken = errors.New("登录凭证无效")

// 验证码（登录 PoW）---------------------------------------------------

// ErrCaptchaRequired 表示开启了验证码但请求未携带 payload，handler 映射为 400。
var ErrCaptchaRequired = errors.New("需要完成人机验证")

// ErrCaptchaInvalid 表示 Altcha 解验签失败、不成立或已过期，handler 映射为 400。
var ErrCaptchaInvalid = errors.New("人机验证失败")

// ErrCaptchaReplayed 表示该 Altcha 解已被消费（重放），handler 映射为 400。
var ErrCaptchaReplayed = errors.New("人机验证已被使用")

// 语言 -------------------------------------------------------------

// ErrInvalidLocale 表示请求的语言不在受支持集合内，handler 层映射为 400。
var ErrInvalidLocale = errors.New("不支持的语言")

// 成员 -------------------------------------------------------------
// 来源：原 member_service.go

// ErrMemberCreateInvalid 在创建成员的输入未通过业务校验时返回，handler 据此映射为 400。
var ErrMemberCreateInvalid = errors.New("成员资料不合法")

// ErrInstanceLimitReached 表示企业已达实例数量上限（organizations.max_instance_count），
// 不能再新建实例（app）。handler 层据此映射为 409 Conflict。
var ErrInstanceLimitReached = errors.New("已达企业实例数量上限")

// 渠道 -------------------------------------------------------------
// 来源：原 channel_service.go

// ErrChannelAdapterMissing 表示 service 调用时未注册对应渠道。
var ErrChannelAdapterMissing = errors.New("当前渠道未启用")

// ErrInstanceNotReady 表示实例当前未就绪（重启 / 版本升级 / 初始化中），pod 暂不可用，
// 此时发起渠道授权会打到不可达的 oc-ops，应稍候重试。handler 层映射为 409 Conflict。
var ErrInstanceNotReady = errors.New("实例尚未就绪，请稍候重试")

// 工作区 -----------------------------------------------------------
// 来源：原 workspace_service.go

// ErrWorkspaceForbidden 表示当前主体不能访问目标应用工作目录。
var ErrWorkspaceForbidden = errors.New("无权访问工作目录")

// ErrWorkspaceMissing 表示 workspace 数据源不可用（app 数据 object store 未配置，
// k8s 编排下即 storage.s3 未启用）。
var ErrWorkspaceMissing = errors.New("应用未关联节点或 adapter 未配置")

// ErrWorkspaceBadPath 表示请求路径越界或包含非法清理后的相对路径。
var ErrWorkspaceBadPath = errors.New("非法工作目录路径")

// 知识库 -----------------------------------------------------------
// 来源：原 knowledge_service.go

// ErrKnowledgeForbidden 表示当前主体无权访问组织或应用知识库。
var ErrKnowledgeForbidden = errors.New("无权访问该知识库")

// ErrKnowledgeMissing 表示知识库主副本或目标节点配置缺失。
var ErrKnowledgeMissing = errors.New("知识库主副本未配置")

// ErrKnowledgeDatasetCreating 表示 RAGFlow dataset 已由并发请求占位创建，当前请求应稍后重试。
var ErrKnowledgeDatasetCreating = errors.New("知识库正在初始化")

// ErrKnowledgeQuotaExceeded 表示知识库累计空间不足，handler 层据此映射为 409 Conflict。
var ErrKnowledgeQuotaExceeded = errors.New("知识库空间不足")

// ErrIndustryKnowledgeNotFound 表示行业知识库不存在或已删除。
var ErrIndustryKnowledgeNotFound = errors.New("行业知识库不存在")

// ErrIndustryKnowledgeNameTaken 表示未删除行业库中已存在同名记录。
var ErrIndustryKnowledgeNameTaken = errors.New("行业知识库名称已存在")

// ErrIndustryKnowledgeInUse 表示行业库仍被未删除助手版本引用，不能删除。
var ErrIndustryKnowledgeInUse = errors.New("行业知识库正在被助手版本引用")

// ErrIndustryKnowledgeUploadTokenInvalid 表示外部上传固定鉴权字符串缺失或错误。
var ErrIndustryKnowledgeUploadTokenInvalid = errors.New("行业知识库上传鉴权失败")

// 充值 -------------------------------------------------------------
// 来源：原 recharge_service.go

// ErrRechargeDenied 表示当前主体不能为目标组织充值。
var ErrRechargeDenied = errors.New("无权执行充值")

// ErrOrgMissingNewAPIUserID 表示企业尚未绑定 new-api 用户，无法充值或查余额。
var ErrOrgMissingNewAPIUserID = errors.New("企业缺少 new-api 用户 ID")

// ErrInvalidRechargeAmount 表示充值额度小于等于 0。
var ErrInvalidRechargeAmount = errors.New("充值金额必须为正")

// 运行节点 -----------------------------------------------------------
// 来源：原 runtime_node_service.go

// ErrAgentTokenInvalid 表示 agent 心跳令牌不存在或与节点不匹配。
var ErrAgentTokenInvalid = errors.New("agent token 无效")

// ErrEnrollInputInvalid 表示 agent enroll 请求缺少必填外部 ID 或密钥。
var ErrEnrollInputInvalid = errors.New("agent enroll 参数不合法")

// 应用 / 运行时操作 ------------------------------------------------
// 来源：原 runtime_operation_service.go

// ErrRuntimeOperationDenied 表示当前主体不能启动、停止、重启或删除目标应用容器。
var ErrRuntimeOperationDenied = errors.New("无权执行运行操作")

// ErrAppNotReinitializable 表示应用当前状态不允许重新初始化 runtime 资源。
var ErrAppNotReinitializable = errors.New("应用当前状态不允许重新初始化")

// 用量 -------------------------------------------------------------
// 来源：原 usage_service.go

// ErrUsageUnavailable 表示底层 new-api 不可用，用量数据暂时无法返回。
var ErrUsageUnavailable = errors.New("用量服务暂不可用")

// 任务看板 -----------------------------------------------------------

// ErrKanbanForbidden 表示当前 principal 无权访问该实例的 Kanban。
var ErrKanbanForbidden = errors.New("无权访问该实例任务看板")

// ErrKanbanRuntimeUnavailable 表示实例容器未运行，无法执行 kanban CLI。
var ErrKanbanRuntimeUnavailable = errors.New("实例容器未运行")

// ErrKanbanNotSupported 表示该实例运行的是 dev stub 镜像，不含真实 hermes。
var ErrKanbanNotSupported = errors.New("该实例镜像不支持任务看板")

// ErrKanbanCLI 表示 hermes kanban CLI 非零退出。
var ErrKanbanCLI = errors.New("kanban 命令执行失败")

// ErrKanbanOutputInvalid 表示 kanban CLI 输出不是合法 JSON。
var ErrKanbanOutputInvalid = errors.New("kanban 输出解析失败")

// ErrKanbanBadRequest 表示 kanban 请求参数非法（board slug / status / task id 等白名单校验失败）。
var ErrKanbanBadRequest = errors.New("kanban 请求参数非法")

// Cron --------------------------------------------------------------

// ErrCronForbidden 表示当前 principal 无权访问该实例的 Cron。
var ErrCronForbidden = errors.New("无权访问该实例 Cron")

// ErrCronRuntimeUnavailable 表示实例容器未运行，无法执行 cron CLI。
var ErrCronRuntimeUnavailable = errors.New("实例容器未运行")

// ErrCronNotSupported 表示该实例运行的镜像不支持 Hermes Cron。
var ErrCronNotSupported = errors.New("该实例镜像不支持 Cron")

// ErrCronCLI 表示 oc-cron 或底层 hermes cron 命令执行失败。
var ErrCronCLI = errors.New("cron 命令执行失败")

// ErrCronOutputInvalid 表示 oc-cron 输出不是合法契约信封或 data 结构不符合预期。
var ErrCronOutputInvalid = errors.New("cron 输出解析失败")

// ErrCronBadRequest 表示 Cron 请求参数非法（job id / script / output file 等白名单校验失败）。
var ErrCronBadRequest = errors.New("cron 请求参数非法")

// 会话 --------------------------------------------------------------
// 会话功能错误哨兵（语义对齐 kanban 同名错误，便于 handler 复用映射规则）。

// ErrConversationForbidden 表示当前 principal 无权访问该实例会话。
var ErrConversationForbidden = errors.New("无权访问该实例会话")

// ErrConversationNotSupported 表示该实例运行的镜像不支持会话功能。
var ErrConversationNotSupported = errors.New("当前实例不支持会话")

// ErrConversationRuntimeUnavailable 表示实例容器未运行，会话运行时尚未就绪。
var ErrConversationRuntimeUnavailable = errors.New("实例运行时尚未就绪")

// ErrConversationBadRequest 表示会话请求参数非法（conversation id / message 等校验失败）。
var ErrConversationBadRequest = errors.New("会话请求参数不合法")

// ErrConversationCLI 表示会话上游 CLI 或 API 调用非零退出或网络失败。
var ErrConversationCLI = errors.New("会话上游调用失败")

// ErrConversationOutputInvalid 表示会话上游返回内容不符合预期协议格式。
var ErrConversationOutputInvalid = errors.New("会话上游返回异常")

// 助手版本 -----------------------------------------------------------

// ErrAssistantVersionNotFound 助手版本不存在或已删除。
var ErrAssistantVersionNotFound = errors.New("助手版本不存在")

// ErrAssistantVersionDenied 无权操作助手版本。
var ErrAssistantVersionDenied = errors.New("无权操作助手版本")

// ErrAssistantVersionInvalid 助手版本入参非法（名称空、模型不存在、镜像 id 未知等）。
var ErrAssistantVersionInvalid = errors.New("助手版本入参非法")

// ErrAssistantVersionNameTaken 助手版本名称已被占用。
var ErrAssistantVersionNameTaken = errors.New("助手版本名称已存在")

// ErrAssistantVersionInUse 助手版本正被组织或实例引用，不可删除。
var ErrAssistantVersionInUse = errors.New("助手版本正被引用，不可删除")

// ErrAssistantVersionSkillNameTaken 同版本内 skill 名称已被占用，不允许重复添加。
var ErrAssistantVersionSkillNameTaken = errors.New("版本内 skill 名称已存在")

// ErrVersionNotInAllowlist 表示目标助手版本不在该实例所属企业的允许列表内，handler 映射为 400。
var ErrVersionNotInAllowlist = errors.New("助手版本不在企业允许列表内")

// ===== 平台库 skill =====

// ErrPlatformSkillNotFound 表示按 id 找不到平台库 skill。
var ErrPlatformSkillNotFound = errors.New("平台库 skill 不存在")

// ErrPlatformSkillDenied 表示当前主体无权管理平台库 skill。
var ErrPlatformSkillDenied = errors.New("无权管理平台库 skill")

// ErrPlatformSkillInvalid 表示上传入参非法（name/version/内容为空或路径段非法）。
var ErrPlatformSkillInvalid = errors.New("平台库 skill 入参非法")

// ErrPlatformSkillNameVersionTaken 表示同名同版本的平台库 skill 已存在。
var ErrPlatformSkillNameVersionTaken = errors.New("同名同版本的平台库 skill 已存在")

// ===== 实例 skill =====

// ErrAppSkillDenied 表示当前主体无权管理该实例的 skill。
var ErrAppSkillDenied = errors.New("无权管理该实例的 skill")

// ErrAppSkillNotFound 表示指定实例 skill 不存在。
var ErrAppSkillNotFound = errors.New("实例 skill 不存在")

// ErrAppSkillNameConflict 表示该实例下已有同名 skill，不允许重复安装。
var ErrAppSkillNameConflict = errors.New("已有同名 skill")

// ErrAppSkillProtected 表示该 skill 是当前助手版本必需的内置 skill，不可删除。
var ErrAppSkillProtected = errors.New("当前助手版本必需的 skill 不可删除")

// ErrAppSkillSourceUnknown 表示 skill 来源字段取值不在已知枚举范围内。
var ErrAppSkillSourceUnknown = errors.New("未知的 skill 来源")

// ErrAppSkillArchiveTooDangerous 表示 skill 归档解压校验失败（如路径穿越、内容篡改等）。
var ErrAppSkillArchiveTooDangerous = errors.New("skill 归档解压校验失败")

// ErrAppSkillRuntimeUnsupported 表示实例当前运行的 hermes 镜像版本过旧、不支持 skill 管理
// （oc-ops 无 /oc/skills 路由，查询返回 404）。区别于「pod 临时不可达」（网络错误），
// 用于提示用户更新实例的运行时版本。
var ErrAppSkillRuntimeUnsupported = errors.New("当前 hermes 运行时版本不支持 skill 管理")

// ===== skill 市场 =====

// ErrSkillMarketSourceUnknown 表示请求了未知的 skill 来源。
var ErrSkillMarketSourceUnknown = errors.New("未知的 skill 来源")

// ErrSkillMarketDenied 表示无权执行该市场操作（如下载归档需平台管理员）。
var ErrSkillMarketDenied = errors.New("无权执行该 skill 市场操作")

// ErrSkillMarketInvalid 表示市场操作入参非法（如下载缺少版本号/标识）。
var ErrSkillMarketInvalid = errors.New("skill 市场操作入参非法")

// ErrSkillMarketUpstreamUnavailable 表示上游第三方市场归档下载失败（非 2xx / 网络错误），
// 且本地缓存未命中、无法降级。映射为 502 Bad Gateway，与「manager 自身 500」语义区分开——
// 让前端能据此提示用户「上游暂时不可用、稍后重试」，而非误以为 manager 故障。
var ErrSkillMarketUpstreamUnavailable = errors.New("上游技能市场暂时不可用")

// ===== 定制技能工单 =====

// ErrSkillTicketNotFound 表示按 id 找不到工单。
var ErrSkillTicketNotFound = errors.New("定制技能工单不存在")

// ErrSkillTicketDenied 表示当前主体无权操作该工单。
var ErrSkillTicketDenied = errors.New("无权操作该定制技能工单")

// ErrSkillTicketInvalid 表示入参非法(标题/描述/消息为空,或非法状态动作/可见范围)。
var ErrSkillTicketInvalid = errors.New("定制技能工单入参非法")

// ===== 定制技能交付 / 取装 =====

// ErrCustomSkillDenied 表示无权交付定制技能(非平台管理员)。
var ErrCustomSkillDenied = errors.New("无权交付定制技能")

// ErrCustomSkillNameMismatch 表示再次交付时归档技能名与工单已锁定的 name 不一致。
var ErrCustomSkillNameMismatch = errors.New("迭代交付必须沿用同一技能名")

// ErrCustomSkillInvalid 表示交付入参非法(空归档/无目标范围/归档不合扁平契约)。
var ErrCustomSkillInvalid = errors.New("定制技能交付入参非法")

// ErrCustomSkillNotFound 表示按 name(+version)找不到定制技能。
var ErrCustomSkillNotFound = errors.New("定制技能不存在")
