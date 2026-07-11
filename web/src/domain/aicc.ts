// AICC 前端领域类型保持与后端 JSON 契约一致，页面层不需要在 snake_case/camelCase 间转换。

// AICCAgentStatus 是智能体生命周期状态。
export type AICCAgentStatus = 'draft' | 'active' | 'paused' | 'deleted'

// AICCPrivacyMode 是访客隐私提示模式。
export type AICCPrivacyMode = 'notice' | 'consent_required'

// AICCPublicChannel 是公开访客入口渠道；前端当前只允许链接和网页挂件两种。
export type AICCPublicChannel = 'web_link' | 'web_widget'

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
  // 允许加载网页挂件的域名列表；为空表示不限制。
  allowed_domains?: string[]
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
  // 允许加载网页挂件的域名列表；为空表示不限制。
  allowed_domains?: string[]
}

// AICCLeadField 是公开页留资字段配置，管理端编辑和访客端渲染共用。
export interface AICCLeadField {
  // 字段 ID；公开页仅用于稳定渲染。
  id?: string
  // 字段稳定 key，提交留资值时使用。
  field_key: string
  // 公开页展示名称。
  label: string
  // 输入类型。
  field_type: 'text' | 'phone' | 'email' | 'number'
  // 是否必填。
  required: boolean
  // 输入提示。
  prompt_text?: string
  // 展示顺序。
  sort_order?: number
}

// AICCLeadFieldPayload 是管理端保存留资字段时提交的载荷。
export type AICCLeadFieldPayload = Omit<AICCLeadField, 'id'>

// AICCAgentSettings 是单个智能体的运营安全配置回显。
export interface AICCAgentSettings {
  // 智能体主键。
  agent_id: string
  // 单个公开会话允许访客发送的最大消息数。
  message_limit_per_session: number
  // 公开端发送前拦截的敏感词列表。
  sensitive_words: string[]
  // 是否启用异常访客封禁检查。
  blocked_visitor_enabled: boolean
  // 异常访客自动封禁阈值配置；当前后端按 JSON 对象透传。
  blocked_visitor_threshold_json?: Record<string, unknown>
  // 公开页刷新后允许续接原会话的分钟数。
  session_resume_ttl_minutes: number
  // 当前有效封禁访客数量，仅用于运营面板展示。
  blocked_visitor_count?: number
}

// AICCAgentSettingsPayload 是保存运营安全配置时提交的载荷。
export interface AICCAgentSettingsPayload {
  // 单个公开会话允许访客发送的最大消息数。
  message_limit_per_session: number
  // 公开端发送前拦截的敏感词列表。
  sensitive_words: string[]
  // 是否启用异常访客封禁检查。
  blocked_visitor_enabled: boolean
  // 异常访客自动封禁阈值配置；未配置时由后端保留默认规则。
  blocked_visitor_threshold_json?: Record<string, unknown>
  // 公开页刷新后允许续接原会话的分钟数。
  session_resume_ttl_minutes: number
}

// AICCKnowledge 是智能体可检索知识范围配置。
export interface AICCKnowledge {
  // 智能体主键。
  agent_id: string
  // 绑定隐藏 app ID，用于跳转当前客服的知识库。
  app_id: string
  // 是否检索企业共享知识库。
  use_org_knowledge: boolean
  // 平台行业知识库 ID 列表。
  industry_knowledge_base_ids: string[]
  // 保留旧接口字段兼容；当前客服的知识库默认启用，不再按文档勾选。
  app_document_ids: string[]
}

// AICCKnowledgePayload 是保存知识范围时提交的完整快照。
export type AICCKnowledgePayload = Pick<AICCKnowledge, 'use_org_knowledge' | 'industry_knowledge_base_ids'>

// AICCKnowledgeOption 是 AICC 知识范围下拉框候选项。
export interface AICCKnowledgeOption {
  // 候选项主键，保存时写入行业库 ID。
  id: string
  // 用户可见名称。
  name: string
  // 行业库文档数；按版本授权查询时可能返回 0。
  document_count: number
}

// AICCKnowledgeOptions 是 AICC 管理页只读候选项集合。
export interface AICCKnowledgeOptions {
  // 平台行业知识库候选项。
  industry_knowledge_bases: AICCKnowledgeOption[]
  // 保留旧接口字段兼容；当前客服的知识库不再按文档勾选。
  app_documents: AICCKnowledgeOption[]
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
  // 公开页留资字段配置。
  lead_fields?: AICCLeadField[]
}

// AICCPublicSession 是公开访客会话的临时凭证和隐私状态。
export interface AICCPublicSession {
  // 会话短期 token；公开页按 publicToken + channel 持久化，用于刷新后续接。
  session_token?: string
  // 是否由服务端根据提交的旧 token 恢复既有会话。
  restored?: boolean
  // 隐私提示模式。
  privacy_mode?: AICCPrivacyMode
  // 隐私说明文本。
  privacy_text?: string
  // 本会话是否已经展示隐私提示。
  privacy_notice_shown?: boolean
}

// AICCPublicSessionDetail 是公开访客刷新页面后按 session token 恢复的会话内容。
export interface AICCPublicSessionDetail {
  // 当前会话已保存的消息。
  messages: AICCMessage[]
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

// AICCPublicLeadValuesResult 是访客提交留资后的状态响应。
export interface AICCPublicLeadValuesResult {
  // 当前会话留资状态。
  lead_status: 'pending' | 'complete' | 'skipped' | string
  // 仍缺失的必填字段 key。
  missing_required_keys?: string[]
}

// AICCSession 是企业管理员查看访客会话列表时的摘要信息。
export interface AICCSession {
  // 会话主键。
  id: string
  // 所属智能体 ID。
  agent_id: string
  // 当前会话接待渠道。
  channel?: string
  // 公开端解析出的访客地域。
  region?: string
  // 访客来源页面。
  source_url?: string
  // 会话消息数量，用于运营快速判断沟通深度。
  message_count?: number
  // 当前会话解决状态。
  resolution_status?: string
  // 当前会话留资状态。
  lead_status?: string
  // 最近活跃时间。
  last_active_at?: string
  // 会话创建时间。
  created_at?: string
  // 最近更新时间。
  updated_at?: string
}

// AICCSessionFilters 是会话列表的轻量筛选条件。
export interface AICCSessionFilters {
  // 解决状态。
  resolution_status?: string
  // 留资状态。
  lead_status?: string
  // 入口渠道。
  channel?: string
  // 访客地域。
  region?: string
  // 创建时间下界，RFC3339 字符串。
  start_at?: string
  // 创建时间上界，RFC3339 字符串。
  end_at?: string
  // 来源关键词。
  keyword?: string
}

// AICCMessage 是单个会话中的访客或助手消息。
export interface AICCMessage {
  // 消息主键。
  id: string
  // 消息方向。
  direction?: 'visitor' | 'assistant' | 'system' | string
  // 内容类型。
  content_type?: string
  // 文本内容。
  text?: string
  // 图片对象 key；存在时说明消息带图片。
  image_object_key?: string
  // 图片 MIME。
  image_mime?: string
  // 图片大小。
  image_size_bytes?: number
  // 是否为兜底回答。
  is_fallback?: boolean
  // 是否为拒答。
  is_refusal?: boolean
  // 运行时错误摘要。
  error_summary?: string
  // 消息创建时间。
  created_at?: string
}

// AICCSessionDetail 是会话详情与消息明细的组合视图。
export interface AICCSessionDetail {
  // 会话摘要。
  session: AICCSession
  // 本会话已提交的留资字段值。
  lead_values?: AICCLeadValue[]
  // 会话消息列表。
  messages: AICCMessage[]
}

// AICCLeadValue 是访客提交的单个自定义留资字段值。
export interface AICCLeadValue {
  // 字段主键。
  field_id?: string
  // 字段稳定 key。
  field_key: string
  // 字段展示名称。
  label: string
  // 字段类型。
  field_type?: string
  // 字段值。
  value: string
  // 创建时间。
  created_at?: string
}

// AICCLead 是从会话中沉淀出的访客线索。
export interface AICCLead {
  // 线索主键。
  id: string
  // 最近关联会话 ID。
  latest_session_id?: string
  // 访客展示名或联系方式摘要。
  display_name?: string
  // 是否未读。
  unread: boolean
  // 自定义留资字段值。
  values?: AICCLeadValue[]
  // 创建时间。
  created_at?: string
  // 最近更新时间。
  updated_at?: string
}

// AICCAnalytics 是 AICC 运营看板的轻量统计。
export interface AICCAnalytics {
  // 今日新增会话数。
  today_sessions: number
  // 当前筛选窗口内的会话总数。
  total_sessions?: number
  // 未读线索数。
  unread_leads: number
  // 已解决会话数。
  resolved_sessions?: number
  // 未解决会话数。
  unresolved_sessions?: number
  // 尚未判定解决状态的会话数。
  unknown_sessions?: number
  // 未解决会话在已判定会话中的占比。
  unresolved_rate?: number
  // 已完成留资的会话数。
  completed_lead_sessions?: number
  // 按日或周聚合的会话趋势。
  session_trend?: AICCTrendBucket[]
  // 访客地域分布。
  regions?: AICCTopItem[]
  // 访客热门问题。
  top_questions?: AICCTopItem[]
  // 访客来源页面分布。
  top_sources?: AICCTopItem[]
}

// AICCAnalyticsFilters 是统计页筛选条件。
export interface AICCAnalyticsFilters {
  // 统计开始时间，RFC3339 字符串。
  start_at?: string
  // 统计结束时间，RFC3339 字符串。
  end_at?: string
  // 趋势聚合粒度。
  bucket?: 'day' | 'week'
  // 限定单个智能体。
  agent_id?: string
}

// AICCTrendBucket 是统计趋势中的单个时间桶。
export interface AICCTrendBucket {
  // 后端返回的日期或周标签。
  bucket: string
  // 该时间桶内的会话数量。
  count: number
}

// AICCTopItem 是统计页中带次数的排行项。
export interface AICCTopItem {
  // 展示文本。
  label: string
  // 出现次数。
  count: number
}

// isAICCAgentRunning 判断智能体是否处于可对外接待状态。
export function isAICCAgentRunning(agent: Pick<AICCAgent, 'status'>): boolean {
  return agent.status === 'active'
}

// normalizeAICCPublicChannel 把路由 query 中的渠道归一化，避免未知值触发后端 CHECK 约束。
export function normalizeAICCPublicChannel(value: unknown): AICCPublicChannel {
  return value === 'web_widget' ? 'web_widget' : 'web_link'
}
