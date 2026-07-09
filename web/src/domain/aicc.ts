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

// AICCPublicConfig 是访客打开公开链接后可读取的非敏感展示配置。
export interface AICCPublicConfig {
  // 智能体公开展示名。
  name?: string
  // 访客欢迎语。
  greeting?: string
  // 隐私提示模式。
  privacy_mode?: AICCPrivacyMode
  // 隐私说明文本。
  privacy_text?: string
  // 数据保留天数。
  retention_days?: number
}

// AICCPublicSession 是公开访客会话的临时凭证和隐私状态。
export interface AICCPublicSession {
  // 会话短期 token；只保存在当前浏览器页面内。
  session_token?: string
  // 隐私提示模式。
  privacy_mode?: AICCPrivacyMode
  // 隐私说明文本。
  privacy_text?: string
  // 本会话是否已经展示隐私提示。
  privacy_notice_shown?: boolean
}

// AICCPublicMessageResult 是公开消息接口返回的助手回复。
export interface AICCPublicMessageResult {
  // 消息 ID，用于后续反馈绑定。
  message_id?: string
  // 助手回复文本。
  text?: string
}

// AICCPublicImageResult 是公开图片上传后返回的文件引用。
export interface AICCPublicImageResult {
  // 图片文件 ID；发送消息时作为 image_file_id 提交。
  image_file_id?: string
  // 服务端识别出的 MIME。
  mime?: string
  // 图片大小，单位字节。
  size?: number
}

// isAICCAgentRunning 判断智能体是否处于可对外接待状态。
export function isAICCAgentRunning(agent: Pick<AICCAgent, 'status'>): boolean {
  return agent.status === 'active'
}
