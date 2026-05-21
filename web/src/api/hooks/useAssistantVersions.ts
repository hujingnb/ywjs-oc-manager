// 助手版本 API hooks：平台管理员维护版本目录（列表、详情、增删改）与 skill tar 上传。
// 写操作统一失效版本列表缓存；skill 上传走原生 fetch（apiRequest 只支持 JSON body）。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'

import { apiRequest, getCsrfToken, getStoredAccessToken } from '@/api/client'

// AUXILIARY_SLOTS 是智能路由的 8 个 auxiliary 槽位，key 与后端约定一致，顺序固定用于表单渲染。
export const AUXILIARY_SLOTS = [
  { key: 'vision', label: '图像识别' },
  { key: 'compression', label: '上下文压缩' },
  { key: 'web_extract', label: '网页提取' },
  { key: 'session_search', label: '会话搜索' },
  { key: 'title_generation', label: '标题生成' },
  { key: 'approval', label: '智能审批' },
  { key: 'skills_hub', label: '技能检索' },
  { key: 'mcp', label: 'MCP 路由' },
] as const

// AssistantVersionRoutingPayload 是创建/更新请求里的 8 槽位路由对象；空字符串表示该场景走主模型。
export interface AssistantVersionRoutingPayload {
  vision: string
  compression: string
  web_extract: string
  session_search: string
  title_generation: string
  approval: string
  skills_hub: string
  mcp: string
}

// emptyRouting 返回全空的路由对象，作为表单初始值与重置值。
export function emptyRouting(): AssistantVersionRoutingPayload {
  return {
    vision: '', compression: '', web_extract: '', session_search: '',
    title_generation: '', approval: '', skills_hub: '', mcp: '',
  }
}

// AssistantVersionSkillDTO 是版本下单个 skill 的元信息。
export interface AssistantVersionSkillDTO {
  name: string
  file_path: string
  file_size: number
  file_sha256: string
}

// AssistantVersionDTO 是助手版本的前端视图。
export interface AssistantVersionDTO {
  id: string
  name: string
  description: string
  system_prompt: string
  image_id: string
  main_model: string
  // routing 是后端返回的紧凑路由 map，只含非空槽位。
  routing: Record<string, string>
  skills: AssistantVersionSkillDTO[]
  revision: number
}

// AssistantVersionFormPayload 是创建/更新版本的提交体。
export interface AssistantVersionFormPayload {
  name: string
  description: string
  system_prompt: string
  image_id: string
  main_model: string
  routing: AssistantVersionRoutingPayload
}

// RuntimeImageDTO 是配置文件暴露的可选镜像（仅 id + label）。
export interface RuntimeImageDTO {
  id: string
  label: string
}

const VERSION_LIST_KEY = ['assistant-versions'] as const

// useAssistantVersionsQuery 获取全部助手版本；仅平台管理员可读。
export function useAssistantVersionsQuery(enabled?: () => boolean) {
  return useQuery<AssistantVersionDTO[]>({
    queryKey: VERSION_LIST_KEY,
    enabled,
    queryFn: async () => {
      const res = await apiRequest<{ versions: AssistantVersionDTO[] }>('/api/v1/assistant-versions')
      return res.versions
    },
  })
}

// useRuntimeImagesQuery 获取配置文件中的可选镜像；enabled 让调用方只在表单打开时请求。
export function useRuntimeImagesQuery(enabled?: () => boolean) {
  return useQuery<RuntimeImageDTO[]>({
    queryKey: ['runtime-images'],
    enabled,
    queryFn: async () => {
      const res = await apiRequest<{ images: RuntimeImageDTO[] }>('/api/v1/runtime-images')
      return res.images
    },
  })
}

// useCreateAssistantVersion 创建版本，成功后失效列表缓存。
export function useCreateAssistantVersion() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (payload: AssistantVersionFormPayload) => {
      const res = await apiRequest<{ version: AssistantVersionDTO }>('/api/v1/assistant-versions', {
        method: 'POST', body: payload,
      })
      return res.version
    },
    onSuccess: () => { void client.invalidateQueries({ queryKey: VERSION_LIST_KEY }) },
  })
}

// useUpdateAssistantVersion 编辑版本。
export function useUpdateAssistantVersion() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, payload }: { id: string; payload: AssistantVersionFormPayload }) => {
      const res = await apiRequest<{ version: AssistantVersionDTO }>(`/api/v1/assistant-versions/${id}`, {
        method: 'PUT', body: payload,
      })
      return res.version
    },
    onSuccess: () => { void client.invalidateQueries({ queryKey: VERSION_LIST_KEY }) },
  })
}

// useDeleteAssistantVersion 删除版本。
export function useDeleteAssistantVersion() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      await apiRequest<void>(`/api/v1/assistant-versions/${id}`, { method: 'DELETE' })
    },
    onSuccess: () => { void client.invalidateQueries({ queryKey: VERSION_LIST_KEY }) },
  })
}

// useUploadAssistantVersionSkill 上传一个 skill tar（multipart 表单字段名 file）。
// 走原生 fetch：apiRequest 只支持 JSON body，无法发 multipart。
export function useUploadAssistantVersionSkill() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, file }: { id: string; file: File }) => {
      const headers: Record<string, string> = {}
      const token = getStoredAccessToken()
      if (token) headers.Authorization = `Bearer ${token}`
      const csrf = getCsrfToken()
      if (csrf) headers['X-CSRF-Token'] = csrf
      const body = new FormData()
      body.append('file', file)
      const response = await fetch(`/api/v1/assistant-versions/${id}/skills`, {
        method: 'POST', headers, body,
      })
      if (!response.ok) {
        const text = await response.text().catch(() => '')
        throw new Error(text || '上传失败')
      }
      const json = (await response.json()) as { version: AssistantVersionDTO }
      return json.version
    },
    onSuccess: () => { void client.invalidateQueries({ queryKey: VERSION_LIST_KEY }) },
  })
}

// useDeleteAssistantVersionSkill 删除版本下的一个 skill。
export function useDeleteAssistantVersionSkill() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, skillName }: { id: string; skillName: string }) => {
      const res = await apiRequest<{ version: AssistantVersionDTO }>(
        `/api/v1/assistant-versions/${id}/skills/${encodeURIComponent(skillName)}`,
        { method: 'DELETE' },
      )
      return res.version
    },
    onSuccess: () => { void client.invalidateQueries({ queryKey: VERSION_LIST_KEY }) },
  })
}
