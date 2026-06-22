// 知识库 API hooks 负责组织级与实例级 RAGFlow 文件列表、上传、下载、删除和重解析。
// 上传走 xhrUpload 支持进度反馈与取消；其余 JSON 接口统一走 apiRequest。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed } from 'vue'
import type { Ref } from 'vue'

import { apiRequest, extractErrorMessage, getStoredAccessToken } from '@/api/client'
import { i18n } from '@/i18n'
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

// KnowledgeListing 是扁平文件列表响应，附带当前知识库容量信息。
export interface KnowledgeListing {
  items: KnowledgeDocument[]
  total: number
  used_bytes: number
  quota_bytes: number
  remaining_bytes: number
}

// KnowledgeRAGFlowScope 标识三个可查看 RAGFlow dataset 运维信息的知识库归属。
export type KnowledgeRAGFlowScope = 'org' | 'app' | 'industry'

// KnowledgeEmbeddingModel 是前端展示和提交的 embedding 模型候选，使用人类可读模型名。
export interface KnowledgeEmbeddingModel {
  name: string
  label: string
  provider: string
  available: boolean
}

// KnowledgeEmbeddingModelList 是平台管理员可选择的 RAGFlow embedding 模型列表。
export interface KnowledgeEmbeddingModelList {
  items: KnowledgeEmbeddingModel[]
}

// KnowledgeRAGFlowDatasetInfo 展示远端 dataset 实时信息；error/not_created 均不触发懒创建。
export interface KnowledgeRAGFlowDatasetInfo {
  scope: KnowledgeRAGFlowScope
  target_id: string
  target_name: string
  status: 'ok' | 'not_created' | 'error' | string
  ragflow_dataset_id?: string
  ragflow_dataset_name?: string
  embedding_model?: KnowledgeEmbeddingModel
  error_message?: string
  doc_num?: number
  chunk_num?: number
  updated_at?: string
}

const orgKey = (orgId: string | undefined) => ['knowledge', 'org', orgId] as const
const appKey = (appId: string | undefined) => ['knowledge', 'app', appId] as const
const ragflowDatasetKey = (scope: KnowledgeRAGFlowScope, targetId: string | undefined) => ['knowledge', 'ragflow-dataset', scope, targetId] as const
const embeddingModelsKey = ['knowledge', 'embedding-models'] as const
const knowledgeDefaultPage = 1
const knowledgeDefaultPageSize = 50

// RAGFlow 文件上传保留前端拦截，避免大文件进入无意义上传会话。
export const KNOWLEDGE_UPLOAD_MAX_BYTES = 1024 * 1024 * 1024
// KNOWLEDGE_DEFAULT_QUOTA_BYTES 对齐后端迁移默认值；旧接口缺少配额字段时前端也不能回退为无限制。
export const KNOWLEDGE_DEFAULT_QUOTA_BYTES = 1024 * 1024 * 1024
// KNOWLEDGE_UPLOAD_MAX_LABEL 是字节上限的人类可读表示，修改上限后自动同步。
export const KNOWLEDGE_UPLOAD_MAX_LABEL = `${KNOWLEDGE_UPLOAD_MAX_BYTES / (1024 * 1024)}MB`
// getKnowledgeUploadMaxMessage 返回单文件上限的用户可见提示文案（经 i18n 翻译）。
// 改为函数以支持运行时语言切换；调用方无需感知 i18n 实例。
export function getKnowledgeUploadMaxMessage(): string {
  return i18n.global.t('knowledge.messages.uploadMaxMessage', { label: KNOWLEDGE_UPLOAD_MAX_LABEL })
}

// KnowledgeListQueryInput 是列表页传入分页、文件名搜索和状态过滤的原始参数。
export interface KnowledgeListQueryInput {
  page?: number | null
  pageSize?: number | null
  keyword?: string | null
  status?: string | null
}

// KnowledgeListQuery 是传给后端列表接口的 query 参数；字段名保持 HTTP 契约的 snake_case。
export interface KnowledgeListQuery extends Record<string, string | number | undefined> {
  page: number
  page_size: number
  keyword?: string
  status?: string
}

// KnowledgeListQueryRef 只要求响应式值可读，兼容 ref 和 computed。
interface KnowledgeListQueryRef<T> {
  value: T
}

// KnowledgeListQueryOptions 是 org/app 知识库 hook 接收的响应式列表控制参数。
export interface KnowledgeListQueryOptions {
  page?: KnowledgeListQueryRef<number | undefined>
  pageSize?: KnowledgeListQueryRef<number | undefined>
  keyword?: KnowledgeListQueryRef<string | undefined>
  status?: KnowledgeListQueryRef<string | undefined>
}

// buildKnowledgeListQuery 统一裁剪搜索词并兜底分页，避免两个页面各自拼 query 产生差异。
export function buildKnowledgeListQuery(input: KnowledgeListQueryInput): KnowledgeListQuery {
  const keyword = stringOrUndefined(input.keyword)
  const status = stringOrUndefined(input.status)
  return {
    page: positiveIntegerOrDefault(input.page, knowledgeDefaultPage),
    page_size: positiveIntegerOrDefault(input.pageSize, knowledgeDefaultPageSize),
    ...(keyword ? { keyword } : {}),
    ...(status ? { status } : {}),
  }
}

// positiveIntegerOrDefault 将外部分页输入收敛为后端可接受的正整数。
function positiveIntegerOrDefault(value: number | null | undefined, fallback: number): number {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 1) {
    return fallback
  }
  return Math.floor(value)
}

// stringOrUndefined 让空白搜索词不进入 URL，交给后端返回未过滤列表。
function stringOrUndefined(value: string | null | undefined): string | undefined {
  const trimmed = value?.trim() ?? ''
  return trimmed || undefined
}

// normalizeKnowledgeNumber 把旧接口缺字段、NaN 或负数统一转成明确业务默认值，避免页面展示坏值。
function normalizeKnowledgeNumber(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : fallback
}

// normalizeKnowledgeListing 兼容旧列表响应，同时保持新接口的累计容量字段在前端始终可用。
export function normalizeKnowledgeListing(
  listing: Partial<KnowledgeListing> | null | undefined,
): KnowledgeListing {
  const items = Array.isArray(listing?.items) ? listing.items : []
  const total = normalizeKnowledgeNumber(listing?.total, items.length)
  const itemSizeTotal = items.reduce(
    (total, item) => total + normalizeKnowledgeNumber((item as Partial<KnowledgeDocument>).size, 0),
    0,
  )
  const hasReliableUsedBytes = typeof listing?.used_bytes === 'number'
    && Number.isFinite(listing.used_bytes)
    && listing.used_bytes >= 0
  const usedBytes = normalizeKnowledgeNumber(listing?.used_bytes, itemSizeTotal)
  const quotaBytes = normalizeKnowledgeNumber(listing?.quota_bytes, KNOWLEDGE_DEFAULT_QUOTA_BYTES)
  // 旧接口若返回分页列表且缺少 used_bytes，当前页大小不能代表全量已用容量，必须保守禁止继续上传。
  const remainingLimit = !hasReliableUsedBytes && total > items.length ? 0 : Math.max(0, quotaBytes - usedBytes)
  const remainingBytes = normalizeKnowledgeNumber(listing?.remaining_bytes, remainingLimit)
  return {
    items,
    total,
    used_bytes: usedBytes,
    quota_bytes: quotaBytes,
    remaining_bytes: Math.min(remainingBytes, remainingLimit),
  }
}

// formatKnowledgeBytes 统一前端知识库容量展示。
export function formatKnowledgeBytes(value: number | null | undefined): string {
  const bytes = normalizeKnowledgeNumber(value, 0)
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`
}

// isKnowledgeUploadOverRemaining 判断文件是否超过知识库当前剩余容量。
export function isKnowledgeUploadOverRemaining(
  file: Pick<File, 'size'>,
  listing: Pick<KnowledgeListing, 'remaining_bytes'> | null | undefined,
): boolean {
  if (!listing) return true
  return file.size > normalizeKnowledgeNumber(listing.remaining_bytes, KNOWLEDGE_DEFAULT_QUOTA_BYTES)
}

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

export function useOrgKnowledgeQuery(orgId: Ref<string | undefined>, options: KnowledgeListQueryOptions = {}) {
  const listQuery = computed(() => buildKnowledgeListQuery({
    page: options.page?.value,
    pageSize: options.pageSize?.value,
    keyword: options.keyword?.value,
    status: options.status?.value,
  }))
  return useQuery<KnowledgeListing | null>({
    queryKey: computed(() => [...orgKey(orgId.value), listQuery.value] as const),
    enabled: () => Boolean(orgId.value),
    refetchInterval: (query) => shouldPollKnowledge(query.state.data) ? 5000 : false,
    queryFn: async () => {
      if (!orgId.value) return null
      return normalizeKnowledgeListing(await apiRequest<KnowledgeListing>(`/api/v1/organizations/${orgId.value}/knowledge`, {
        query: listQuery.value,
      }))
    },
  })
}

export function useAppKnowledgeQuery(appId: Ref<string | undefined>, options: KnowledgeListQueryOptions = {}) {
  const listQuery = computed(() => buildKnowledgeListQuery({
    page: options.page?.value,
    pageSize: options.pageSize?.value,
    keyword: options.keyword?.value,
    status: options.status?.value,
  }))
  return useQuery<KnowledgeListing | null>({
    queryKey: computed(() => [...appKey(appId.value), listQuery.value] as const),
    enabled: () => Boolean(appId.value),
    refetchInterval: (query) => shouldPollKnowledge(query.state.data) ? 5000 : false,
    queryFn: async () => {
      if (!appId.value) return null
      return normalizeKnowledgeListing(await apiRequest<KnowledgeListing>(`/api/v1/apps/${appId.value}/knowledge`, {
        query: listQuery.value,
      }))
    },
  })
}

// ragflowDatasetPath 按知识库归属生成统一弹框读取和修改 RAGFlow dataset 的后端路径。
function ragflowDatasetPath(scope: KnowledgeRAGFlowScope, targetId: string): string {
  if (scope === 'org') return `/api/v1/organizations/${targetId}/knowledge/ragflow-dataset`
  if (scope === 'app') return `/api/v1/apps/${targetId}/knowledge/ragflow-dataset`
  return `/api/v1/industry-knowledge-bases/${targetId}/ragflow-dataset`
}

// useKnowledgeEmbeddingModelsQuery 读取平台配置的 embedding 模型候选。
export function useKnowledgeEmbeddingModelsQuery(enabled?: () => boolean) {
  return useQuery<KnowledgeEmbeddingModelList>({
    queryKey: embeddingModelsKey,
    enabled,
    queryFn: async () => apiRequest<KnowledgeEmbeddingModelList>('/api/v1/knowledge/embedding-models'),
  })
}

// useRAGFlowDatasetInfoQuery 读取指定知识库对应的远端 dataset 实时信息。
export function useRAGFlowDatasetInfoQuery(
  scope: Ref<KnowledgeRAGFlowScope>,
  targetId: Ref<string | undefined>,
  enabled?: () => boolean,
) {
  return useQuery<KnowledgeRAGFlowDatasetInfo | null>({
    queryKey: computed(() => ragflowDatasetKey(scope.value, targetId.value)),
    enabled: () => Boolean(targetId.value) && (enabled ? enabled() : true),
    queryFn: async () => {
      if (!targetId.value) return null
      return apiRequest<KnowledgeRAGFlowDatasetInfo>(ragflowDatasetPath(scope.value, targetId.value))
    },
  })
}

// useUpdateRAGFlowDatasetEmbeddingModel 修改远端 dataset 模型，并刷新对应知识库列表状态。
export function useUpdateRAGFlowDatasetEmbeddingModel(
  scope: Ref<KnowledgeRAGFlowScope>,
  targetId: Ref<string | undefined>,
) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { name: string; provider?: string }) => {
      if (!targetId.value) throw new Error(i18n.global.t('common.errors.missingKnowledgeId'))
      return apiRequest<KnowledgeRAGFlowDatasetInfo>(`${ragflowDatasetPath(scope.value, targetId.value)}/embedding-model`, {
        method: 'PATCH',
        body: input,
      })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ragflowDatasetKey(scope.value, targetId.value) })
      if (scope.value === 'org') void client.invalidateQueries({ queryKey: orgKey(targetId.value) })
      if (scope.value === 'app') void client.invalidateQueries({ queryKey: appKey(targetId.value) })
      if (scope.value === 'industry') void client.invalidateQueries({ queryKey: ['industry-knowledge-files', targetId.value] })
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
      if (!orgId.value) throw new Error(i18n.global.t('common.errors.missingOrgId'))
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
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingAppId'))
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
      if (!orgId.value) throw new Error(i18n.global.t('common.errors.missingOrgId'))
      await apiRequest<void>(`/api/v1/organizations/${orgId.value}/knowledge/${documentId}`, { method: 'DELETE' })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: orgKey(orgId.value) })
    },
  })
}

export function useClearOrgKnowledge(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      if (!orgId.value) throw new Error(i18n.global.t('common.errors.missingOrgId'))
      await apiRequest<void>(`/api/v1/organizations/${orgId.value}/knowledge`, { method: 'DELETE' })
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
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingAppId'))
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
      if (!orgId.value) throw new Error(i18n.global.t('common.errors.missingOrgId'))
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
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingAppId'))
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
