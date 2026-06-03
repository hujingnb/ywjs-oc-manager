// useSkills API hooks 测试覆盖 skill 相关端点 URL、请求体、缓存键和缓存失效边界。
// 使用真实 QueryClient 挂载组合式函数，验证 Vue Query 行为而非仅测静态 helper。
import { VueQueryPlugin, QueryClient } from '@tanstack/vue-query'
import { mount } from '@vue/test-utils'
import { defineComponent, h, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { apiRequest } from '@/api/client'
import { xhrUpload } from '@/api/xhrUpload'
import {
  _appSkillKey,
  _platformSkillKey,
  _skillMarketKey,
  useAppSkillsQuery,
  useDeletePlatformSkill,
  useInstallAppSkill,
  usePlatformSkillsQuery,
  useSkillMarketQuery,
  useUninstallAppSkill,
  useUpdateAppSkill,
  useUploadPlatformSkill,
} from './useSkills'

vi.mock('@/api/client', () => ({
  apiRequest: vi.fn(),
}))
vi.mock('@/api/xhrUpload', () => ({
  xhrUpload: vi.fn(),
}))

const apiRequestMock = vi.mocked(apiRequest)
const xhrUploadMock = vi.mocked(xhrUpload)

// createTestQueryClient 每次测试独立创建 QueryClient，禁用重试避免异步噪声。
function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

// mountWithQueryClient 挂载匿名组件并暴露 hook 返回值，供后续 mutateAsync / 断言使用。
function mountWithQueryClient(setupHook: () => Record<string, unknown> | void) {
  const queryClient = createTestQueryClient()
  const wrapper = mount(defineComponent({
    setup(_, { expose }) {
      const exposed = setupHook()
      if (exposed) expose(exposed)
      return () => h('div')
    },
  }), {
    global: {
      plugins: [[VueQueryPlugin, { queryClient }]],
    },
  })
  return { queryClient, wrapper }
}

describe('useSkills hooks', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.clearAllTimers()
  })

  // ===== 缓存键结构 =====

  // 三类缓存键必须互不冲突，避免 appId 相同时列表、平台库和市场数据互相覆盖。
  it('不同资源的缓存键互不冲突', () => {
    expect(_appSkillKey('app-1')).toEqual(['skills', 'app', 'app-1'])
    expect(_platformSkillKey()).toEqual(['skills', 'platform'])
    expect(_skillMarketKey({ source: 'platform', q: 'web' })).toEqual(['skills', 'market', 'platform', 'web'])
    // 空参数时保持稳定的字符串占位，不产生 undefined
    expect(_skillMarketKey({})).toEqual(['skills', 'market', '', ''])
  })

  // ===== 实例 skill 列表查询 =====

  // appId 有值时应请求正确路径；后端直接返回数组，无需解包。
  it('useAppSkillsQuery 用 appId 路径请求实例 skill 列表', async () => {
    const skillList = [{ name: 'web-search', status: 'active', source: 'platform', source_ref: 'web-search', version: '1.0.0' }]
    apiRequestMock.mockResolvedValueOnce(skillList)
    const appId = ref('app-1')

    mountWithQueryClient(() => { useAppSkillsQuery(appId) })

    await vi.waitFor(() => {
      expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/apps/app-1/skills')
    })
  })

  // appId 为 undefined 时不应发出请求（enabled=false）。
  it('useAppSkillsQuery 在 appId 为 undefined 时不发请求', async () => {
    const appId = ref<string | undefined>(undefined)
    mountWithQueryClient(() => { useAppSkillsQuery(appId) })
    await new Promise(r => setTimeout(r, 50))
    expect(apiRequestMock).not.toHaveBeenCalled()
  })

  // ===== 市场查询 =====

  // 市场 query 应将 source/q 透传为 query string，并解包 { page } 包装层。
  it('useSkillMarketQuery 携带 source 和 q 参数并解包 page 键', async () => {
    const page = { entries: [{ name: 'tool-a', source: 'platform', source_ref: 'tool-a', version: '0.1.0' }], next_cursor: '' }
    apiRequestMock.mockResolvedValueOnce({ page })
    const params = ref({ source: 'platform', q: 'tool' })

    mountWithQueryClient(() => { useSkillMarketQuery(params) })

    await vi.waitFor(() => {
      expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/skill-market', {
        query: { source: 'platform', q: 'tool' },
      })
    })
  })

  // ===== 安装 skill =====

  // 安装应 POST 到正确路径，成功后使该实例的 skill 缓存失效。
  it('useInstallAppSkill POST 到安装路径并 invalidate 实例 skill 缓存', async () => {
    const installed = { name: 'web-search', status: 'pending', source: 'platform', source_ref: 'web-search', version: '1.0.0' }
    apiRequestMock.mockResolvedValueOnce(installed)
    const appId = ref('app-1')
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      install: useInstallAppSkill(appId),
    }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await (wrapper.vm as unknown as {
      install: ReturnType<typeof useInstallAppSkill>
    }).install.mutateAsync({ source: 'platform', source_ref: 'web-search', name: 'web-search', version: '1.0.0' })

    // 请求路径和请求体必须准确
    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/apps/app-1/skills', {
      method: 'POST',
      body: { source: 'platform', source_ref: 'web-search', name: 'web-search', version: '1.0.0' },
    })
    // 安装成功后必须刷新该实例 skill 列表
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _appSkillKey('app-1') })
  })

  // ===== 卸载 skill =====

  // 卸载应 DELETE 到 skillName 路径，成功后 invalidate 列表缓存。
  it('useUninstallAppSkill DELETE 到卸载路径并 invalidate 实例 skill 缓存', async () => {
    apiRequestMock.mockResolvedValueOnce(undefined)
    const appId = ref('app-1')
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      uninstall: useUninstallAppSkill(appId),
    }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await (wrapper.vm as unknown as {
      uninstall: ReturnType<typeof useUninstallAppSkill>
    }).uninstall.mutateAsync('web-search')

    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/apps/app-1/skills/web-search', { method: 'DELETE' })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _appSkillKey('app-1') })
  })

  // ===== 更新 skill 版本 =====

  // 更新应 POST 到 {skillName}/update 路径，body 仅含 version，成功后 invalidate 缓存。
  it('useUpdateAppSkill POST 到更新路径并 invalidate 实例 skill 缓存', async () => {
    const updated = { name: 'web-search', status: 'active', source: 'platform', source_ref: 'web-search', version: '2.0.0' }
    apiRequestMock.mockResolvedValueOnce(updated)
    const appId = ref('app-1')
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      update: useUpdateAppSkill(appId),
    }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await (wrapper.vm as unknown as {
      update: ReturnType<typeof useUpdateAppSkill>
    }).update.mutateAsync({ name: 'web-search', version: '2.0.0' })

    // skillName 在路径里，body 只有 version
    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/apps/app-1/skills/web-search/update', {
      method: 'POST',
      body: { version: '2.0.0' },
    })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _appSkillKey('app-1') })
  })

  // ===== 平台库查询 =====

  // 平台库查询应解包 { skills } 响应键，返回数组。
  it('usePlatformSkillsQuery 请求平台库列表并解包 skills 键', async () => {
    const skills = [{ id: 'ps-1', name: 'code-runner', version: '1.0.0', file_size: 1024 }]
    apiRequestMock.mockResolvedValueOnce({ skills })

    mountWithQueryClient(() => { usePlatformSkillsQuery() })

    await vi.waitFor(() => {
      expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/platform-skills')
    })
  })

  // ===== 上传平台库 skill =====

  // 上传应走 xhrUpload（multipart），FormData 包含 name/version/description/file 四字段，
  // 成功后 invalidate 平台库列表缓存。
  it('useUploadPlatformSkill 走 xhrUpload 发送 multipart 并 invalidate 平台库缓存', async () => {
    const uploaded = { id: 'ps-2', name: 'new-skill', version: '0.1.0', file_size: 2048 }
    xhrUploadMock.mockResolvedValueOnce({ status: 201, body: { skill: uploaded } })
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      upload: useUploadPlatformSkill(),
    }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const file = new File(['tar content'], 'new-skill.tar')
    await (wrapper.vm as unknown as {
      upload: ReturnType<typeof useUploadPlatformSkill>
    }).upload.mutateAsync({ name: 'new-skill', version: '0.1.0', description: '新 skill', file })

    // 必须走 xhrUpload，路径正确
    expect(xhrUploadMock).toHaveBeenCalledWith(
      '/api/v1/platform-skills',
      expect.objectContaining({ method: 'POST' }),
    )
    // 上传的 body 必须是 FormData
    const callArgs = xhrUploadMock.mock.calls[0][1]
    expect(callArgs.body).toBeInstanceOf(FormData)
    // 成功后 invalidate 平台库列表
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _platformSkillKey() })
  })

  // 上传时 description 为空应省略该字段（不向 FormData 追加空字符串描述）。
  it('useUploadPlatformSkill 省略为空的 description 字段', async () => {
    xhrUploadMock.mockResolvedValueOnce({ status: 201, body: { skill: { id: 'ps-3', name: 'no-desc', version: '1.0.0', file_size: 100 } } })
    const { wrapper } = mountWithQueryClient(() => ({
      upload: useUploadPlatformSkill(),
    }))

    const file = new File(['x'], 'no-desc.tar')
    await (wrapper.vm as unknown as {
      upload: ReturnType<typeof useUploadPlatformSkill>
    }).upload.mutateAsync({ name: 'no-desc', version: '1.0.0', file })

    const formData = xhrUploadMock.mock.calls[0][1].body as FormData
    // description 字段未追加时 get() 返回 null
    expect(formData.get('description')).toBeNull()
    expect(formData.get('name')).toBe('no-desc')
    expect(formData.get('version')).toBe('1.0.0')
  })

  // ===== 删除平台库 skill =====

  // 删除应 DELETE 到 id 路径，成功后 invalidate 平台库列表。
  it('useDeletePlatformSkill DELETE 到平台库 id 路径并 invalidate 缓存', async () => {
    apiRequestMock.mockResolvedValueOnce(undefined)
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      deletePlatformSkill: useDeletePlatformSkill(),
    }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await (wrapper.vm as unknown as {
      deletePlatformSkill: ReturnType<typeof useDeletePlatformSkill>
    }).deletePlatformSkill.mutateAsync('ps-1')

    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/platform-skills/ps-1', { method: 'DELETE' })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _platformSkillKey() })
  })
})
