// useSkills.ts — skill 相关 API hooks，覆盖平台库管理、市场浏览和实例 skill 装/卸/更新。
// 所有 JSON 接口走 apiRequest；平台库上传（multipart）走 xhrUpload 支持进度回调。
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed } from 'vue'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import { xhrUpload } from '@/api/xhrUpload'
import type { AppSkill, PlatformSkill, SkillEntry } from '@/api'
import type { components } from '@/api/generated'

// SkillPage 是市场查询的分页响应，entries 为当前页条目，next_cursor 用于 clawhub 翻页。
export interface SkillPage {
  // 当前页市场条目列表。
  entries?: SkillEntry[]
  // 下一页游标；platform 来源始终为空，clawhub 来源用于翻页。
  next_cursor?: string
}

// InstallSkillInput 是安装 skill 的请求体，对应后端 handlers.InstallAppSkillRequest。
type InstallSkillInput = components['schemas']['handlers.InstallAppSkillRequest']

// updateAppSkillInput 是更新 skill 版本的请求体，对应后端 handlers.UpdateAppSkillRequest。
type UpdateAppSkillInput = components['schemas']['handlers.UpdateAppSkillRequest']

// appSkillKey 是实例 skill 缓存键，invalidate 时统一使用此函数生成，避免拼写分歧。
const appSkillKey = (appId: string | undefined) => ['skills', 'app', appId] as const

// platformSkillKey 是平台库 skill 缓存键。
const platformSkillKey = () => ['skills', 'platform'] as const

// skillMarketKey 是市场搜索缓存键，source/q 变化时自动重查。
const skillMarketKey = (params: { source?: string; q?: string }) =>
  ['skills', 'market', params.source ?? '', params.q ?? ''] as const

// useAppSkillsQuery 拉取指定实例已安装的 skill 列表（含实时对账 status）。
// appId 为 undefined 时不发请求；对账状态（active/pending/builtin/self_created）由后端实时填充。
export function useAppSkillsQuery(appId: Ref<string | undefined>) {
  return useQuery<AppSkill[]>({
    queryKey: computed(() => appSkillKey(appId.value)),
    enabled: () => Boolean(appId.value),
    // 4xx 客户端错误（含「运行时版本过旧」409）是确定性失败，重试无意义且会延迟报错；
    // 仅对 5xx/网络错误重试，让运行时不支持等提示能第一时间呈现。
    retry: (failureCount, error) => {
      const status = (error as { status?: number }).status
      if (typeof status === 'number' && status >= 400 && status < 500) return false
      return failureCount < 3
    },
    queryFn: async () => {
      if (!appId.value) return []
      // GET /api/v1/apps/:appId/skills 直接返回数组，无外层包装键。
      return apiRequest<AppSkill[]>(`/api/v1/apps/${appId.value}/skills`)
    },
  })
}

// useSkillMarketQuery 浏览/搜索 skill 市场（platform 库 + clawhub 公共库聚合），支持游标翻页。
// params.source 过滤来源（"platform"/"clawhub"/""=聚合），params.q 关键词模糊搜索。
// clawhub 每页返回 next_cursor，用 useInfiniteQuery 的 fetchNextPage 追加下一页；
// platform 来源无游标（next_cursor 恒空），故 hasNextPage 自然为 false、不显示「加载更多」。
export function useSkillMarketQuery(params: Ref<{ source?: string; q?: string }>) {
  return useInfiniteQuery<SkillPage>({
    queryKey: computed(() => skillMarketKey(params.value)),
    // 首页游标为空串；source/q 变化时 queryKey 改变，TanStack 自动重置回第一页。
    initialPageParam: '',
    queryFn: async ({ pageParam }) => {
      // GET /api/v1/skill-market 返回 { page: SkillPage }；cursor 透传给后端取下一页。
      const resp = await apiRequest<{ page: SkillPage }>('/api/v1/skill-market', {
        query: {
          source: params.value.source,
          q: params.value.q,
          cursor: (pageParam as string) || undefined,
        },
      })
      return resp.page ?? {}
    },
    // next_cursor 缺失/为空串表示没有更多页，返回 undefined 让 hasNextPage=false。
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
  })
}

// SkillDetailInfo 是 skill 富详情（clawhub 字段最全，platform 只有名称/描述/版本）。
export interface SkillDetailInfo {
  name?: string
  source?: string
  source_ref?: string
  description?: string
  version?: string
  downloads?: number
  stars?: number
  installs?: number
  comments?: number
  license?: string
  keywords?: string[]
  created_at?: string
  updated_at?: string
  author_name?: string
  author_handle?: string
  author_avatar?: string
}
// SkillVersionInfo 是详情页版本列表单项（含更新说明与发布时间）。
export interface SkillVersionInfo {
  version: string
  changelog?: string
  published_at?: number
}
// SkillDetailResponse 是详情端点的响应：富详情 + 版本列表。
export interface SkillDetailResponse {
  detail: SkillDetailInfo
  versions: SkillVersionInfo[]
}

// useSkillDetailQuery 查询某 skill（source+ref）的富详情 + 版本列表，供详情抽屉展示。
// 仅当 source 与 ref 都存在时才发请求（builtin/self_created 无来源标识，详情用容器侧 description）。
export function useSkillDetailQuery(params: Ref<{ source?: string; ref?: string }>) {
  return useQuery<SkillDetailResponse>({
    queryKey: computed(() => ['skills', 'detail', params.value.source ?? '', params.value.ref ?? '']),
    enabled: () => Boolean(params.value.source && params.value.ref),
    queryFn: async () => {
      // GET /api/v1/skill-market/detail?source=&ref= 返回 { detail, versions }。
      const resp = await apiRequest<SkillDetailResponse>('/api/v1/skill-market/detail', {
        query: { source: params.value.source, ref: params.value.ref },
      })
      return resp ?? { detail: {}, versions: [] }
    },
  })
}

// useInstallAppSkill 安装一个 skill 到指定实例（POST /api/v1/apps/:appId/skills）。
// onSuccess 使安装后列表自动刷新，确保对账 status 第一时间可见。
export function useInstallAppSkill(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: InstallSkillInput) => {
      if (!appId.value) throw new Error('缺少实例 ID')
      // POST body：{ source, source_ref, name, version }，后端直接返回已安装 skill 结果。
      return apiRequest<AppSkill>(`/api/v1/apps/${appId.value}/skills`, {
        method: 'POST',
        body: input,
      })
    },
    onSuccess: () => {
      // 安装成功后 invalidate 该实例的 skill 列表，让 useAppSkillsQuery 自动重拉。
      void client.invalidateQueries({ queryKey: appSkillKey(appId.value) })
    },
  })
}

// useUninstallAppSkill 按 skillName 卸载实例 skill（DELETE /api/v1/apps/:appId/skills/:skillName）。
// 受版本保护的 skill（protected=true）后端会返回 403，前端应在按钮级别隐藏。
export function useUninstallAppSkill(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (skillName: string) => {
      if (!appId.value) throw new Error('缺少实例 ID')
      // 204 无响应体，apiRequest 在 status=204 时返回 undefined。
      await apiRequest<void>(`/api/v1/apps/${appId.value}/skills/${skillName}`, { method: 'DELETE' })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: appSkillKey(appId.value) })
    },
  })
}

// useUpdateAppSkill 将已安装的 skill 更新到目标版本（POST /api/v1/apps/:appId/skills/:skillName/update）。
// latest_version 非空时前端可展示「更新」按钮，调用此 hook 传入 skillName + version。
export function useUpdateAppSkill(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: UpdateAppSkillInput & { name: string }) => {
      if (!appId.value) throw new Error('缺少实例 ID')
      // POST body：{ version }；skillName 拼在路径里。
      const { name, ...body } = input
      return apiRequest<AppSkill>(`/api/v1/apps/${appId.value}/skills/${name}/update`, {
        method: 'POST',
        body,
      })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: appSkillKey(appId.value) })
    },
  })
}

// useReinstallAppSkill 对 pending 状态的 skill 重新触发热装 + reload
// （POST /api/v1/apps/:appId/skills/:skillName/reinstall）。
// 首次安装时 oc-ops 热装 / reload 失败会落 pending；用户点「重新安装」调此 hook 重试，成功后转 active。
export function useReinstallAppSkill(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (skillName: string) => {
      if (!appId.value) throw new Error('缺少实例 ID')
      // POST 无 body；skillName 拼在路径里。
      return apiRequest<AppSkill>(`/api/v1/apps/${appId.value}/skills/${skillName}/reinstall`, {
        method: 'POST',
      })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: appSkillKey(appId.value) })
    },
  })
}

// usePlatformSkillsQuery 拉取平台库所有 skill（GET /api/v1/platform-skills）。
// 仅 platform_admin 有权限，非管理员后端会 403。
export function usePlatformSkillsQuery() {
  return useQuery<PlatformSkill[]>({
    queryKey: platformSkillKey(),
    queryFn: async () => {
      // GET /platform-skills 返回 { skills: PlatformSkillResult[] }。
      const resp = await apiRequest<{ skills: PlatformSkill[] }>('/api/v1/platform-skills')
      return resp.skills ?? []
    },
  })
}

// PlatformSkillUploadInput 描述平台库上传请求的字段。
export interface PlatformSkillUploadInput {
  // skill 名称，后端作为唯一键之一。
  name: string
  // 版本号，与 name 组成联合唯一约束。
  version: string
  // skill 描述，可选。
  description?: string
  // skill tar 归档文件。
  file: File
  // 上传进度回调（loaded, total），调用方用于展示进度条。
  onProgress?: (loaded: number, total: number) => void
  // 取消信号，调用方在用户点击取消时 abort。
  signal?: AbortSignal
}

// useUploadPlatformSkill 上传 skill tar 到平台库（POST /api/v1/platform-skills multipart）。
// 走 xhrUpload 以支持上传进度回调；成功后 invalidate 平台库列表。
export function useUploadPlatformSkill() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: PlatformSkillUploadInput) => {
      // 构造 multipart FormData：name/version/description 为文本字段，file 为二进制文件。
      const form = new FormData()
      form.append('name', input.name)
      form.append('version', input.version)
      if (input.description) {
        form.append('description', input.description)
      }
      form.append('file', input.file)

      const resp = await xhrUpload('/api/v1/platform-skills', {
        method: 'POST',
        body: form,
        onProgress: input.onProgress,
        signal: input.signal,
      })
      // POST /platform-skills 返回 { skill: PlatformSkillResult }。
      return (resp.body as { skill: PlatformSkill }).skill
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: platformSkillKey() })
    },
  })
}

// useDeletePlatformSkill 按 id 删除平台库 skill（DELETE /api/v1/platform-skills/:id）。
// 204 无响应体；成功后 invalidate 平台库列表。
export function useDeletePlatformSkill() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      await apiRequest<void>(`/api/v1/platform-skills/${id}`, { method: 'DELETE' })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: platformSkillKey() })
    },
  })
}

// 导出内部缓存键函数，供单测断言 invalidate 行为时复用。
export const _appSkillKey = appSkillKey
export const _platformSkillKey = platformSkillKey
export const _skillMarketKey = skillMarketKey
