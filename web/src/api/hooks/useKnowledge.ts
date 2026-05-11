import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest, getStoredAccessToken } from '@/api/client'

export interface KnowledgeEntry {
  path: string
  name: string
  size: number
  is_dir: boolean
}

export interface KnowledgeListing {
  path: string
  entries: KnowledgeEntry[]
}

const orgKey = (orgId: string | undefined, path: string) => ['knowledge', 'org', orgId, path] as const
const appKey = (appId: string | undefined, path: string) => ['knowledge', 'app', appId, path] as const

// useOrgKnowledgeQuery 列出组织级知识库。
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

export interface OrgSyncStatusEntry {
  org_id: string
  node_id: string
  status: 'pending' | 'synced' | 'failed'
  last_success_at?: string
  last_error?: string
  updated_at: string
}

const orgSyncStatusKey = (orgId: string | undefined) => ['knowledge', 'org', orgId, 'sync-status'] as const

// useOrgKnowledgeSyncStatusQuery 拉取组织在所有节点的最近同步状态。
// 4 秒轮询一次，让前端能看到 pending → synced/failed 状态翻转。
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
