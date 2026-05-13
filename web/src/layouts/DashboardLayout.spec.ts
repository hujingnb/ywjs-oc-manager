import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { NLayoutContent } from 'naive-ui'

import DashboardLayout from './DashboardLayout.vue'

vi.mock('vue-router', () => ({
  RouterView: { template: '<section class="route-page">页面内容</section>' },
  useRoute: () => ({ path: '/runtime-nodes' }),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: { username: 'admin', display_name: 'admin', role: 'platform_admin' },
    isPlatformAdmin: true,
    isOrgMember: false,
    logout: vi.fn(),
  }),
}))

// DashboardLayout 负责所有登录后页面的视口骨架，内容区必须给子页面提供可撑满的剩余高度。
describe('DashboardLayout', () => {
  it('wraps routed pages in a fill-height content frame', () => {
    const wrapper = mount(DashboardLayout, {
      global: {
        stubs: {
          RouterView: { template: '<section class="route-page">页面内容</section>' },
        },
      },
    })
    const content = wrapper.findComponent(NLayoutContent)

    expect(content.props('contentStyle')).toContain('min-height: calc(100vh - 64px)')
    expect(content.props('contentStyle')).toContain('display: flex')
    expect(wrapper.find('.dashboard-page-frame').exists()).toBe(true)
  })
})
