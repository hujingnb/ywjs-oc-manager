// 对外暴露的业务类型 alias，从 openapi-typescript 生成的 schema 派生。
// 各 hook 文件应 `import type { Member } from '@/api'` 而非直接引用 generated.ts，
// 让 schema 重命名 / 拆分对调用方透明。
import type { components } from './generated'

// 缩写访问 schemas 节
type Schemas = components['schemas']

// 辅助工具：Required<T> 仅对指定键，其余保持原始可选性。
// 用于把 openapi-typescript 生成的"全部可选"schema 中确实在运行时必须存在的字段标记为必须，
// 与原手写 types.ts 保持语义兼容，避免调用方大量出现 `!` 或可选链。
type WithRequired<T, K extends keyof T> = T & { [P in K]-?: T[P] }

// ===== 鉴权 =====
// AuthUser：id / username / display_name / role / status 在登录后 API 必然返回
export type AuthUser = WithRequired<Schemas['service.AuthUser'], 'id' | 'username' | 'display_name' | 'role' | 'status'>

// TokenPair：access_token / refresh_token 在登录响应中必然返回
export type TokenPair = WithRequired<Schemas['service.TokenPair'], 'access_token' | 'refresh_token'>

// LoginResult：tokens / user 在登录响应中必然返回；内部类型也被收紧
export type LoginResult = {
  tokens: TokenPair
  user: AuthUser
}

// ===== 业务对象 =====
// swag 生成的 schema key 含包前缀（如 'service.MemberResult'）

// Member：id / username / display_name / role / status 后端必返
export type Member = WithRequired<
  Schemas['service.MemberResult'],
  'id' | 'username' | 'display_name' | 'role' | 'status'
>

// Organization：id / name / status / code / model_id 后端必返
export type Organization = WithRequired<
  Schemas['service.OrganizationResult'],
  'id' | 'name' | 'status' | 'code' | 'model_id'
>

// App：id / name / status / persona_mode / api_key_status 后端必返
export type App = WithRequired<
  Schemas['service.AppResult'],
  'id' | 'name' | 'status' | 'persona_mode' | 'api_key_status'
>

// RuntimeNode：id / name / status / heartbeat_interval_seconds / has_agent_token 后端必返
export type RuntimeNode = WithRequired<
  Schemas['service.RuntimeNodeResult'],
  'id' | 'name' | 'status' | 'heartbeat_interval_seconds' | 'has_agent_token'
>

// AuditLog：id / actor_role / target_type / target_id / action / result / created_at 后端必返，
// *_label 为对应字段的中文展示名，后端同步填充。
export type AuditLog = WithRequired<
  Schemas['service.AuditResult'],
  | 'id' | 'actor_role' | 'target_type' | 'target_id' | 'action' | 'result' | 'created_at'
  | 'action_label' | 'target_type_label' | 'actor_role_label' | 'result_label'
>

// ===== 手工补的类型（来自工具链限制） =====
// service.LogsPage.items 与 service.QuotaSeries.items 因 swag v2 rc5 扫描外部 newapi 包
// 存在 nil panic 风险，两个字段被加了 swaggerignore:"true"。
// yaml 中 LogsPage 只有 scope/scope_id/total/updated_at，QuotaSeries 只有 scope/scope_id/updated_at。
// 这里手工补 LogEntry / QuotaDate 接口并 intersect 回 items 字段。
// FIXME: 等 swag v2 修复跨包扫描后移除这段，改为直接从 generated.ts 派生。

// LogEntry 字段按 Go 侧 internal/integrations/newapi/client.go 中定义对齐。
// FIXME: 等 swag v2 修复跨包扫描后，从 components['schemas']['service.LogsPage'].items 派生即可移除手补。
export interface LogEntry {
  // new-api 日志 ID。
  id: number
  // new-api 用户 ID。
  user_id: number
  // new-api 用户名。
  username: string
  // new-api token ID。
  token_id: number
  // token 名称。
  token_name: string
  // 模型名称。
  model_name: string
  // 本次调用消耗额度。
  quota: number
  // prompt token 数。
  prompt_tokens: number
  // completion token 数。
  completion_tokens: number
  // 调用耗时。
  use_time: number
  // new-api 创建时间戳。
  created_at: number
}

// QuotaDate 字段按 Go 侧 internal/integrations/newapi/client.go 中定义对齐。
// FIXME: 等 swag v2 修复跨包扫描后，从 components['schemas']['service.QuotaSeries'].items 派生即可移除手补。
export interface QuotaDate {
  // 聚合日期。
  date: string
  // new-api v1.0.2 可能仅返回创建时间，manager 会据此补齐 date。
  created_at?: number
  // 模型名称。
  model_name: string
  // 调用次数。
  count: number
  // 消耗额度。
  quota: number
  // token 用量。
  token_used: number
}

type RawLogsPage = Schemas['service.LogsPage']
type RawQuotaSeries = Schemas['service.QuotaSeries']

// LogsPage 是 app / member 维度用量查询的响应类型（items 为 LogEntry 数组）。
export type LogsPage = RawLogsPage & { items: LogEntry[] }

// QuotaSeries 是 org / platform 维度用量查询的响应类型（items 为 QuotaDate 数组）。
export type QuotaSeries = RawQuotaSeries & { items: QuotaDate[] }

// 重导出 generated.ts（hook 直接用 paths 类型时引用）
export type { paths, components } from './generated'
