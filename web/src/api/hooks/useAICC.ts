// AICC API hooks 只封装请求、缓存键和失效范围；页面展示逻辑放在 pages/components 中。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'
import { computed } from 'vue'

import { apiRequest } from '@/api/client'
import type { AICCAgent, AICCAgentPayload } from '@/domain/aicc'

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
