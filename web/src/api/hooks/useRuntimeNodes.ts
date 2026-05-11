// Runtime Node API hooks 负责平台管理员管理运行节点。
// 节点启停只刷新节点列表；节点详情页通过 nodeId 独立 query 读取。
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
// nodeId 缺失时返回 null，避免详情页路由参数未就绪时请求 /undefined。
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
// 状态变化会影响调度可用性，前端仅刷新节点列表，实际调度结果由后端保证。
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
