// AICC API hooks 只封装请求、缓存键和失效范围；页面展示逻辑放在 pages/components 中。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'
import { computed } from 'vue'

import { apiRequest, getCsrfToken } from '@/api/client'
import type {
  AICCAgent,
  AICCAgentPayload,
  AICCPublicConfig,
  AICCPublicImageResult,
  AICCPublicMessageResult,
  AICCPublicSession,
} from '@/domain/aicc'

const AICC_AGENTS_KEY = ['aicc', 'agents'] as const
const aiccAgentsKey = (orgId?: string) => [...AICC_AGENTS_KEY, orgId ?? 'current'] as const
const aiccAgentKey = (agentId?: string) => ['aicc', 'agent', agentId] as const

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

// fetchAICCPublicConfig 读取访客公开配置；该接口不带 Authorization，避免公开链接受登录态影响。
export async function fetchAICCPublicConfig(publicToken: string): Promise<AICCPublicConfig> {
  const response = await apiRequest<{ config: AICCPublicConfig }>(`/api/v1/public/aicc/agents/${publicToken}/config`, { withAuth: false })
  return response.config
}

// createAICCPublicSession 为公开访客创建短期会话 token。
export async function createAICCPublicSession(publicToken: string): Promise<AICCPublicSession> {
  const response = await apiRequest<{ session: AICCPublicSession }>(`/api/v1/public/aicc/agents/${publicToken}/sessions`, {
    method: 'POST',
    withAuth: false,
    body: {
      channel: 'web_link',
      referrer: typeof document === 'undefined' ? '' : document.referrer,
      source_url: typeof window === 'undefined' ? '' : window.location.href,
    },
  })
  return response.session
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
