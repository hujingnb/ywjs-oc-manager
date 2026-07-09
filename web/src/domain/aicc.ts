// AICC 前端领域类型保持与后端 JSON 契约一致，页面层不需要在 snake_case/camelCase 间转换。

// AICCAgentStatus 是智能体生命周期状态。
export type AICCAgentStatus = 'draft' | 'active' | 'paused' | 'deleted'

// AICCPrivacyMode 是访客隐私提示模式。
export type AICCPrivacyMode = 'notice' | 'consent_required'

// AICCAgent 是管理面展示和编辑 AICC 智能体所需的基础视图。
export interface AICCAgent {
  // 智能体主键。
  id: string
  // 所属企业 ID。
  org_id: string
  // 绑定的隐藏 app ID。
  app_id: string
  // 智能体展示名。
  name: string
  // 智能体状态。
  status: AICCAgentStatus
  // 业务场景说明。
  scenario?: string
  // 访客欢迎语。
  greeting?: string
  // 回答边界说明。
  answer_boundary?: string
  // 隐私提示模式。
  privacy_mode: AICCPrivacyMode
  // 隐私说明文本。
  privacy_text?: string
  // 数据保留天数。
  retention_days: number
  // 公开链接 token。
  public_token?: string
  // 嵌入组件 token。
  widget_token?: string
  // 创建时间。
  created_at?: string
  // 更新时间。
  updated_at?: string
}

// AICCAgentPayload 是创建和更新智能体时提交给后端的表单载荷。
export interface AICCAgentPayload {
  // 智能体展示名。
  name: string
  // 业务场景说明。
  scenario?: string
  // 访客欢迎语。
  greeting?: string
  // 回答边界说明。
  answer_boundary?: string
  // 隐私提示模式。
  privacy_mode?: AICCPrivacyMode
  // 隐私说明文本。
  privacy_text?: string
  // 数据保留天数；0 或缺省由后端使用默认值。
  retention_days?: number
}

// isAICCAgentRunning 判断智能体是否处于可对外接待状态。
export function isAICCAgentRunning(agent: Pick<AICCAgent, 'status'>): boolean {
  return agent.status === 'active'
}
