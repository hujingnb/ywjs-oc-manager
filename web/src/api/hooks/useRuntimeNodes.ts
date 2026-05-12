// Runtime Node API hooks 负责平台管理员管理运行节点。
// 节点启停只刷新节点列表；节点详情页通过 nodeId 独立 query 读取。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import type { RuntimeNode } from '@/api'

const RUNTIME_NODES_KEY = ['runtime-nodes'] as const

export type ResourceRange = '1h' | '24h' | '7d' | '30d'

// NodeResourceSample 对齐节点资源趋势接口返回字段。
// 字段保持 snake_case，页面可直接按指标选择对应序列，不在 hook 层做展示转换。
export interface NodeResourceSample {
  sampled_at: string
  cpu_percent?: number
  memory_used_bytes?: number
  memory_total_bytes?: number
  disk_used_bytes?: number
  disk_total_bytes?: number
  network_rx_bytes?: number
  network_tx_bytes?: number
  instance_count?: number
  last_error?: string
}

// InstanceResourceSample 对齐应用实例资源趋势接口返回字段。
// bucket 查询中 container_status 代表桶内最新状态，趋势图仍以 sampled_at 作为横轴。
export interface InstanceResourceSample {
  sampled_at: string
  container_status?: string
  cpu_percent?: number
  memory_used_bytes?: number
  memory_limit_bytes?: number
  disk_read_bytes?: number
  disk_write_bytes?: number
  network_rx_bytes?: number
  network_tx_bytes?: number
  last_error?: string
}

// NodeInstanceResourceRow 是节点实例列表行，current_resource 表示最近一次实例采样。
export interface NodeInstanceResourceRow {
  app_id: string
  org_id: string
  owner_user_id: string
  name: string
  status: string
  runtime_node_id: string
  container_id?: string
  current_resource?: InstanceResourceSample
}

// rangeQuery 把前端固定时间范围转成后端资源趋势查询参数。
// 1h 使用原始采样，其余范围使用聚合桶，避免长时间跨度返回过多点影响页面渲染。
export function rangeQuery(range: ResourceRange): { from: string; to: string; bucket?: '5m' | '1h' } {
  const to = new Date()
  const from = new Date(to)
  let bucket: '5m' | '1h' | undefined

  switch (range) {
    case '1h':
      from.setHours(from.getHours() - 1)
      break
    case '24h':
      from.setDate(from.getDate() - 1)
      bucket = '5m'
      break
    case '7d':
      from.setDate(from.getDate() - 7)
      bucket = '5m'
      break
    case '30d':
      from.setDate(from.getDate() - 30)
      bucket = '1h'
      break
  }

  return { from: from.toISOString(), to: to.toISOString(), bucket }
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
// nodeId 缺失时 query 被禁用，避免请求 /undefined；data 通常为 undefined，除非已有缓存。
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

// useRuntimeNodeResourcesQuery 查询节点资源趋势。
// 缓存键包含 range ref，范围切换时 TanStack Query 会自动重新拉取对应时间窗。
export function useRuntimeNodeResourcesQuery(nodeId: Ref<string | undefined>, range: Ref<ResourceRange>) {
  return useQuery<NodeResourceSample[]>({
    queryKey: ['runtime-node-resources', nodeId, range],
    enabled: () => Boolean(nodeId.value),
    queryFn: async () => {
      if (!nodeId.value) return []
      const response = await apiRequest<{ samples?: NodeResourceSample[] }>(
        `/api/v1/runtime-nodes/${nodeId.value}/resources`,
        { query: rangeQuery(range.value) },
      )
      return response.samples ?? []
    },
  })
}

// useRuntimeNodeInstancesQuery 查询节点承载的应用实例列表。
// limit 与后端最大值保持一致，节点抽屉由前端本地筛选展示当前批次。
export function useRuntimeNodeInstancesQuery(nodeId: Ref<string | undefined>) {
  return useQuery<NodeInstanceResourceRow[]>({
    queryKey: ['runtime-node-instances', nodeId],
    enabled: () => Boolean(nodeId.value),
    queryFn: async () => {
      if (!nodeId.value) return []
      const response = await apiRequest<{ instances?: NodeInstanceResourceRow[] }>(
        `/api/v1/runtime-nodes/${nodeId.value}/instances`,
        { query: { limit: 200 } },
      )
      return response.instances ?? []
    },
  })
}

// useRuntimeNodeInstanceResourcesQuery 查询指定节点上的单个实例趋势。
// enabled 由调用方控制抽屉、行展开等交互状态，避免未展示时提前发起明细请求。
export function useRuntimeNodeInstanceResourcesQuery(
  nodeId: Ref<string | undefined>,
  appId: Ref<string | undefined>,
  range: Ref<ResourceRange>,
  enabled: Ref<boolean>,
) {
  return useQuery<InstanceResourceSample[]>({
    queryKey: ['runtime-node-instance-resources', nodeId, appId, range],
    enabled: () => Boolean(enabled.value && nodeId.value && appId.value),
    queryFn: async () => {
      if (!nodeId.value || !appId.value) return []
      const response = await apiRequest<{ samples?: InstanceResourceSample[] }>(
        `/api/v1/runtime-nodes/${nodeId.value}/instances/${appId.value}/resources`,
        { query: rangeQuery(range.value) },
      )
      return response.samples ?? []
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
