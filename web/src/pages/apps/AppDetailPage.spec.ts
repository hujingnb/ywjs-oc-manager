import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import AppDetailPage from './AppDetailPage.vue'

// 实例详情标题只展示业务可读名称，不把实例 UUID 作为主视觉信息展示给用户。
vi.mock('@/api/hooks/useApps', () => ({
  useAppQuery: () => ({
    data: ref({
      id: '00000000-0000-0000-0000-000000000001',
      org_id: '00000000-0000-0000-0000-000000000101',
      owner_user_id: '00000000-0000-0000-0000-000000000201',
      name: '测试实例',
      status: 'running',
      persona_mode: 'org_inherited',
      api_key_status: 'active',
    }),
    isLoading: ref(false),
    error: ref(null),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    isPlatformAdmin: false,
  }),
}))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => ({ params: { appId: '00000000-0000-0000-0000-000000000001' }, path: '/apps/00000000-0000-0000-0000-000000000001/overview' }),
    useRouter: () => ({ push: vi.fn() }),
    RouterView: { template: '<section />' },
  }
})

describe('AppDetailPage', () => {
  // 覆盖实例详情页业务 tab 导航，确保 Task 6 新增的定时任务入口对组织用户可见。
  it('渲染定时任务 tab 入口', () => {
    const wrapper = mount(AppDetailPage, {
      global: {
        stubs: {
          AppStatusTag: { template: '<span />' },
          NCard: { template: '<section><slot name="header" /><slot /></section>' },
          NTabs: { template: '<nav><slot /></nav>' },
          NTabPane: true,
        },
      },
    })

    expect(wrapper.text()).toContain('定时任务')
  })

  // 覆盖实例详情标题展示规则，避免 UUID 泄露到主视觉标题。
  it('标题展示实例名称且不展示实例 UUID', () => {
    const wrapper = mount(AppDetailPage, {
      global: {
        stubs: {
          AppStatusTag: { template: '<span />' },
          NCard: { template: '<section><slot name="header" /><slot /></section>' },
          NTabs: { template: '<nav><slot /></nav>' },
          NTabPane: true,
        },
      },
    })

    expect(wrapper.text()).toContain('测试实例')
    expect(wrapper.text()).not.toContain('00000000-0000-0000-0000-000000000001')
  })
})
