import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import AppDetailPage from './AppDetailPage.vue'

const authState = vi.hoisted(() => ({
  isPlatformAdmin: false,
  isOrgMember: false,
}))

// 实例详情标题只展示业务可读名称，不把实例 UUID 作为主视觉信息展示给用户。
vi.mock('@/api/hooks/useApps', () => ({
  useAppQuery: () => ({
    data: ref({
      id: '00000000-0000-0000-0000-000000000001',
      org_id: '00000000-0000-0000-0000-000000000101',
      owner_user_id: '00000000-0000-0000-0000-000000000201',
      name: '测试实例',
      status: 'running',
      api_key_status: 'active',
    }),
    isLoading: ref(false),
    error: ref(null),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
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

function mountDetail() {
  return mount(AppDetailPage, {
    global: {
      stubs: {
        AppStatusTag: { template: '<span />' },
        NCard: { template: '<section><slot name="header" /><slot /></section>' },
      },
      plugins: [i18n],
    },
  })
}

describe('AppDetailPage', () => {
  beforeEach(() => {
    // 每次用例前将 i18n 语言设为中文，确保断言中文文案的测试与翻译文件对齐。
    i18n.global.locale.value = 'zh'
    authState.isPlatformAdmin = false
    authState.isOrgMember = false
  })

  // 覆盖管理员/组织管理员视角：实例详情页仍保留业务 tab 导航。
  it('非组织成员保留实例详情 tab 入口', () => {
    const wrapper = mountDetail()

    expect(wrapper.find('.tab-nav').exists()).toBe(true)
    expect(wrapper.text()).toContain('定时任务')
  })

  // 覆盖组织成员视角：实例能力已拉平到左侧菜单，详情页顶部不再重复显示 tab。
  it('组织成员隐藏实例详情 tab 入口', () => {
    authState.isOrgMember = true

    const wrapper = mountDetail()

    expect(wrapper.find('.tab-nav').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('定时任务')
  })

  // 覆盖实例详情标题展示规则，避免 UUID 作为主视觉标题替代业务名称。
  it('标题展示实例名称且不展示实例 UUID', () => {
    const wrapper = mountDetail()

    expect(wrapper.find('h2').text()).toBe('测试实例')
    expect(wrapper.find('h2').text()).not.toContain('00000000-0000-0000-0000-000000000001')
  })

  // 覆盖平台管理员排障场景：详情页头部应直接展示实例 UUID，便于跨系统定位资源。
  it('平台管理员在实例详情页看到实例 UUID', () => {
    authState.isPlatformAdmin = true

    const wrapper = mountDetail()

    expect(wrapper.text()).toContain('实例 UUID')
    expect(wrapper.text()).toContain('00000000-0000-0000-0000-000000000001')
  })
})
