// 知识库 API hooks 负责组织级与实例级 RAGFlow 文件列表、上传、下载、删除和重解析。
// 上传走 xhrUpload 支持进度反馈与取消；其余 JSON 接口统一走 apiRequest。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed } from 'vue'
import type { Ref } from 'vue'

import { apiRequest, extractErrorMessage, getStoredAccessToken } from '@/api/client'
import { xhrUpload } from '@/api/xhrUpload'

// KnowledgeDocument 是 manager 从 RAGFlow document 元数据缓存归一化后的扁平文件视图。
export interface KnowledgeDocument {
  id: string
  name: string
  size: number
  mime_type?: string
  suffix?: string
  parse_status: 'queued' | 'running' | 'completed' | 'failed' | 'stopped' | string
  progress: number
  last_error?: string
  created_at: string
}

// KnowledgeListing 是扁平文件列表响应。
export interface KnowledgeListing {
  items: KnowledgeDocument[]
  total: number
}

const orgKey = (orgId: string | undefined) => ['knowledge', 'org', orgId] as const
const appKey = (appId: string | undefined) => ['knowledge', 'app', appId] as const

// RAGFlow 文件上传仍保留 100MB 前端拦截，避免大文件进入无意义上传会话。
export const KNOWLEDGE_UPLOAD_MAX_BYTES = 100 * 1024 * 1024
export const KNOWLEDGE_UPLOAD_MAX_LABEL = '100MB'
export const KNOWLEDGE_UPLOAD_MAX_MESSAGE = `单文件最多支持 ${KNOWLEDGE_UPLOAD_MAX_LABEL}`

// isKnowledgeUploadTooLarge 在页面发起上传会话前做本地拦截。
export function isKnowledgeUploadTooLarge(file: Pick<File, 'size'>): boolean {
  return file.size > KNOWLEDGE_UPLOAD_MAX_BYTES
}

// downloadKnowledgeBlob 负责把受保护知识库下载接口返回的二进制内容转成浏览器下载。
async function downloadKnowledgeBlob(url: string, fileName: string): Promise<void> {
  const headers: Record<string, string> = {}
  const token = getStoredAccessToken()
  if (token) headers.Authorization = `Bearer ${token}`
  const response = await fetch(url, { headers })
  if (!response.ok) {
    const contentType = response.headers.get('content-type') ?? ''
    const body =
      contentType.includes('application/json')
        ? await response.json().catch(() => undefined)
        : await response.text().catch(() => undefined)
    throw new Error(extractErrorMessage(body, response.status))
  }
  const blob = await response.blob()
  const objectUrl = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = objectUrl
  link.download = fileName
  document.body.appendChild(link)
  try {
    link.click()
  } finally {
    link.remove()
    URL.revokeObjectURL(objectUrl)
  }
}

export function downloadOrgKnowledgeFile(orgId: string, documentId: string, fileName: string): Promise<void> {
  return downloadKnowledgeBlob(`/api/v1/organizations/${orgId}/knowledge/${documentId}/file`, fileName)
}

export function downloadAppKnowledgeFile(appId: string, documentId: string, fileName: string): Promise<void> {
  return downloadKnowledgeBlob(`/api/v1/apps/${appId}/knowledge/${documentId}/file`, fileName)
}

export function useOrgKnowledgeQuery(orgId: Ref<string | undefined>) {
  return useQuery<KnowledgeListing | null>({
    queryKey: computed(() => orgKey(orgId.value)),
    enabled: () => Boolean(orgId.value),
    refetchInterval: (query) => shouldPollKnowledge(query.state.data) ? 5000 : false,
    queryFn: async () => {
      if (!orgId.value) return null
      return apiRequest<KnowledgeListing>(`/api/v1/organizations/${orgId.value}/knowledge`)
    },
  })
}

export function useAppKnowledgeQuery(appId: Ref<string | undefined>) {
  return useQuery<KnowledgeListing | null>({
    queryKey: computed(() => appKey(appId.value)),
    enabled: () => Boolean(appId.value),
    refetchInterval: (query) => shouldPollKnowledge(query.state.data) ? 5000 : false,
    queryFn: async () => {
      if (!appId.value) return null
      return apiRequest<KnowledgeListing>(`/api/v1/apps/${appId.value}/knowledge`)
    },
  })
}

export function useUploadOrgKnowledge(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: {
      file: File
      onProgress?: (loaded: number, total: number) => void
      signal?: AbortSignal
    }) => {
      if (!orgId.value) throw new Error('缺少组织 ID')
      const params = new URLSearchParams({ filename: input.file.name })
      await xhrUpload(`/api/v1/organizations/${orgId.value}/knowledge?${params.toString()}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/octet-stream' },
        body: input.file,
        onProgress: input.onProgress,
        signal: input.signal,
      })
    },
    onSettled: () => {
      void client.invalidateQueries({ queryKey: orgKey(orgId.value) })
    },
  })
}

export function useUploadAppKnowledge(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: {
      file: File
      onProgress?: (loaded: number, total: number) => void
      signal?: AbortSignal
    }) => {
      if (!appId.value) throw new Error('缺少实例 ID')
      const params = new URLSearchParams({ filename: input.file.name })
      await xhrUpload(`/api/v1/apps/${appId.value}/knowledge?${params.toString()}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/octet-stream' },
        body: input.file,
        onProgress: input.onProgress,
        signal: input.signal,
      })
    },
    onSettled: () => {
      void client.invalidateQueries({ queryKey: appKey(appId.value) })
    },
  })
}

export function useDeleteOrgKnowledge(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (documentId: string) => {
      if (!orgId.value) throw new Error('缺少组织 ID')
      await apiRequest<void>(`/api/v1/organizations/${orgId.value}/knowledge/${documentId}`, { method: 'DELETE' })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: orgKey(orgId.value) })
    },
  })
}

export function useDeleteAppKnowledge(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (documentId: string) => {
      if (!appId.value) throw new Error('缺少实例 ID')
      await apiRequest<void>(`/api/v1/apps/${appId.value}/knowledge/${documentId}`, { method: 'DELETE' })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: appKey(appId.value) })
    },
  })
}

export function useReparseOrgKnowledge(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (documentId: string) => {
      if (!orgId.value) throw new Error('缺少组织 ID')
      await apiRequest<KnowledgeDocument>(`/api/v1/organizations/${orgId.value}/knowledge/${documentId}/reparse`, { method: 'POST' })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: orgKey(orgId.value) })
    },
  })
}

export function useReparseAppKnowledge(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (documentId: string) => {
      if (!appId.value) throw new Error('缺少实例 ID')
      await apiRequest<KnowledgeDocument>(`/api/v1/apps/${appId.value}/knowledge/${documentId}/reparse`, { method: 'POST' })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: appKey(appId.value) })
    },
  })
}

function shouldPollKnowledge(listing: KnowledgeListing | null | undefined): boolean {
  return Boolean(listing?.items.some(item => item.parse_status === 'queued' || item.parse_status === 'running'))
}

export const _appKnowledgeKey = appKey
