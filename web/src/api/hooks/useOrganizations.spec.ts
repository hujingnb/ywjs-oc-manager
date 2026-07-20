// useOrganizations API hooks 测试覆盖独立企业 AICC 配置的端点、响应解包和缓存失效。
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import { mount } from '@vue/test-utils'
import { defineComponent, h, ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { apiRequest } from '@/api/client'
import { useOrganizationAICCConfigQuery, useUpdateOrganizationAICCConfig } from './useOrganizations'

vi.mock('@/api/client', () => ({ apiRequest: vi.fn() }))

const apiRequestMock = vi.mocked(apiRequest)

// mountHook 使用禁用重试的真实 QueryClient，直接验证 Vue Query 行为。
function mountHook(setupHook: () => Record<string, unknown>) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const wrapper = mount(defineComponent({
    setup(_, { expose }) {
      expose(setupHook())
      return () => h('div')
    },
  }), {
    global: { plugins: [[VueQueryPlugin, { queryClient }]] },
  })
  return { queryClient, wrapper }
}

describe('useOrganizations AICC config hooks', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  // 独立配置查询必须请求 GET 端点并从 config envelope 解包完整快照。
  it('读取企业 AICC 独立配置并解包 config', async () => {
    const config = {
      org_id: 'org-1', enabled: true, model: 'qwen3.5:27b', agent_limit: 5,
      revision: 3, industry_knowledge_bases: [{ id: 'industry-1', name: '行业库 A' }],
    }
    apiRequestMock.mockResolvedValueOnce({ config })
    const orgId = ref<string | undefined>('org-1')
    const { wrapper } = mountHook(() => ({ query: useOrganizationAICCConfigQuery(orgId) }))

    await vi.waitFor(() => {
      expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/organizations/org-1/aicc-config')
      expect((wrapper.vm as unknown as { query: { data: { value: typeof config } } }).query.data.value).toEqual(config)
    })
  })

  // 更新配置必须使用 PUT 完整载荷，并同时失效组织列表和该企业配置缓存。
  it('PUT 企业 AICC 配置后失效列表与独立配置缓存', async () => {
    const config = {
      org_id: 'org-1', enabled: true, model: 'qwen3.5:27b', agent_limit: 5,
      revision: 4, industry_knowledge_bases: [{ id: 'industry-1', name: '行业库 A' }],
    }
    apiRequestMock.mockResolvedValueOnce({ config })
    const { queryClient, wrapper } = mountHook(() => ({ mutation: useUpdateOrganizationAICCConfig() }))
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    const payload = {
      enabled: true,
      model: 'qwen3.5:27b',
      agent_limit: 5,
      industry_knowledge_base_ids: ['industry-1'],
    }

    const result = await (
      wrapper.vm as unknown as { mutation: ReturnType<typeof useUpdateOrganizationAICCConfig> }
    ).mutation.mutateAsync({ id: 'org-1', payload })

    expect(apiRequestMock).toHaveBeenCalledWith('/api/v1/organizations/org-1/aicc-config', {
      method: 'PUT',
      body: payload,
    })
    expect(result).toEqual(config)
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['organizations'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['organization-aicc-config', 'org-1'] })
  })
})
