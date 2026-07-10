import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import AICCConsoleLayout from './AICCConsoleLayout.vue'

interface OrganizationFixture {
  id: string
  name: string
  status: string
  code: string
  aicc_enabled: boolean
}

const routerPush = vi.hoisted(() => vi.fn())
const routerReplace = vi.hoisted(() => vi.fn())
const authState = vi.hoisted(() => ({
  user: { id: 'owner-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' },
}))
const organizationState = vi.hoisted(() => {
  const { ref } = require('vue') as typeof import('vue')

  return {
    data: ref<OrganizationFixture | undefined>({
      id: 'org-1',
      name: '测试企业',
      status: 'enabled',
      code: 'test-org',
      aicc_enabled: true,
    }),
    isLoading: ref(false),
  }
})

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: routerPush, replace: routerReplace }),
}))

vi.mock('./AICCConsoleWorkspace.vue', () => ({
  default: { template: '<main data-test="aicc-workspace">AICC 工作区</main>' },
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationQuery: () => organizationState,
}))

// LocaleSwitcherStub：独立工作台只验证语言切换器插槽位置，不挂载真实本地化存储逻辑。
const LocaleSwitcherStub = {
  props: ['persist'],
  template: '<div data-test="locale-switcher" />',
}

const ButtonStub = defineComponent({
  emits: ['click'],
  setup(_, { slots, emit }) {
    return () => h('button', {
      onClick: () => emit('click'),
    }, [slots.icon?.(), slots.default?.()])
  },
})

const LayoutSiderStub = defineComponent({
  setup(_, { slots }) {
    return () => h('aside', { 'data-test': 'legacy-layout-sider' }, slots.default?.())
  },
})

const MenuStub = defineComponent({
  setup(_, { slots }) {
    return () => h('nav', { 'data-test': 'legacy-layout-menu' }, slots.default?.())
  },
})

// mountLayout：使用中文 i18n 挂载工作台外壳，便于直接断言用户可见文案。
function mountLayout() {
  i18n.global.locale.value = 'zh'
  return mount(AICCConsoleLayout, {
    global: {
      plugins: [i18n],
      stubs: {
        LocaleSwitcher: LocaleSwitcherStub,
        NButton: ButtonStub,
        Button: ButtonStub,
        'n-button': ButtonStub,
        NLayoutSider: LayoutSiderStub,
        LayoutSider: LayoutSiderStub,
        'n-layout-sider': LayoutSiderStub,
        NMenu: MenuStub,
        Menu: MenuStub,
        'n-menu': MenuStub,
      },
    },
  })
}

describe('AICCConsoleLayout', () => {
  beforeEach(() => {
    routerPush.mockClear()
    routerReplace.mockClear()
    authState.user = { id: 'owner-1', username: 'owner', display_name: '管理员', role: 'org_admin', org_id: 'org-1' }
    organizationState.data.value = {
      id: 'org-1',
      name: '测试企业',
      status: 'enabled',
      code: 'test-org',
      aicc_enabled: true,
    }
    organizationState.isLoading.value = false
  })

  // 覆盖独立客服工作台骨架：外壳只负责品牌栏和开通后的工作区挂载，模块导航交给工作区承载。
  it('renders the independent AICC console shell with enabled workspace content', () => {
    const wrapper = mountLayout()

    expect(wrapper.text()).toContain('AI Contact Center')
    expect(wrapper.text()).toContain('AICC 工作台')
    expect(wrapper.find('[data-test="locale-switcher"]').exists()).toBe(true)
    expect(wrapper.findAll('button').some(button => button.text().includes('返回概览'))).toBe(true)
    expect(wrapper.find('[data-test="aicc-workspace"]').text()).toBe('AICC 工作区')
    expect(wrapper.find('[data-test="legacy-layout-sider"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="legacy-layout-menu"]').exists()).toBe(false)
  })

  // 覆盖返回入口行为：独立工作台的“返回概览”按钮必须回到企业概览页，而不是浏览器历史或旧 /aicc 路由。
  it('returns to the enterprise overview when clicking the overview button', async () => {
    const wrapper = mountLayout()

    await wrapper.findAll('button').find(button => button.text().includes('返回概览'))!.trigger('click')

    expect(routerPush).toHaveBeenCalledWith('/')
  })

  // 覆盖未开通企业直接访问兜底：即使用户手动输入 /aicc-console，也会回到概览页。
  it('redirects disabled organizations back to overview', async () => {
    organizationState.data.value = {
      id: 'org-1',
      name: '测试企业',
      status: 'enabled',
      code: 'test-org',
      aicc_enabled: false,
    }

    const wrapper = mountLayout()
    await nextTick()

    expect(routerReplace).toHaveBeenCalledWith('/')
    expect(wrapper.find('[data-test="aicc-workspace"]').exists()).toBe(false)
  })

  // 覆盖开通状态加载期间的访问保护：企业状态未知时不能提前挂载工作区，避免工作区抢先请求 AICC API。
  it('does not render workspace content while organization enablement is loading', () => {
    organizationState.data.value = undefined
    organizationState.isLoading.value = true

    const wrapper = mountLayout()

    expect(wrapper.find('[data-test="aicc-workspace"]').exists()).toBe(false)
    expect(routerReplace).not.toHaveBeenCalled()
  })
})
