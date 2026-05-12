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

// ErrInvalidResourceRange 表示资源趋势查询的时间范围或聚合粒度非法，handler 层应映射为 400。
var ErrInvalidResourceRange = errors.New("资源查询范围不合法")

// 节点 -------------------------------------------------------------

// ErrNoNodeAvailable 表示当前没有「active 且剩余容量 > 0」的节点可分配新应用。
// 由 OnboardingService 在自动选节点失败时返回；handler 层映射为 503 + NO_NODE_AVAILABLE。
var ErrNoNodeAvailable = errors.New("当前无可用 runtime 节点")

// 认证 -------------------------------------------------------------
// 来源：原 auth_service.go

// ErrInvalidCredentials 对外统一表示登录失败，避免泄露用户名是否存在或密码是否错误。
var ErrInvalidCredentials = errors.New("用户名或密码错误")

// ErrUserDisabled 表示用户状态为 disabled；登录、刷新和高风险运行操作都会拒绝。
var ErrUserDisabled = errors.New("用户已被禁用")

// ErrOrgDisabled 表示用户所属组织已停用；组织成员登录和刷新会被拒绝。
var ErrOrgDisabled = errors.New("组织已被禁用")

// ErrInvalidToken 表示 access / refresh token 格式、签名、过期或撤销状态无效。
var ErrInvalidToken = errors.New("登录凭证无效")

// 成员 -------------------------------------------------------------
// 来源：原 member_service.go

// ErrMemberCreateInvalid 在创建成员的输入未通过业务校验时返回，handler 据此映射为 400。
var ErrMemberCreateInvalid = errors.New("成员资料不合法")

// 渠道 -------------------------------------------------------------
// 来源：原 channel_service.go

// ErrChannelAdapterMissing 表示 service 调用时未注册对应渠道。
var ErrChannelAdapterMissing = errors.New("当前渠道未启用")

// 工作区 -----------------------------------------------------------
// 来源：原 workspace_service.go

// ErrWorkspaceForbidden 表示当前主体不能访问目标应用工作目录。
var ErrWorkspaceForbidden = errors.New("无权访问工作目录")

// ErrWorkspaceMissing 表示应用缺少节点绑定或 runtime adapter，文件代理无法定位目录。
var ErrWorkspaceMissing = errors.New("应用未关联节点或 adapter 未配置")

// ErrWorkspaceBadPath 表示请求路径越界或包含非法清理后的相对路径。
var ErrWorkspaceBadPath = errors.New("非法工作目录路径")

// 人设 -------------------------------------------------------------
// 来源：原 persona_service.go

// ErrPersonaNotFound 表示组织还没有写入过默认人设。
var ErrPersonaNotFound = errors.New("组织尚未配置人设")

// ErrPersonaDenied 表示当前主体无权读写目标组织人设。
var ErrPersonaDenied = errors.New("无权访问该组织人设")

// 知识库 -----------------------------------------------------------
// 来源：原 knowledge_service.go

// ErrKnowledgeForbidden 表示当前主体无权访问组织或应用知识库。
var ErrKnowledgeForbidden = errors.New("无权访问该知识库")

// ErrKnowledgeMissing 表示知识库主副本或目标节点配置缺失。
var ErrKnowledgeMissing = errors.New("知识库主副本未配置")

// 充值 -------------------------------------------------------------
// 来源：原 recharge_service.go

// ErrRechargeDenied 表示当前主体不能为目标组织充值。
var ErrRechargeDenied = errors.New("无权执行充值")

// ErrOrgMissingNewAPIUserID 表示组织尚未绑定 new-api 用户，无法充值或查余额。
var ErrOrgMissingNewAPIUserID = errors.New("组织缺少 new-api 用户 ID")

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
