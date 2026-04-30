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

// 这里给 useAppKnowledge 留出占位，便于后续扩展应用级上传。
export const _appKnowledgeKey = appKey
