// useMembers API hooks 测试覆盖成员相关 mutation 的缓存失效边界。
// 使用真实 QueryClient 挂载组合式函数，验证 onSuccess 失效的 queryKey 集合完整无遗漏。
import { VueQueryPlugin, QueryClient } from '@tanstack/vue-query'
import { mount } from '@vue/test-utils'
import { defineComponent, h, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { apiRequest } from '@/api/client'
import { _appsKeys } from '@/api/hooks/useApps'
import { useCreateMemberApp } from './useMembers'

vi.mock('@/api/client', () => ({
  apiRequest: vi.fn(),
}))

const apiRequestMock = vi.mocked(apiRequest)

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
  const wrapper = mount(
    defineComponent({
      setup(_, { expose }) {
        const exposed = setupHook()
        if (exposed) expose(exposed)
        return () => h('div')
      },
    }),
    {
      global: {
        plugins: [[VueQueryPlugin, { queryClient }]],
      },
    },
  )
  return { queryClient, wrapper }
}

describe('useMembers hooks', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.clearAllTimers()
  })

  // 回归测试：建实例后必须同时失效组织应用列表缓存，否则双视角 useOwnApp 依赖的
  // ['apps','org',orgId] 读到陈旧数据，切「我的实例」会误判无实例、落到空状态页（需刷新才恢复）。
  it('useCreateMemberApp 成功后同时失效成员列表与组织应用列表缓存', async () => {
    // mock apiRequest 返回建实例成功响应
    const mockResult = {
      member_app: {
        app: { id: 'app-1', org_id: 'org-1', owner_user_id: 'u-1', name: '测试实例', status: 'draft', api_key_status: 'pending', knowledge_quota_bytes: 0 },
        job_id: 'job-1',
      },
    }
    apiRequestMock.mockResolvedValueOnce(mockResult)

    const orgId = ref<string | undefined>('org-1')
    const { queryClient, wrapper } = mountWithQueryClient(() => ({
      createApp: useCreateMemberApp(orgId),
    }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    // 触发 mutate
    await (
      wrapper.vm as unknown as { createApp: ReturnType<typeof useCreateMemberApp> }
    ).createApp.mutateAsync({
      userId: 'u-1',
      payload: { app_name: '测试实例', version_id: 'v-1' },
    })

    // 1. 成员列表缓存必须被失效，保证管理员页面成员行刷新
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['members', 'org-1'] })
    // 2. 组织应用列表缓存必须被失效，否则 useOwnApp（双视角）读到旧的「无 app」状态
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: _appsKeys.orgKey('org-1') })
    // 确认两个 key 都是独立调用（invalidateQueries 被调用两次，不是只有一次）
    expect(invalidateSpy).toHaveBeenCalledTimes(2)
  })
})
