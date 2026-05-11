// 知识库 API hooks 负责组织级与应用级文件列表、上传、删除和节点同步状态。
// 上传使用原始字节流 fetch，其余 JSON 接口统一走 apiRequest。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest, getStoredAccessToken } from '@/api/client'

// KnowledgeEntry 是知识库目录中的单个文件或目录。
export interface KnowledgeEntry {
  // 相对根目录的完整路径，作为删除、下载或进入目录的目标。
  path: string
  // 当前层级展示名。
  name: string
  // 文件大小字节数；目录可由后端返回 0。
  size: number
  // true 表示目录，false 表示普通文件。
  is_dir: boolean
}

// KnowledgeListing 是某个相对路径下的目录列表。
export interface KnowledgeListing {
  // 当前目录相对路径。
  path: string
  // 当前目录的直接子项。
  entries: KnowledgeEntry[]
}

// 缓存键包含 path，保证同一知识库不同目录可以独立缓存和失效。
const orgKey = (orgId: string | undefined, path: string) => ['knowledge', 'org', orgId, path] as const
const appKey = (appId: string | undefined, path: string) => ['knowledge', 'app', appId, path] as const

// useOrgKnowledgeQuery 列出组织级知识库。
// orgId 为空时暂停；relative 由调用方维护，通常来自面包屑或目录点击。
export function useOrgKnowledgeQuery(orgId: Ref<string | undefined>, relative: Ref<string>) {
  return useQuery<KnowledgeListing | null>({
    queryKey: ['knowledge', 'org', orgId, relative],
    enabled: () => Boolean(orgId.value),
    queryFn: async () => {
      if (!orgId.value) return null
      return apiRequest<KnowledgeListing>(`/api/v1/organizations/${orgId.value}/knowledge`, {
        query: { path: relative.value },
      })
    },
  })
}

// useAppKnowledgeQuery 列出应用级知识库。
// context 同时携带组织、拥有者和路径，后端用它定位应用工作区下的知识库边界。
export function useAppKnowledgeQuery(
  appId: Ref<string | undefined>,
  context: Ref<{ orgId: string; ownerUserId: string; path: string } | undefined>,
) {
  return useQuery<KnowledgeListing | null>({
    queryKey: ['knowledge', 'app', appId, context],
    enabled: () => Boolean(appId.value && context.value),
    queryFn: async () => {
      if (!appId.value || !context.value) return null
      return apiRequest<KnowledgeListing>(`/api/v1/apps/${appId.value}/knowledge`, {
        query: {
          org_id: context.value.orgId,
          owner_user_id: context.value.ownerUserId,
          path: context.value.path,
        },
      })
    },
  })
}

// useUploadOrgKnowledge 上传组织级文件。
// 这里直接走 fetch 发原始字节流，不通过 apiRequest，因为 body 不是 JSON。
// 成功后只失效当前目录，父级目录是否刷新由调用方切换路径时自然触发。
export function useUploadOrgKnowledge(orgId: Ref<string | undefined>, relative: Ref<string>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { path: string; file: File }) => {
      if (!orgId.value) throw new Error('缺少组织 ID')
      const params = new URLSearchParams({ path: input.path })
      const headers: Record<string, string> = { 'Content-Type': 'application/octet-stream' }
      const token = getStoredAccessToken()
      if (token) headers.Authorization = `Bearer ${token}`
      const response = await fetch(`/api/v1/organizations/${orgId.value}/knowledge?${params.toString()}`, {
        method: 'POST',
        headers,
        body: input.file,
      })
      if (!response.ok) {
        const text = await response.text().catch(() => '')
        throw new Error(text || '上传失败')
      }
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: orgKey(orgId.value, relative.value) })
    },
  })
}

// useDeleteOrgKnowledge 删除组织级文件。
// 删除目标由调用方传完整相对路径；成功后刷新当前目录列表。
export function useDeleteOrgKnowledge(orgId: Ref<string | undefined>, relative: Ref<string>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (targetPath: string) => {
      if (!orgId.value) throw new Error('缺少组织 ID')
      await apiRequest<void>(`/api/v1/organizations/${orgId.value}/knowledge`, {
        method: 'DELETE',
        query: { path: targetPath },
      })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: orgKey(orgId.value, relative.value) })
    },
  })
}

// useUploadAppKnowledge 上传应用级文件。
// 应用知识库 mutation 失效 app 级前缀，覆盖当前目录和可能受新增目录影响的列表。
export function useUploadAppKnowledge(
  appId: Ref<string | undefined>,
  context: Ref<{ orgId: string; ownerUserId: string; path: string } | undefined>,
) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { path: string; file: File }) => {
      if (!appId.value || !context.value) throw new Error('缺少应用知识库上下文')
      const params = new URLSearchParams({
        org_id: context.value.orgId,
        owner_user_id: context.value.ownerUserId,
        path: input.path,
      })
      const headers: Record<string, string> = { 'Content-Type': 'application/octet-stream' }
      const token = getStoredAccessToken()
      if (token) headers.Authorization = `Bearer ${token}`
      const response = await fetch(`/api/v1/apps/${appId.value}/knowledge?${params.toString()}`, {
        method: 'POST',
        headers,
        body: input.file,
      })
      if (!response.ok) {
        const text = await response.text().catch(() => '')
        throw new Error(text || '上传失败')
      }
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['knowledge', 'app'] })
    },
  })
}

// useDeleteAppKnowledge 删除应用级文件。
// 删除后同样失效 app 级知识库前缀，避免目录树和当前列表不一致。
export function useDeleteAppKnowledge(
  appId: Ref<string | undefined>,
  context: Ref<{ orgId: string; ownerUserId: string; path: string } | undefined>,
) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (targetPath: string) => {
      if (!appId.value || !context.value) throw new Error('缺少应用知识库上下文')
      await apiRequest<void>(`/api/v1/apps/${appId.value}/knowledge`, {
        method: 'DELETE',
        query: {
          org_id: context.value.orgId,
          owner_user_id: context.value.ownerUserId,
          path: targetPath,
        },
      })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['knowledge', 'app'] })
    },
  })
}

export const _appKnowledgeKey = appKey

// ============================================================================
// 组织级知识库节点同步状态（Chunk-2 Task 7+8）
// ============================================================================

// OrgSyncStatusEntry 描述组织知识库在单个 runtime 节点上的同步状态。
export interface OrgSyncStatusEntry {
  // 组织 ID。
  org_id: string
  // runtime 节点 ID。
  node_id: string
  // 同步状态，pending 表示已有任务但尚未成功落盘。
  status: 'pending' | 'synced' | 'failed'
  // 最近一次成功同步时间。
  last_success_at?: string
  // 最近一次失败原因，供平台管理员排障。
  last_error?: string
  // 状态更新时间。
  updated_at: string
}

const orgSyncStatusKey = (orgId: string | undefined) => ['knowledge', 'org', orgId, 'sync-status'] as const

// useOrgKnowledgeSyncStatusQuery 拉取组织在所有节点的最近同步状态。
// 4 秒轮询一次，让前端能看到 pending → synced/failed 状态翻转。
// enabled 允许页面在不可见或无权限时暂停轮询，减少后台请求。
export function useOrgKnowledgeSyncStatusQuery(orgId: Ref<string | undefined>, enabled?: Ref<boolean>) {
  return useQuery<OrgSyncStatusEntry[]>({
    queryKey: ['knowledge', 'org', orgId, 'sync-status'],
    enabled: () => Boolean(orgId.value && (enabled?.value ?? true)),
    refetchInterval: 4000,
    queryFn: async () => {
      if (!orgId.value) return []
      const resp = await apiRequest<{ statuses: OrgSyncStatusEntry[] }>(
        `/api/v1/organizations/${orgId.value}/knowledge/sync-status`,
      )
      return resp.statuses ?? []
    },
  })
}

// useRetryOrgKnowledgeSync 触发指定 (org, node) 重新同步。
// 成功只代表重试请求已提交，因此失效同步状态列表等待轮询呈现结果。
export function useRetryOrgKnowledgeSync(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (nodeId: string) => {
      if (!orgId.value) throw new Error('缺少 org_id')
      await apiRequest<void>(`/api/v1/organizations/${orgId.value}/knowledge/sync-status/retry`, {
        method: 'POST',
        body: { node_id: nodeId },
      })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: orgSyncStatusKey(orgId.value) })
    },
  })
}
