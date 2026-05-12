import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import AppDetailPage from './AppDetailPage.vue'

// 应用详情标题只展示业务可读名称，不把应用 UUID 作为主视觉信息展示给用户。
vi.mock('@/api/hooks/useApps', () => ({
  useAppQuery: () => ({
    data: ref({
      id: '00000000-0000-0000-0000-000000000001',
      org_id: '00000000-0000-0000-0000-000000000101',
      owner_user_id: '00000000-0000-0000-0000-000000000201',
      name: '测试应用',
      status: 'running',
      persona_mode: 'org_inherited',
      api_key_status: 'active',
    }),
    isLoading: ref(false),
    error: ref(null),
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
  it('标题展示应用名称且不展示应用 UUID', () => {
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

    expect(wrapper.text()).toContain('测试应用')
    expect(wrapper.text()).not.toContain('00000000-0000-0000-0000-000000000001')
  })
})
