// AICC API hooks 只封装请求、缓存键和失效范围；页面展示逻辑放在 pages/components 中。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'
import { computed } from 'vue'

import { apiDownload, apiRequest, getCsrfToken } from '@/api/client'
import type {
  AICCAgent,
  AICCAgentPayload,
  AICCAgentSettings,
  AICCAgentSettingsPayload,
  AICCAnalytics,
  AICCAnalyticsFilters,
  AICCLead,
  AICCLeadField,
  AICCLeadFieldPayload,
  AICCKnowledge,
  AICCKnowledgeOptions,
  AICCKnowledgePayload,
  AICCPublicConfig,
  AICCPublicChannel,
  AICCPublicImageResult,
  AICCPublicLeadValuesResult,
  AICCPublicMessageResult,
  AICCPublicResolutionResult,
  AICCPublicSession,
  AICCPublicSessionDetail,
  AICCSession,
  AICCSessionFilters,
  AICCSessionDetail,
} from '@/domain/aicc'

const AICC_AGENTS_KEY = ['aicc', 'agents'] as const
const aiccAgentsKey = (orgId?: string) => [...AICC_AGENTS_KEY, orgId ?? 'current'] as const
const aiccAgentKey = (agentId?: string) => ['aicc', 'agent', agentId] as const
const aiccSettingsKey = (agentId?: string) => ['aicc', 'settings', agentId] as const
const aiccLeadFieldsKey = (agentId?: string) => ['aicc', 'lead-fields', agentId] as const
const aiccKnowledgeKey = (agentId?: string) => ['aicc', 'knowledge', agentId] as const
const aiccKnowledgeOptionsKey = (agentId?: string) => ['aicc', 'knowledge-options', agentId] as const
const aiccSessionsKey = (agentId?: string, filters?: AICCSessionFilters) => ['aicc', 'sessions', agentId, filters ?? {}] as const
const aiccSessionKey = (sessionId?: string) => ['aicc', 'session', sessionId] as const
const AICC_LEADS_KEY = ['aicc', 'leads'] as const
const AICC_ANALYTICS_KEY = ['aicc', 'analytics'] as const
const aiccAnalyticsKey = (filters?: AICCAnalyticsFilters) => [...AICC_ANALYTICS_KEY, filters ?? {}] as const

// useAICCAgentsQuery 查询 AICC 智能体列表；orgId 为空时后端按当前企业管理员所属企业处理。
export function useAICCAgentsQuery(orgId?: Ref<string | undefined>, enabled?: () => boolean) {
  return useQuery<AICCAgent[]>({
    queryKey: computed(() => aiccAgentsKey(orgId?.value)),
    enabled,
    queryFn: async () => {
      const query = orgId?.value ? { org_id: orgId.value, limit: 200 } : { limit: 200 }
      const response = await apiRequest<{ agents: AICCAgent[] }>('/api/v1/aicc/agents', { query })
      return response.agents
    },
  })
}

// useAICCAgentQuery 查询单个 AICC 智能体详情。
export function useAICCAgentQuery(agentId: Ref<string | undefined>) {
  return useQuery<AICCAgent | null>({
    queryKey: computed(() => aiccAgentKey(agentId.value)),
    enabled: () => Boolean(agentId.value),
    queryFn: async () => {
      if (!agentId.value) return null
      const response = await apiRequest<{ agent: AICCAgent }>(`/api/v1/aicc/agents/${agentId.value}`)
      return response.agent
    },
  })
}

// useCreateAICCAgent 创建智能体，成功后刷新列表缓存。
export function useCreateAICCAgent() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (payload: AICCAgentPayload) => {
      const response = await apiRequest<{ agent: AICCAgent }>('/api/v1/aicc/agents', {
        method: 'POST',
        body: payload,
      })
      return response.agent
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: AICC_AGENTS_KEY })
    },
  })
}

// useUpdateAICCAgent 更新智能体资料，成功后刷新详情与列表缓存。
export function useUpdateAICCAgent() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ agentId, payload }: { agentId: string; payload: AICCAgentPayload }) => {
      const response = await apiRequest<{ agent: AICCAgent }>(`/api/v1/aicc/agents/${agentId}`, {
        method: 'PATCH',
        body: payload,
      })
      return response.agent
    },
    onSuccess: (agent) => {
      void client.invalidateQueries({ queryKey: AICC_AGENTS_KEY })
      void client.invalidateQueries({ queryKey: aiccAgentKey(agent.id) })
    },
  })
}

// useSetAICCAgentStatus 启动或停止智能体。
export function useSetAICCAgentStatus() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ agentId, action }: { agentId: string; action: 'start' | 'stop' }) => {
      const response = await apiRequest<{ agent: AICCAgent }>(`/api/v1/aicc/agents/${agentId}/${action}`, {
        method: 'POST',
      })
      return response.agent
    },
    onSuccess: (agent) => {
      void client.invalidateQueries({ queryKey: AICC_AGENTS_KEY })
      void client.invalidateQueries({ queryKey: aiccAgentKey(agent.id) })
    },
  })
}

// useDeleteAICCAgent 软删除智能体，成功后刷新列表并移除详情缓存。
export function useDeleteAICCAgent() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (agentId: string) => {
      await apiRequest<void>(`/api/v1/aicc/agents/${agentId}`, { method: 'DELETE' })
      return agentId
    },
    onSuccess: (agentId) => {
      void client.invalidateQueries({ queryKey: AICC_AGENTS_KEY })
      void client.removeQueries({ queryKey: aiccAgentKey(agentId) })
    },
  })
}

// useAICCSettingsQuery 查询选中智能体的运营安全配置；旧智能体无 settings 行时由后端返回默认值。
export function useAICCSettingsQuery(agentId: Ref<string | undefined>) {
  return useQuery<AICCAgentSettings | null>({
    queryKey: computed(() => aiccSettingsKey(agentId.value)),
    enabled: () => Boolean(agentId.value),
    queryFn: async () => {
      if (!agentId.value) return null
      const response = await apiRequest<{ settings: AICCAgentSettings }>(`/api/v1/aicc/agents/${agentId.value}/settings`)
      return response.settings
    },
  })
}

// useUpdateAICCSettings 保存智能体运营安全配置，成功后刷新当前智能体 settings 缓存。
export function useUpdateAICCSettings() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ agentId, payload }: { agentId: string; payload: AICCAgentSettingsPayload }) => {
      const response = await apiRequest<{ settings: AICCAgentSettings }>(`/api/v1/aicc/agents/${agentId}/settings`, {
        method: 'PUT',
        body: payload,
      })
      return response.settings
    },
    onSuccess: (_settings, vars) => {
      void client.invalidateQueries({ queryKey: aiccSettingsKey(vars.agentId) })
    },
  })
}

// useAICCLeadFieldsQuery 查询选中智能体的公开页留资字段配置。
export function useAICCLeadFieldsQuery(agentId: Ref<string | undefined>) {
  return useQuery<AICCLeadField[]>({
    queryKey: computed(() => aiccLeadFieldsKey(agentId.value)),
    enabled: () => Boolean(agentId.value),
    queryFn: async () => {
      if (!agentId.value) return []
      const response = await apiRequest<{ fields: AICCLeadField[] }>(`/api/v1/aicc/agents/${agentId.value}/lead-fields`)
      return response.fields
    },
  })
}

// useReplaceAICCLeadFields 整组保存公开页留资字段配置。
export function useReplaceAICCLeadFields() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ agentId, fields }: { agentId: string; fields: AICCLeadFieldPayload[] }) => {
      const response = await apiRequest<{ fields: AICCLeadField[] }>(`/api/v1/aicc/agents/${agentId}/lead-fields`, {
        method: 'PUT',
        body: { fields },
      })
      return response.fields
    },
    onSuccess: (_fields, vars) => {
      void client.invalidateQueries({ queryKey: aiccLeadFieldsKey(vars.agentId) })
    },
  })
}

// useAICCKnowledgeQuery 查询选中智能体的知识库挂载范围。
export function useAICCKnowledgeQuery(agentId: Ref<string | undefined>) {
  return useQuery<AICCKnowledge | null>({
    queryKey: computed(() => aiccKnowledgeKey(agentId.value)),
    enabled: () => Boolean(agentId.value),
    queryFn: async () => {
      if (!agentId.value) return null
      const response = await apiRequest<{ knowledge: AICCKnowledge }>(`/api/v1/aicc/agents/${agentId.value}/knowledge`)
      return response.knowledge
    },
  })
}

// useAICCKnowledgeOptionsQuery 查询 AICC 知识范围配置页的企业授权行业库候选项。
export function useAICCKnowledgeOptionsQuery(agentId: Ref<string | undefined>) {
  return useQuery<AICCKnowledgeOptions | null>({
    queryKey: computed(() => aiccKnowledgeOptionsKey(agentId.value)),
    enabled: () => Boolean(agentId.value),
    queryFn: async () => {
      if (!agentId.value) return null
      const response = await apiRequest<{ options: AICCKnowledgeOptions }>(`/api/v1/aicc/agents/${agentId.value}/knowledge-options`)
      return response.options
    },
  })
}

// useReplaceAICCKnowledge 整组保存智能体可检索的企业和行业知识范围。
export function useReplaceAICCKnowledge() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ agentId, payload }: { agentId: string; payload: AICCKnowledgePayload }) => {
      const response = await apiRequest<{ knowledge: AICCKnowledge }>(`/api/v1/aicc/agents/${agentId}/knowledge`, {
        method: 'PUT',
        body: payload,
      })
      return response.knowledge
    },
    onSuccess: (_knowledge, vars) => {
      void client.invalidateQueries({ queryKey: aiccKnowledgeKey(vars.agentId) })
    },
  })
}

// useAICCSessionsQuery 查询选中智能体的会话摘要列表；无智能体时保持禁用。
export function useAICCSessionsQuery(agentId: Ref<string | undefined>, filters?: Ref<AICCSessionFilters | undefined>) {
  return useQuery<AICCSession[]>({
    queryKey: computed(() => aiccSessionsKey(agentId.value, filters?.value)),
    enabled: () => Boolean(agentId.value),
    queryFn: async () => {
      if (!agentId.value) return []
      const response = await apiRequest<{ sessions: AICCSession[] }>(`/api/v1/aicc/agents/${agentId.value}/sessions`, {
        query: {
          limit: 100,
          resolution_status: filters?.value?.resolution_status || undefined,
          lead_status: filters?.value?.lead_status || undefined,
          channel: filters?.value?.channel || undefined,
          region: filters?.value?.region?.trim() || undefined,
          start_at: filters?.value?.start_at || undefined,
          end_at: filters?.value?.end_at || undefined,
          keyword: filters?.value?.keyword?.trim() || undefined,
        },
      })
      return response.sessions
    },
  })
}

// useAICCSessionQuery 查询单个会话的消息明细；仅在用户选中会话后触发。
export function useAICCSessionQuery(sessionId: Ref<string | undefined>) {
  return useQuery<AICCSessionDetail | null>({
    queryKey: computed(() => aiccSessionKey(sessionId.value)),
    enabled: () => Boolean(sessionId.value),
    queryFn: async () => {
      if (!sessionId.value) return null
      return apiRequest<AICCSessionDetail>(`/api/v1/aicc/sessions/${sessionId.value}`)
    },
  })
}

// useAICCLeadsQuery 查询当前企业的访客线索列表。
export function useAICCLeadsQuery() {
  return useQuery<AICCLead[]>({
    queryKey: AICC_LEADS_KEY,
    queryFn: async () => {
      const response = await apiRequest<{ leads: AICCLead[] }>('/api/v1/aicc/leads', {
        query: { limit: 200 },
      })
      return response.leads
    },
  })
}

// useMarkAICCLeadRead 标记线索已读，并刷新线索列表与统计卡片。
export function useMarkAICCLeadRead() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (leadId: string) => {
      await apiRequest<void>(`/api/v1/aicc/leads/${leadId}/read`, { method: 'POST' })
      return leadId
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: AICC_LEADS_KEY })
      void client.invalidateQueries({ queryKey: AICC_ANALYTICS_KEY })
    },
  })
}

// useAICCAnalyticsQuery 查询 AICC 运营看板统计；筛选条件进入 query key，避免不同时间窗口复用旧缓存。
export function useAICCAnalyticsQuery(filters?: Ref<AICCAnalyticsFilters | undefined>) {
  return useQuery<AICCAnalytics>({
    queryKey: computed(() => aiccAnalyticsKey(filters?.value)),
    queryFn: async () => {
      const currentFilters = filters?.value
      const response = await apiRequest<{ analytics: AICCAnalytics }>('/api/v1/aicc/analytics', {
        query: {
          start_at: currentFilters?.start_at,
          end_at: currentFilters?.end_at,
          bucket: currentFilters?.bucket,
          agent_id: currentFilters?.agent_id,
        },
      })
      return response.analytics
    },
  })
}

// downloadAICCLeadsCSV 下载当前企业线索 CSV，用于运营人员离线跟进。
export function downloadAICCLeadsCSV() {
  return apiDownload('/api/v1/aicc/leads/export')
}

// fetchAICCPublicConfig 读取访客公开配置；该接口不带 Authorization，避免公开链接受登录态影响。
export async function fetchAICCPublicConfig(publicToken: string, channel: AICCPublicChannel = 'web_link'): Promise<AICCPublicConfig> {
  const response = await apiRequest<{ config: AICCPublicConfig }>(`/api/v1/public/aicc/agents/${publicToken}/config`, {
    withAuth: false,
    query: { channel },
  })
  return response.config
}

// createAICCPublicSession 为公开访客创建短期会话 token。
export async function createAICCPublicSession(publicToken: string, channel: AICCPublicChannel = 'web_link'): Promise<AICCPublicSession> {
  const storageKey = aiccPublicSessionStorageKey(publicToken, channel)
  const storedSessionToken = readAICCPublicSessionToken(storageKey)
  const response = await apiRequest<{ session: AICCPublicSession }>(`/api/v1/public/aicc/agents/${publicToken}/sessions`, {
    method: 'POST',
    withAuth: false,
    body: {
      channel,
      session_token: storedSessionToken || undefined,
      referrer: typeof document === 'undefined' ? '' : document.referrer,
      source_url: typeof window === 'undefined' ? '' : window.location.href,
    },
  })
  if (response.session.session_token) {
    writeAICCPublicSessionToken(storageKey, response.session.session_token)
  }
  return response.session
}

// fetchAICCPublicSession 通过访客 session token 读取当前会话消息，用于刷新页面后恢复对话内容。
export async function fetchAICCPublicSession(sessionToken: string): Promise<AICCPublicSessionDetail> {
  const response = await apiRequest<{ session: AICCPublicSessionDetail }>(`/api/v1/public/aicc/sessions/${sessionToken}`, {
    withAuth: false,
  })
  return response.session
}

// readAICCPublicStoredSessionToken 读取当前公开入口的本地会话 token，用于页面刷新后恢复内存状态。
export function readAICCPublicStoredSessionToken(publicToken: string, channel: AICCPublicChannel = 'web_link'): string {
  return readAICCPublicSessionToken(aiccPublicSessionStorageKey(publicToken, channel))
}

// clearAICCPublicStoredSessionToken 清除当前公开入口的本地会话 token，用于访客主动新建对话。
export function clearAICCPublicStoredSessionToken(publicToken: string, channel: AICCPublicChannel = 'web_link'): void {
  const key = aiccPublicSessionStorageKey(publicToken, channel)
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.removeItem(key)
  } catch {
    // localStorage 不可用时忽略，当前页面仍会清掉内存会话。
  }
}

// aiccPublicSessionStorageKey 按公开入口和渠道隔离访客会话，避免公开链接与挂件互相串会话。
function aiccPublicSessionStorageKey(publicToken: string, channel: AICCPublicChannel): string {
  return `aicc:session:${publicToken}:${channel}`
}

// readAICCPublicSessionToken 容忍隐私模式禁用 localStorage，公开页仍可退化为新建会话。
function readAICCPublicSessionToken(key: string): string {
  if (typeof localStorage === 'undefined') return ''
  try {
    return localStorage.getItem(key) || ''
  } catch {
    return ''
  }
}

// writeAICCPublicSessionToken 持久化服务端返回的最新 token，用于刷新页面后的同渠道续接。
function writeAICCPublicSessionToken(key: string, token: string): void {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(key, token)
  } catch {
    // localStorage 不可用时忽略，公开客服仍保持当前内存会话。
  }
}

// consentAICCPublicSession 记录访客已同意隐私说明；仅 consent_required 模式需要调用。
export async function consentAICCPublicSession(sessionToken: string): Promise<void> {
  await apiRequest<void>(`/api/v1/public/aicc/sessions/${sessionToken}/consent`, {
    method: 'POST',
    withAuth: false,
  })
}

// sendAICCPublicMessage 发送文字、图片或混合消息，并返回助手回复。
export async function sendAICCPublicMessage(
  sessionToken: string,
  payload: { text?: string; image_file_id?: string },
): Promise<AICCPublicMessageResult> {
  const response = await apiRequest<{ message: AICCPublicMessageResult }>(`/api/v1/public/aicc/sessions/${sessionToken}/messages`, {
    method: 'POST',
    withAuth: false,
    body: payload,
  })
  return response.message
}

// submitAICCPublicLeadValues 提交公开访客留资字段值。
export async function submitAICCPublicLeadValues(
  sessionToken: string,
  values: Record<string, string>,
): Promise<AICCPublicLeadValuesResult> {
  const response = await apiRequest<{ lead: AICCPublicLeadValuesResult }>(`/api/v1/public/aicc/sessions/${sessionToken}/lead-values`, {
    method: 'POST',
    withAuth: false,
    body: { values },
  })
  return response.lead
}

// submitAICCPublicFeedback 把访客对某条助手回复的评价绑定到当前会话。
export async function submitAICCPublicFeedback(
  sessionToken: string,
  messageId: string,
  helpful: boolean,
): Promise<void> {
  await apiRequest<void>(`/api/v1/public/aicc/sessions/${sessionToken}/messages/${messageId}/feedback`, {
    method: 'POST',
    withAuth: false,
    body: { helpful },
  })
}

// updateAICCPublicSessionResolution 将当前公开访客会话标记为已解决或未解决，不绑定单条助手回复。
export async function updateAICCPublicSessionResolution(
  sessionToken: string,
  resolutionStatus: 'resolved' | 'unresolved',
): Promise<AICCPublicResolutionResult> {
  const response = await apiRequest<{ resolution: AICCPublicResolutionResult }>(`/api/v1/public/aicc/sessions/${sessionToken}/resolution`, {
    method: 'POST',
    withAuth: false,
    body: { resolution_status: resolutionStatus },
  })
  return response.resolution
}

// uploadAICCPublicImage 直接用 fetch 上传二进制图片；apiRequest 只处理 JSON，不适合该接口。
export async function uploadAICCPublicImage(sessionToken: string, file: File): Promise<AICCPublicImageResult> {
  const params = new URLSearchParams({ filename: file.name })
  const headers: Record<string, string> = {
    Accept: 'application/json',
    'Content-Type': file.type || 'application/octet-stream',
  }
  const csrf = getCsrfToken()
  if (csrf) headers['X-CSRF-Token'] = csrf

  const response = await fetch(`/api/v1/public/aicc/sessions/${sessionToken}/images?${params.toString()}`, {
    method: 'POST',
    headers,
    body: file,
  })
  const contentType = response.headers.get('content-type') ?? ''
  const payload = contentType.includes('application/json')
    ? await response.json().catch(() => undefined)
    : await response.text().catch(() => undefined)
  if (!response.ok) {
    const message = typeof payload === 'object' && payload && 'message' in payload
      ? String((payload as { message?: unknown }).message)
      : `HTTP ${response.status}`
    throw new Error(message)
  }
  return (payload as { image: AICCPublicImageResult }).image
}
