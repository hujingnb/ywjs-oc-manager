import { mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import AICCConsoleLayout from './AICCConsoleLayout.vue'

const routerPush = vi.hoisted(() => vi.fn())
const routeState = vi.hoisted(() => ({ path: '/aicc-console' }))

vi.mock('vue-router', () => ({
  RouterView: { template: '<main data-test="router-view">AICC 子页面</main>' },
  useRoute: () => routeState,
  useRouter: () => ({ push: routerPush }),
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

const MenuStub = defineComponent({
  props: ['options', 'value'],
  emits: ['update:value'],
  setup(props, { emit }) {
    return () => h('nav', { 'data-test': 'aicc-nav', 'data-value': props.value }, (props.options as { key: string; label: string }[]).map(option => (
      h('button', {
        'data-test': 'aicc-nav-item',
        'data-key': option.key,
        onClick: () => emit('update:value', option.key),
      }, option.label)
    )))
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
        NMenu: MenuStub,
        Button: ButtonStub,
        Menu: MenuStub,
        'n-button': ButtonStub,
        'n-menu': MenuStub,
      },
    },
  })
}

function navLabels(wrapper: ReturnType<typeof mountLayout>) {
  return wrapper.findAll('[data-test="aicc-nav-item"]').map(item => item.text())
}

function navKeys(wrapper: ReturnType<typeof mountLayout>) {
  return wrapper.findAll('[data-test="aicc-nav-item"]').map(item => item.attributes('data-key'))
}

describe('AICCConsoleLayout', () => {
  beforeEach(() => {
    routeState.path = '/aicc-console'
    routerPush.mockClear()
  })

  // 覆盖独立客服工作台骨架：顶部栏、内部导航和子路由出口必须同时存在，避免回落到主后台菜单。
  it('renders the independent AICC console shell with internal navigation and routed content', () => {
    const wrapper = mountLayout()

    expect(wrapper.text()).toContain('AI Contact Center')
    expect(wrapper.text()).toContain('AICC 工作台')
    expect(wrapper.find('[data-test="locale-switcher"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="router-view"]').text()).toBe('AICC 子页面')
    expect(navLabels(wrapper)).toEqual(['接待台', '会话', '知识库', '线索', '分析', '设置'])
    expect(navKeys(wrapper)).toEqual([
      '/aicc-console',
      '/aicc-console/sessions',
      '/aicc-console/knowledge',
      '/aicc-console/leads',
      '/aicc-console/analytics',
      '/aicc-console/settings',
    ])
  })

  // 覆盖返回入口行为：独立工作台的“返回概览”按钮必须回到企业概览页，而不是浏览器历史或旧 /aicc 路由。
  it('returns to the enterprise overview when clicking the overview button', async () => {
    const wrapper = mountLayout()

    await wrapper.findAll('button').find(button => button.text().includes('返回概览'))!.trigger('click')

    expect(routerPush).toHaveBeenCalledWith('/')
  })

  // 覆盖阶段性导航完整性：Task 4 拆页前，所有已展示的工作台入口都必须跳到已约定的 /aicc-console 子路径。
  it('pushes every visible console navigation item to its registered AICC console path', async () => {
    const wrapper = mountLayout()
    const expectedTargets = [
      ['会话', '/aicc-console/sessions'],
      ['知识库', '/aicc-console/knowledge'],
      ['线索', '/aicc-console/leads'],
      ['分析', '/aicc-console/analytics'],
      ['设置', '/aicc-console/settings'],
    ] as const

    for (const [label, path] of expectedTargets) {
      routerPush.mockClear()
      const navItem = wrapper.findAll('[data-test="aicc-nav-item"]').find(item => item.text() === label)

      expect(navItem?.attributes('data-key')).toBe(path)
      await navItem!.trigger('click')
      expect(routerPush).toHaveBeenCalledWith(path)
    }
  })
})
