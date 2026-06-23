// 助手版本 API hooks：平台管理员维护版本目录（列表、详情、增删改）与 skill tar 上传。
// 写操作统一失效版本列表缓存；skill 上传走 xhrUpload 以支持进度反馈与取消。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'

import { apiRequest } from '@/api/client'
import { xhrUpload } from '@/api/xhrUpload'

// AUXILIARY_SLOTS 是智能路由的 8 个 auxiliary 槽位，key 与后端约定一致，顺序固定用于表单渲染。
// labelKey 是 platform.versions.routingSlots 命名空间下的 i18n 键路径，消费方通过 t(slot.labelKey) 翻译。
export const AUXILIARY_SLOTS = [
  { key: 'vision', labelKey: 'platform.versions.routingSlots.vision' },
  { key: 'compression', labelKey: 'platform.versions.routingSlots.compression' },
  { key: 'web_extract', labelKey: 'platform.versions.routingSlots.web_extract' },
  { key: 'session_search', labelKey: 'platform.versions.routingSlots.session_search' },
  { key: 'title_generation', labelKey: 'platform.versions.routingSlots.title_generation' },
  { key: 'approval', labelKey: 'platform.versions.routingSlots.approval' },
  { key: 'skills_hub', labelKey: 'platform.versions.routingSlots.skills_hub' },
  { key: 'mcp', labelKey: 'platform.versions.routingSlots.mcp' },
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
// source / source_ref / version 在 P4 后端 AddSkillFromLibrary 接口改造后新增；
// file_path / file_size / file_sha256 保留以兼容历史数据展示，后端 cached_path 对应前端 file_path。
export interface AssistantVersionSkillDTO {
  name: string
  source?: string
  source_ref?: string
  version?: string
  file_path?: string
  file_size?: number
  file_sha256?: string
}

// AssistantVersionIndustryKnowledgeBaseDTO 是版本已关联行业库的轻量来源信息。
export interface AssistantVersionIndustryKnowledgeBaseDTO {
  id: string
  name: string
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
  industry_knowledge_bases?: AssistantVersionIndustryKnowledgeBaseDTO[]
}

// AssistantVersionFormPayload 是创建/更新版本的提交体。
export interface AssistantVersionFormPayload {
  name: string
  description: string
  system_prompt: string
  image_id: string
  main_model: string
  routing: AssistantVersionRoutingPayload
  industry_knowledge_base_ids: string[]
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

// AddVersionSkillInput 是从市场（平台库/ClawHub）选 skill 的请求体，对应后端 handlers.AddSkillFromLibraryRequest。
export interface AddVersionSkillInput {
  // source 是 skill 来源类型，支持 "platform" 与 "clawhub"。
  source: string
  // source_ref 是来源内精准标识；platform=skill name，clawhub=slug。
  source_ref: string
  // name 是 skill 在版本内的目录名；clawhub 必填（displayName），platform 可省略。
  name?: string
  // version 是要配进版本的 skill 版本号。
  version: string
}

// useAddVersionSkill 从平台库选 skill 配进助手版本（POST /api/v1/assistant-versions/:id/skills JSON）。
// 与旧的 multipart 上传不同，此接口走 JSON body，后端返回更新后的完整 AssistantVersionDTO。
// onSuccess invalidate 版本列表缓存，确保列表侧 skill 计数同步刷新。
export function useAddVersionSkill() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, input }: { id: string; input: AddVersionSkillInput }) => {
      const res = await apiRequest<{ version: AssistantVersionDTO }>(`/api/v1/assistant-versions/${id}/skills`, {
        method: 'POST',
        body: input,
      })
      return res.version
    },
    onSuccess: () => { void client.invalidateQueries({ queryKey: VERSION_LIST_KEY }) },
  })
}

// useUploadAssistantVersionSkill 上传一个 skill tar（multipart 表单字段名 file）。
// 走 xhrUpload 支持进度回调与取消信号；multipart body 由 xhrUpload 透传，不强制 Content-Type，
// 让浏览器自动设置 boundary。
// onSuccess 改为 onSettled：取消或失败时同样刷新列表，避免新建态批量上传部分失败后视图错位。
export function useUploadAssistantVersionSkill() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: {
      id: string
      file: File
      onProgress?: (loaded: number, total: number) => void
      signal?: AbortSignal
    }) => {
      const body = new FormData()
      body.append('file', input.file)
      const res = await xhrUpload(`/api/v1/assistant-versions/${input.id}/skills`, {
        method: 'POST',
        body,
        onProgress: input.onProgress,
        signal: input.signal,
      })
      // 后端响应体形如 { version: AssistantVersionDTO }；xhrUpload 已按 content-type 解析为对象。
      return (res.body as { version: AssistantVersionDTO }).version
    },
    onSettled: () => { void client.invalidateQueries({ queryKey: VERSION_LIST_KEY }) },
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
