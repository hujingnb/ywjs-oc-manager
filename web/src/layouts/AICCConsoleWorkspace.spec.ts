import { mount } from '@vue/test-utils'
import { defineComponent, h, inject, isReadonly, nextTick } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import type { AICCAgent } from '@/domain/aicc'
import { AICCConsoleContextKey } from '@/pages/aicc/aiccConsoleContext'
import AICCConsoleWorkspace from './AICCConsoleWorkspace.vue'

const routerPush = vi.hoisted(() => vi.fn())
const routeState = vi.hoisted(() => ({ path: '/aicc-console' }))
const agentsState = vi.hoisted(() => {
  const { ref } = require('vue') as typeof import('vue')

  return {
    data: ref<AICCAgent[] | undefined>(undefined),
    isLoading: ref(false),
    error: ref<Error | null>(null),
  }
})

vi.mock('vue-router', () => ({
  RouterView: defineComponent({
    setup() {
      const context = inject(AICCConsoleContextKey)

      return () => h('main', { 'data-test': 'router-view' }, [
        h('span', { 'data-test': 'context-selected-id' }, context?.selectedAgentId.value ?? 'none'),
        h('span', { 'data-test': 'context-selected-agent' }, context?.selectedAgent.value?.name ?? 'none'),
        h('span', { 'data-test': 'context-id-readonly' }, String(context ? isReadonly(context.selectedAgentId) : false)),
        h('button', {
          'data-test': 'select-support',
          onClick: () => context?.selectAgent('agent-support'),
        }, 'select support'),
        h('button', {
          'data-test': 'start-create-agent',
          onClick: () => context?.startCreateAgent(),
        }, 'create'),
      ])
    },
  }),
  useRoute: () => routeState,
  useRouter: () => ({ push: routerPush }),
}))

vi.mock('@/api/hooks/useAICC', () => ({
  useAICCAgentsQuery: () => agentsState,
}))

const ButtonStub = defineComponent({
  props: ['type', 'secondary', 'size'],
  emits: ['click'],
  setup(_, { slots, emit }) {
    return () => h('button', {
      onClick: () => emit('click'),
    }, [slots.icon?.(), slots.default?.()])
  },
})

const SelectStub = defineComponent({
  props: ['value', 'options', 'placeholder', 'loading'],
  emits: ['update:value'],
  setup(props, { emit }) {
    return () => h('select', {
      'data-test': 'agent-switcher',
      value: props.value ?? '',
      onChange: (event: Event) => emit('update:value', (event.target as HTMLSelectElement).value || undefined),
    }, [
      h('option', { value: '' }, props.placeholder as string),
      ...((props.options ?? []) as { label: string; value: string }[]).map(option => (
        h('option', { value: option.value }, option.label)
      )),
    ])
  },
})

const TagStub = defineComponent({
  setup(_, { slots }) {
    return () => h('span', { 'data-test': 'status-tag' }, slots.default?.())
  },
})

// makeAgent：构造最小智能体夹具，字段保持与后端 snake_case 契约一致。
function makeAgent(overrides: Partial<AICCAgent> = {}): AICCAgent {
  return {
    id: 'agent-sales',
    org_id: 'org-1',
    app_id: 'app-1',
    name: '售前接待',
    status: 'active',
    privacy_mode: 'notice',
    retention_days: 180,
    public_token: 'public-token',
    ...overrides,
  }
}

// mountWorkspace：使用中文 i18n 和轻量 Naive UI stub 挂载工作区外壳，避免测试依赖浏览器布局实现。
function mountWorkspace() {
  i18n.global.locale.value = 'zh'
  return mount(AICCConsoleWorkspace, {
    global: {
      plugins: [i18n],
      stubs: {
        NButton: ButtonStub,
        NSelect: SelectStub,
        NTag: TagStub,
        'n-button': ButtonStub,
        'n-select': SelectStub,
        'n-tag': TagStub,
      },
    },
  })
}

function navItems(wrapper: ReturnType<typeof mountWorkspace>) {
  return wrapper.findAll('[data-test="workspace-nav-item"]')
}

describe('AICCConsoleWorkspace', () => {
  beforeEach(() => {
    routeState.path = '/aicc-console'
    routerPush.mockClear()
    agentsState.data.value = [
      makeAgent(),
      makeAgent({
        id: 'agent-support',
        app_id: 'app-2',
        name: '售后支持',
        status: 'paused',
        retention_days: 90,
        public_token: undefined,
      }),
    ]
    agentsState.isLoading.value = false
    agentsState.error.value = null
  })

  // 覆盖最终工作台结构：顶部只做智能体选择，左侧菜单负责模块切换。
  it('renders the module menu in the left rail and pushes all console routes', async () => {
    const wrapper = mountWorkspace()

    expect(wrapper.find('[data-test="workspace-module-menu"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="workspace-agent-bar"]').exists()).toBe(true)
    expect(navItems(wrapper).map(item => item.text())).toEqual(['接待台', '会话', '线索', '知识库', '分析', '设置'])
    expect(navItems(wrapper).map(item => item.attributes('href'))).toEqual([
      '/aicc-console',
      '/aicc-console/sessions',
      '/aicc-console/leads',
      '/aicc-console/knowledge',
      '/aicc-console/analytics',
      '/aicc-console/settings',
    ])
    expect(wrapper.find('[data-test="router-view"]').exists()).toBe(true)

    for (const item of navItems(wrapper)) {
      await item.trigger('click')
    }

    expect(routerPush.mock.calls.map(call => call[0])).toEqual([
      '/aicc-console',
      '/aicc-console/sessions',
      '/aicc-console/leads',
      '/aicc-console/knowledge',
      '/aicc-console/analytics',
      '/aicc-console/settings',
    ])
  })

  // 覆盖当前智能体上下文条：默认选中首个智能体，并展示名称、运行状态、公开入口和保留天数。
  it('shows the selected agent context summary from the agent list', () => {
    const wrapper = mountWorkspace()

    expect(wrapper.text()).toContain('当前智能体')
    expect(wrapper.text()).toContain('售前接待')
    expect(wrapper.text()).toContain('接待中')
    expect(wrapper.text()).toContain('已生成')
    expect(wrapper.text()).toContain('180 天')
    expect(wrapper.find('[data-test="context-selected-id"]').text()).toBe('agent-sales')
    expect(wrapper.find('[data-test="context-id-readonly"]').text()).toBe('true')
  })

  // 覆盖上下文写入口：子页面只能通过 selectAgent/startCreateAgent 修改工作区内部选中值。
  it('provides read-only selected agent context with controlled selection actions', async () => {
    const wrapper = mountWorkspace()

    await wrapper.find('[data-test="select-support"]').trigger('click')
    await nextTick()

    expect(wrapper.find('[data-test="context-selected-id"]').text()).toBe('agent-support')
    expect(wrapper.find('[data-test="context-selected-agent"]').text()).toBe('售后支持')
    expect(wrapper.text()).toContain('售后支持')
    expect(wrapper.text()).toContain('已暂停')
    expect(wrapper.text()).toContain('保存后生成')
    expect(wrapper.text()).toContain('90 天')

    await wrapper.find('[data-test="start-create-agent"]').trigger('click')
    await nextTick()

    expect(wrapper.find('[data-test="context-selected-id"]').text()).toBe('none')
    expect(wrapper.text()).toContain('未选择智能体')
    expect(wrapper.text()).toContain('新建智能体')
  })

  // 覆盖无智能体空态：列表为空时仍挂载子路由，并给出创建智能体入口。
  it('shows an empty agent context when no agents exist', () => {
    agentsState.data.value = []

    const wrapper = mountWorkspace()

    expect(wrapper.text()).toContain('未选择智能体')
    expect(wrapper.text()).toContain('新建智能体')
    expect(wrapper.find('[data-test="context-selected-id"]').text()).toBe('none')
    expect(wrapper.find('[data-test="router-view"]').exists()).toBe(true)
  })

  // 覆盖智能体加载与失败状态：上下文条必须复用 i18n 文案提示当前数据状态。
  it('shows agent loading and load failure states in the context strip', () => {
    agentsState.data.value = []
    agentsState.isLoading.value = true
    const loadingWrapper = mountWorkspace()

    expect(loadingWrapper.text()).toContain('正在加载智能体')

    agentsState.isLoading.value = false
    agentsState.error.value = new Error('network failed')
    const errorWrapper = mountWorkspace()

    expect(errorWrapper.text()).toContain('智能体加载失败')
  })
})
