// Package service 的所有可被 handler 层 errors.Is 检查的 sentinel error。
// 业务模块需要新增错误时优先扩展本文件，不要在各 service 文件内再定义本地 sentinel。
package service

import "errors"

// 通用错误 ----------------------------------------------------------

var ErrForbidden = errors.New("无权执行该操作")
var ErrNotFound = errors.New("资源不存在")

// ErrConflict 表示资源冲突，如唯一性约束冲突或状态冲突。
var ErrConflict = errors.New("资源冲突")

// 节点 -------------------------------------------------------------

// ErrNoNodeAvailable 表示当前没有「active 且剩余容量 > 0」的节点可分配新应用。
// 由 OnboardingService 在自动选节点失败时返回；handler 层映射为 503 + NO_NODE_AVAILABLE。
var ErrNoNodeAvailable = errors.New("当前无可用 runtime 节点")

// 认证 -------------------------------------------------------------
// 来源：原 auth_service.go

// ErrInvalidCredentials 对外统一表示登录失败，避免泄露用户名是否存在或密码是否错误。
var ErrInvalidCredentials = errors.New("用户名或密码错误")
var ErrUserDisabled = errors.New("用户已被禁用")
var ErrOrgDisabled = errors.New("组织已被禁用")
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

var ErrWorkspaceForbidden = errors.New("无权访问工作目录")
var ErrWorkspaceMissing = errors.New("应用未关联节点或 adapter 未配置")
var ErrWorkspaceBadPath = errors.New("非法工作目录路径")

// 人设 -------------------------------------------------------------
// 来源：原 persona_service.go

var ErrPersonaNotFound = errors.New("组织尚未配置人设")
var ErrPersonaDenied = errors.New("无权访问该组织人设")

// 知识库 -----------------------------------------------------------
// 来源：原 knowledge_service.go

var ErrKnowledgeForbidden = errors.New("无权访问该知识库")
var ErrKnowledgeMissing = errors.New("知识库主副本未配置")

// 充值 -------------------------------------------------------------
// 来源：原 recharge_service.go

var ErrRechargeDenied = errors.New("无权执行充值")
var ErrOrgMissingNewAPIUserID = errors.New("组织缺少 new-api 用户 ID")
var ErrInvalidRechargeAmount = errors.New("充值金额必须为正")

// Runtime Node -----------------------------------------------------
// 来源：原 runtime_node_service.go

var ErrAgentTokenInvalid = errors.New("agent token 无效")
var ErrEnrollInputInvalid = errors.New("agent enroll 参数不合法")

// 应用 / 运行时操作 ------------------------------------------------
// 来源：原 runtime_operation_service.go

var ErrRuntimeOperationDenied = errors.New("无权执行运行操作")
var ErrAppNotReinitializable = errors.New("应用当前状态不允许重新初始化")

// 用量 -------------------------------------------------------------
// 来源：原 usage_service.go

// ErrUsageUnavailable 表示底层 new-api 不可用，用量数据暂时无法返回。
var ErrUsageUnavailable = errors.New("用量服务暂不可用")
