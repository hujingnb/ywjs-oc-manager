// 知识库 API hooks 负责组织级与应用级文件列表、上传、删除和节点同步状态。
// 上传走 xhrUpload 支持进度反馈与取消；其余 JSON 接口统一走 apiRequest。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest, getStoredAccessToken } from '@/api/client'
import { xhrUpload } from '@/api/xhrUpload'

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

// 知识库上传单文件上限与 manager-api files.KnowledgeMaxFileSize、nginx client_max_body_size 保持一致。
export const KNOWLEDGE_UPLOAD_MAX_BYTES = 100 * 1024 * 1024
export const KNOWLEDGE_UPLOAD_MAX_LABEL = '100MB'
export const KNOWLEDGE_UPLOAD_MAX_MESSAGE = `单文件最多支持 ${KNOWLEDGE_UPLOAD_MAX_LABEL}`

// isKnowledgeUploadTooLarge 在页面发起上传会话前做本地拦截，避免超限文件进入网络请求。
export function isKnowledgeUploadTooLarge(file: Pick<File, 'size'>): boolean {
  return file.size > KNOWLEDGE_UPLOAD_MAX_BYTES
}

// downloadKnowledgeBlob 负责把受保护知识库下载接口返回的二进制内容转成浏览器下载。
// 下载接口是 GET，但仍需要 Authorization，不能用裸 a.href 直接访问。
async function downloadKnowledgeBlob(url: string, fileName: string): Promise<void> {
  const headers: Record<string, string> = {}
  const token = getStoredAccessToken()
  if (token) headers.Authorization = `Bearer ${token}`
  const response = await fetch(url, { headers })
  if (!response.ok) {
    const text = await response.text().catch(() => '')
    throw new Error(text || '下载失败')
  }
  const blob = await response.blob()
  const objectUrl = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = objectUrl
  link.download = fileName
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(objectUrl)
}

// downloadOrgKnowledgeFile 下载组织级知识库中的单个普通文件。
export function downloadOrgKnowledgeFile(orgId: string, targetPath: string, fileName: string): Promise<void> {
  const params = new URLSearchParams({ path: targetPath })
  return downloadKnowledgeBlob(`/api/v1/organizations/${orgId}/knowledge/file?${params.toString()}`, fileName)
}

// downloadAppKnowledgeFile 下载实例级知识库中的单个普通文件。
export function downloadAppKnowledgeFile(
  appId: string,
  orgId: string,
  ownerUserId: string,
  targetPath: string,
  fileName: string,
): Promise<void> {
  const params = new URLSearchParams({
    org_id: orgId,
    owner_user_id: ownerUserId,
    path: targetPath,
  })
  return downloadKnowledgeBlob(`/api/v1/apps/${appId}/knowledge/file?${params.toString()}`, fileName)
}

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
// 走 xhrUpload 以支持进度回调与取消信号；底层会自动注入 Bearer + CSRF，
// 与 apiRequest 等价的 401 处理也由 xhrUpload 复用。
// onSuccess 改为 onSettled：取消或失败时同样刷新当前目录，避免列表与实际状态脱节。
export function useUploadOrgKnowledge(orgId: Ref<string | undefined>, relative: Ref<string>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: {
      path: string
      file: File
      onProgress?: (loaded: number, total: number) => void
      signal?: AbortSignal
    }) => {
      if (!orgId.value) throw new Error('缺少组织 ID')
      const params = new URLSearchParams({ path: input.path })
      await xhrUpload(`/api/v1/organizations/${orgId.value}/knowledge?${params.toString()}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/octet-stream' },
        body: input.file,
        onProgress: input.onProgress,
        signal: input.signal,
      })
    },
    onSettled: () => {
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
// 走 xhrUpload 以支持进度回调与取消信号；其他语义与 useUploadOrgKnowledge 一致。
// 失效 app 级前缀：兜底覆盖当前目录与可能受新增目录影响的列表。
export function useUploadAppKnowledge(
  appId: Ref<string | undefined>,
  context: Ref<{ orgId: string; ownerUserId: string; path: string } | undefined>,
) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: {
      path: string
      file: File
      onProgress?: (loaded: number, total: number) => void
      signal?: AbortSignal
    }) => {
      if (!appId.value || !context.value) throw new Error('缺少实例知识库上下文')
      const params = new URLSearchParams({
        org_id: context.value.orgId,
        owner_user_id: context.value.ownerUserId,
        path: input.path,
      })
      await xhrUpload(`/api/v1/apps/${appId.value}/knowledge?${params.toString()}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/octet-stream' },
        body: input.file,
        onProgress: input.onProgress,
        signal: input.signal,
      })
    },
    onSettled: () => {
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
      if (!appId.value || !context.value) throw new Error('缺少实例知识库上下文')
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
