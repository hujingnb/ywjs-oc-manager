import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import type { RuntimeNode } from '@/api'

const RUNTIME_NODES_KEY = ['runtime-nodes'] as const

// useRuntimeNodesQuery 列出 runtime 节点。
// 仅平台管理员可访问，因此前端在路由层就限制了入口；这里只负责数据加载。
export function useRuntimeNodesQuery() {
  return useQuery<RuntimeNode[]>({
    queryKey: RUNTIME_NODES_KEY,
    queryFn: async () => {
      const response = await apiRequest<{ runtime_nodes: RuntimeNode[] }>('/api/v1/runtime-nodes', {
        query: { limit: 200 },
      })
      return response.runtime_nodes
    },
  })
}

// useRuntimeNodeQuery 查询单个节点详情。
export function useRuntimeNodeQuery(nodeId: Ref<string | undefined>) {
  return useQuery<RuntimeNode | null>({
    queryKey: ['runtime-node', nodeId],
    enabled: () => Boolean(nodeId.value),
    queryFn: async () => {
      if (!nodeId.value) return null
      const response = await apiRequest<{ runtime_node: RuntimeNode }>(`/api/v1/runtime-nodes/${nodeId.value}`)
      return response.runtime_node
    },
  })
}

// useSetRuntimeNodeStatus 启用/禁用节点。
export function useSetRuntimeNodeStatus() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ nodeId, action }: { nodeId: string; action: 'enable' | 'disable' }) => {
      const response = await apiRequest<{ runtime_node: RuntimeNode }>(
        `/api/v1/runtime-nodes/${nodeId}/${action}`,
        { method: 'POST' },
      )
      return response.runtime_node
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: RUNTIME_NODES_KEY })
    },
  })
}
