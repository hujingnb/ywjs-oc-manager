// 行业知识库 API hooks：平台管理员管理平台级行业库和库内 RAGFlow 文件。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed } from 'vue'
import type { Ref } from 'vue'

import { apiDownload, apiRequest } from '@/api/client'
import { xhrUpload } from '@/api/xhrUpload'
import type { KnowledgeDocument } from '@/api/hooks/useKnowledge'

// IndustryKnowledgeBase 是平台行业库列表中的摘要信息。
export interface IndustryKnowledgeBase {
  id: string
  name: string
  document_count: number
  created_at: string
  updated_at: string
}

// IndustryKnowledgeBaseList 是行业库分页列表响应。
export interface IndustryKnowledgeBaseList {
  items: IndustryKnowledgeBase[]
  total: number
}

// IndustryKnowledgeUploadToken 是平台行业库外部上传入口的当前配置。
export interface IndustryKnowledgeUploadToken {
  upload_token: string
}

// IndustryKnowledgeFileList 是行业库文件列表响应；行业库不展示累计配额。
export interface IndustryKnowledgeFileList {
  items: KnowledgeDocument[]
  total: number
}

const baseListKey = (keyword?: string) => ['industry-knowledge-bases', keyword ?? ''] as const
const fileListKeyPrefix = (industryId: string | undefined) => ['industry-knowledge-files', industryId] as const
const fileListKey = (industryId: string | undefined, query: IndustryKnowledgeFileListQuery) => [...fileListKeyPrefix(industryId), query] as const
const uploadTokenKey = ['industry-knowledge-upload-token'] as const
const industryFileDefaultPage = 1
const industryFileDefaultPageSize = 50

// IndustryKnowledgeFileListQueryInput 是行业库文件列表页传入分页和筛选的原始参数。
export interface IndustryKnowledgeFileListQueryInput {
  page?: number | null
  pageSize?: number | null
  keyword?: string | null
  status?: string | null
  createdFrom?: string | null
  createdTo?: string | null
}

// IndustryKnowledgeFileListQuery 是传给后端行业库文件列表接口的 query 参数。
export interface IndustryKnowledgeFileListQuery extends Record<string, string | number | undefined> {
  page: number
  page_size: number
  keyword?: string
  status?: string
  created_from?: string
  created_to?: string
}

// IndustryKnowledgeFileListQueryRef 只要求响应式值可读，兼容 ref 和 computed。
interface IndustryKnowledgeFileListQueryRef<T> {
  value: T
}

// IndustryKnowledgeFileListQueryOptions 是行业库文件 hook 接收的响应式列表控制参数。
export interface IndustryKnowledgeFileListQueryOptions {
  page?: IndustryKnowledgeFileListQueryRef<number | undefined>
  pageSize?: IndustryKnowledgeFileListQueryRef<number | undefined>
  keyword?: IndustryKnowledgeFileListQueryRef<string | undefined>
  status?: IndustryKnowledgeFileListQueryRef<string | undefined>
  createdFrom?: IndustryKnowledgeFileListQueryRef<string | undefined>
  createdTo?: IndustryKnowledgeFileListQueryRef<string | undefined>
}

// buildIndustryKnowledgeFileListQuery 统一裁剪文件筛选条件并兜底分页，避免页面直接拼 URL 参数。
export function buildIndustryKnowledgeFileListQuery(input: IndustryKnowledgeFileListQueryInput): IndustryKnowledgeFileListQuery {
  const keyword = stringOrUndefined(input.keyword)
  const status = stringOrUndefined(input.status)
  const createdFrom = stringOrUndefined(input.createdFrom)
  const createdTo = stringOrUndefined(input.createdTo)
  return {
    page: positiveIntegerOrDefault(input.page, industryFileDefaultPage),
    page_size: positiveIntegerOrDefault(input.pageSize, industryFileDefaultPageSize),
    ...(keyword ? { keyword } : {}),
    ...(status ? { status } : {}),
    ...(createdFrom ? { created_from: createdFrom } : {}),
    ...(createdTo ? { created_to: createdTo } : {}),
  }
}

// positiveIntegerOrDefault 将分页输入收敛为后端可接受的正整数。
function positiveIntegerOrDefault(value: number | null | undefined, fallback: number): number {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 1) {
    return fallback
  }
  return Math.floor(value)
}

// stringOrUndefined 让空白筛选条件不进入 URL，交给后端返回未过滤列表。
function stringOrUndefined(value: string | null | undefined): string | undefined {
  const trimmed = value?.trim() ?? ''
  return trimmed || undefined
}

// useIndustryKnowledgeUploadTokenQuery 读取固定上传 token，供平台管理员接口文档展示真实调用值。
export function useIndustryKnowledgeUploadTokenQuery() {
  return useQuery<IndustryKnowledgeUploadToken>({
    queryKey: uploadTokenKey,
    queryFn: async () => apiRequest<IndustryKnowledgeUploadToken>('/api/v1/industry-knowledge-bases/upload-token'),
  })
}

// useIndustryKnowledgeBasesQuery 列出行业库；助手版本表单复用该 hook 作为多选来源。
export function useIndustryKnowledgeBasesQuery(enabled?: () => boolean, keyword?: Ref<string>) {
  return useQuery<IndustryKnowledgeBaseList>({
    queryKey: computed(() => baseListKey(keyword?.value.trim())),
    enabled,
    queryFn: async () => {
      const res = await apiRequest<Partial<IndustryKnowledgeBaseList>>('/api/v1/industry-knowledge-bases', {
        query: { page: 1, page_size: 200, keyword: keyword?.value.trim() || undefined },
      })
      return { items: res.items ?? [], total: res.total ?? 0 }
    },
  })
}

// useCreateIndustryKnowledgeBase 创建行业库，名称由后端保证未删除记录唯一。
export function useCreateIndustryKnowledgeBase() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (name: string) => apiRequest<IndustryKnowledgeBase>('/api/v1/industry-knowledge-bases', {
      method: 'POST',
      body: { name },
    }),
    onSuccess: () => { void client.invalidateQueries({ queryKey: ['industry-knowledge-bases'] }) },
  })
}

// useRenameIndustryKnowledgeBase 重命名行业库，成功后刷新列表和当前来源选项。
export function useRenameIndustryKnowledgeBase() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, name }: { id: string; name: string }) =>
      apiRequest<IndustryKnowledgeBase>(`/api/v1/industry-knowledge-bases/${id}`, {
        method: 'PUT',
        body: { name },
      }),
    onSuccess: () => { void client.invalidateQueries({ queryKey: ['industry-knowledge-bases'] }) },
  })
}

// useDeleteIndustryKnowledgeBase 删除未被助手版本引用的行业库。
export function useDeleteIndustryKnowledgeBase() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      await apiRequest<void>(`/api/v1/industry-knowledge-bases/${id}`, { method: 'DELETE' })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['industry-knowledge-bases'] })
      void client.invalidateQueries({ queryKey: ['industry-knowledge-files'] })
    },
  })
}

// useIndustryKnowledgeFilesQuery 列出指定行业库内的文件，并在解析中自动轮询。
export function useIndustryKnowledgeFilesQuery(industryId: Ref<string | undefined>, options: IndustryKnowledgeFileListQueryOptions = {}) {
  const listQuery = computed(() => buildIndustryKnowledgeFileListQuery({
    page: options.page?.value,
    pageSize: options.pageSize?.value,
    keyword: options.keyword?.value,
    status: options.status?.value,
    createdFrom: options.createdFrom?.value,
    createdTo: options.createdTo?.value,
  }))
  return useQuery<IndustryKnowledgeFileList | null>({
    queryKey: computed(() => fileListKey(industryId.value, listQuery.value)),
    enabled: () => Boolean(industryId.value),
    refetchInterval: (query) => query.state.data?.items.some(item => item.parse_status === 'queued' || item.parse_status === 'running') ? 5000 : false,
    queryFn: async () => {
      if (!industryId.value) return null
      const res = await apiRequest<Partial<IndustryKnowledgeFileList>>(
        `/api/v1/industry-knowledge-bases/${industryId.value}/knowledge`,
        { query: listQuery.value },
      )
      return { items: res.items ?? [], total: res.total ?? 0 }
    },
  })
}

// useUploadIndustryKnowledgeFile 上传行业库文件；同名覆盖由后端按行业库内文件名处理。
export function useUploadIndustryKnowledgeFile(industryId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: {
      file: File
      onProgress?: (loaded: number, total: number) => void
      signal?: AbortSignal
    }) => {
      if (!industryId.value) throw new Error('缺少行业知识库 ID')
      const params = new URLSearchParams({ filename: input.file.name })
      await xhrUpload(`/api/v1/industry-knowledge-bases/${industryId.value}/knowledge?${params.toString()}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/octet-stream' },
        body: input.file,
        onProgress: input.onProgress,
        signal: input.signal,
      })
    },
    onSettled: () => {
      void client.invalidateQueries({ queryKey: fileListKeyPrefix(industryId.value) })
      void client.invalidateQueries({ queryKey: ['industry-knowledge-bases'] })
    },
  })
}

// useDeleteIndustryKnowledgeFile 删除行业库文件。
export function useDeleteIndustryKnowledgeFile(industryId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (documentId: string) => {
      if (!industryId.value) throw new Error('缺少行业知识库 ID')
      await apiRequest<void>(
        `/api/v1/industry-knowledge-bases/${industryId.value}/knowledge/${documentId}`,
        { method: 'DELETE' },
      )
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: fileListKeyPrefix(industryId.value) })
      void client.invalidateQueries({ queryKey: ['industry-knowledge-bases'] })
    },
  })
}

// useReparseIndustryKnowledgeFile 重新触发行业库文件解析。
export function useReparseIndustryKnowledgeFile(industryId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (documentId: string) => {
      if (!industryId.value) throw new Error('缺少行业知识库 ID')
      await apiRequest<KnowledgeDocument>(
        `/api/v1/industry-knowledge-bases/${industryId.value}/knowledge/${documentId}/reparse`,
        { method: 'POST' },
      )
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: fileListKeyPrefix(industryId.value) })
    },
  })
}

// downloadIndustryKnowledgeFile 通过受保护接口下载行业库文件原件。
export async function downloadIndustryKnowledgeFile(industryId: string, documentId: string, fileName: string): Promise<void> {
  const { blob, filename } = await apiDownload(`/api/v1/industry-knowledge-bases/${industryId}/knowledge/${documentId}/file`)
  const objectUrl = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = objectUrl
  link.download = filename ?? fileName
  document.body.appendChild(link)
  try {
    link.click()
  } finally {
    link.remove()
    URL.revokeObjectURL(objectUrl)
  }
}
