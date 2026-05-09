import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import type { RuntimeNode } from '@/api'

const RUNTIME_NODES_KEY = ['runtime-nodes'] as const

export interface RuntimeNodeFormPayload {
  name: string
  heartbeat_interval_seconds?: number
  node_data_root?: string
}

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

// useCreateRuntimeNode 创建节点并返回 bootstrap token。
export function useCreateRuntimeNode() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (payload: RuntimeNodeFormPayload) => {
      const response = await apiRequest<{ runtime_node: RuntimeNode }>('/api/v1/runtime-nodes', {
        method: 'POST',
        body: payload,
      })
      return response.runtime_node
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: RUNTIME_NODES_KEY })
    },
  })
}

// useRotateBootstrap 轮换 bootstrap token；后端会拒绝 active 节点。
export function useRotateBootstrap() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (nodeId: string) => {
      const response = await apiRequest<{ runtime_node: RuntimeNode }>(
        `/api/v1/runtime-nodes/${nodeId}/rotate-bootstrap`,
        { method: 'POST' },
      )
      return response.runtime_node
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: RUNTIME_NODES_KEY })
    },
  })
}

// useUpdateRuntimeNodeMaxApps 设置或清空节点的应用数上限（仅 platform_admin）。
// maxApps 传 null 表示清空（不限）；传非负整数表示显式上限。
export function useUpdateRuntimeNodeMaxApps() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ nodeId, maxApps }: { nodeId: string; maxApps: number | null }) => {
      const response = await apiRequest<{ runtime_node: RuntimeNode }>(
        `/api/v1/runtime-nodes/${nodeId}`,
        { method: 'PATCH', body: { max_apps: maxApps } },
      )
      return response.runtime_node
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: RUNTIME_NODES_KEY })
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
