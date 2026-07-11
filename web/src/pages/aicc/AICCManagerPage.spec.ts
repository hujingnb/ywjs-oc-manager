import { mount } from '@vue/test-utils'
import { computed, defineComponent, h, ref, type Component, type ComputedRef } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import type { AICCAgent } from '@/domain/aicc'
import { AICCConsoleContextKey, type AICCConsoleContext } from './aiccConsoleContext'
import AICCManagerPage from './AICCManagerPage.vue'

const mutationState = vi.hoisted(() => {
  const { ref } = require('vue') as typeof import('vue')
  return {
    pending: ref(false),
    isPending: ref(false),
    mutateAsync: vi.fn(),
  }
})

const queryState = vi.hoisted(() => {
  const { ref } = require('vue') as typeof import('vue')
  return {
    settings: { data: ref(undefined), isFetching: ref(false) },
    leadFields: { data: ref([]), isFetching: ref(false) },
    knowledge: { data: ref(undefined), isFetching: ref(false) },
    knowledgeOptions: {
      data: ref({ industry_knowledge_bases: [], app_documents: [] }),
      isFetching: ref(false),
    },
  }
})

vi.mock('qrcode', () => ({
  default: {
    toDataURL: vi.fn().mockResolvedValue('data:image/png;base64,test'),
  },
}))

vi.mock('@/api/hooks/useAICC', () => ({
  useAICCSettingsQuery: () => queryState.settings,
  useAICCLeadFieldsQuery: () => queryState.leadFields,
  useAICCKnowledgeQuery: () => queryState.knowledge,
  useAICCKnowledgeOptionsQuery: () => queryState.knowledgeOptions,
  useCreateAICCAgent: () => mutationState,
  useUpdateAICCAgent: () => mutationState,
  useUpdateAICCSettings: () => mutationState,
  useReplaceAICCLeadFields: () => mutationState,
  useReplaceAICCKnowledge: () => mutationState,
  useSetAICCAgentStatus: () => mutationState,
  useDeleteAICCAgent: () => mutationState,
}))

// makeAgent：构造 AICC 管理页需要的最小智能体对象，保持 snake_case 字段与 API 契约一致。
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
    widget_token: 'widget-token',
    ...overrides,
  }
}

const ButtonStub = defineComponent({
  props: ['type', 'disabled', 'loading'],
  emits: ['click'],
  setup(props, { slots, emit }) {
    return () => h('button', {
      disabled: Boolean(props.disabled),
      onClick: () => emit('click'),
    }, [slots.icon?.(), slots.default?.()])
  },
})

const TagStub = defineComponent({
  setup(_, { slots }) {
    return () => h('span', slots.default?.())
  },
})

const AlertStub = defineComponent({
  setup(_, { slots }) {
    return () => h('div', slots.default?.())
  },
})

const SpaceStub = defineComponent({
  setup(_, { slots }) {
    return () => h('div', slots.default?.())
  },
})

const FormStub = defineComponent({
  setup(_, { slots }) {
    return () => h('form', slots.default?.())
  },
})

const FormItemStub = defineComponent({
  props: ['label'],
  setup(props, { slots }) {
    return () => h('label', [slots.label?.() ?? h('span', String(props.label ?? '')), slots.default?.()])
  },
})

const InputStub = defineComponent({
  props: ['value', 'placeholder', 'inputProps'],
  emits: ['update:value'],
  setup(props, { emit }) {
    return () => h('input', {
      ...(props.inputProps as Record<string, unknown> | undefined),
      value: props.value ?? '',
      placeholder: props.placeholder as string,
      onInput: (event: Event) => emit('update:value', (event.target as HTMLInputElement).value),
    })
  },
})

const InputNumberStub = InputStub
const SelectStub = InputStub
const CheckboxStub = defineComponent({
  props: ['checked'],
  emits: ['update:checked'],
  setup(props, { slots, emit }) {
    return () => h('label', [
      h('input', {
        type: 'checkbox',
        checked: Boolean(props.checked),
        onChange: (event: Event) => emit('update:checked', (event.target as HTMLInputElement).checked),
      }),
      slots.default?.(),
    ])
  },
})
const SwitchStub = CheckboxStub
const TooltipStub = defineComponent({
  setup(_, { slots }) {
    return () => h('span', { 'data-test': 'tooltip' }, [slots.trigger?.(), slots.default?.()])
  },
})

function mountManager(context: AICCConsoleContext, props: { initialSection?: 'reception' | 'settings' } = {}) {
  i18n.global.locale.value = 'zh'
  return mount(AICCManagerPage, {
    props,
    global: {
      plugins: [i18n],
      provide: {
        [AICCConsoleContextKey as symbol]: context,
      },
      stubs: {
        ConfirmActionModal: true,
        AICCAnalyticsPage: true,
        AICCLeadsPage: true,
        AICCSessionsPage: true,
        NAlert: AlertStub,
        NButton: ButtonStub,
        NCheckbox: CheckboxStub,
        NForm: FormStub,
        NFormItem: FormItemStub,
        NInput: InputStub,
        NInputNumber: InputNumberStub,
        NSelect: SelectStub,
        NSpace: SpaceStub,
        NTag: TagStub,
        NSwitch: SwitchStub,
        NTooltip: TooltipStub,
        'n-alert': AlertStub,
        'n-button': ButtonStub,
        'n-checkbox': CheckboxStub,
        'n-form': FormStub,
        'n-form-item': FormItemStub,
        'n-input': InputStub,
        'n-input-number': InputNumberStub,
        'n-select': SelectStub,
        'n-space': SpaceStub,
        'n-tag': TagStub,
        'n-switch': SwitchStub,
        'n-tooltip': TooltipStub,
      } satisfies Record<string, Component | boolean>,
    },
  })
}

function makeConsoleContext() {
  const agents = ref<AICCAgent[]>([
    makeAgent(),
    makeAgent({ id: 'agent-support', app_id: 'app-2', name: '售后支持', status: 'paused' }),
  ])
  const selectedAgentIdState = ref<string | undefined>('agent-sales')
  const selectedAgent = computed(() => agents.value.find(agent => agent.id === selectedAgentIdState.value))
  const context: AICCConsoleContext = {
    agents: computed(() => agents.value),
    selectedOrgId: computed(() => 'org-1'),
    isPlatformAdmin: computed(() => false),
    selectedAgentId: computed(() => selectedAgentIdState.value) as ComputedRef<string | undefined>,
    selectedAgent,
    agentsLoading: computed(() => false),
    agentsError: computed(() => null),
    selectAgent: (agentId?: string) => {
      selectedAgentIdState.value = agentId
    },
    startCreateAgent: () => {
      selectedAgentIdState.value = undefined
    },
  }

  return { context, selectedAgentIdState }
}

describe('AICCManagerPage', () => {
  beforeEach(() => {
    mutationState.pending.value = false
    mutationState.isPending.value = false
    mutationState.mutateAsync.mockReset()
    queryState.settings.data.value = undefined
    queryState.leadFields.data.value = []
    queryState.knowledge.data.value = undefined
    queryState.knowledgeOptions.data.value = { industry_knowledge_bases: [], app_documents: [] }
  })

  // 覆盖最终布局：智能体选择已经上移到工作台顶部，接待台内容区不能再重复展示智能体列表。
  it('uses the top selected agent without rendering a content-area agent list', () => {
    const { context } = makeConsoleContext()
    const wrapper = mountManager(context)

    expect(wrapper.find('.agent-rail').exists()).toBe(false)
    expect(wrapper.findAll('button').filter(button => button.text() === '新建智能体')).toHaveLength(0)
    expect(wrapper.text()).toContain('售前接待')
  })

  // 覆盖左侧菜单语义：接待台只展示投放和运行概览，不再混入设置表单。
  it('renders reception as a delivery overview instead of the settings form', async () => {
    const { context } = makeConsoleContext()
    const wrapper = mountManager(context, { initialSection: 'reception' })
    const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)

    expect(wrapper.text()).toContain('公开链接')
    expect(wrapper.text()).toContain('嵌入占位')
    expect(wrapper.text()).toContain('预览挂件效果')
    expect(wrapper.text()).not.toContain('智能体名称')
    expect(wrapper.text()).not.toContain('单会话消息上限')
    expect(wrapper.text()).not.toContain('访客留资')

    await wrapper.findAll('button').find(button => button.text().includes('预览挂件效果'))?.trigger('click')
    expect(openSpy).toHaveBeenCalledWith('/aicc-widget-preview/widget-token', '_blank', 'noopener,noreferrer')
    openSpy.mockRestore()
  })

  // 覆盖左侧菜单语义：设置页承载规则配置，不再重复展示接待台投放概览。
  it('renders settings as the dedicated configuration page', () => {
    const { context } = makeConsoleContext()
    const wrapper = mountManager(context, { initialSection: 'settings' })

    expect(wrapper.text()).toContain('智能体名称')
    expect(wrapper.text()).toContain('单会话消息上限')
    expect(wrapper.text()).toContain('知识库范围')
    expect(wrapper.text()).toContain('当前客服的知识库')
    expect(wrapper.text()).not.toContain('专属文档')
    expect(wrapper.text()).toContain('访客留资')
    expect(wrapper.text()).not.toContain('嵌入占位')
  })

  // 覆盖设置页路由体验：进入独立设置页时不应再触发旧版锚点滚动，避免页面跳动。
  it('does not scroll when opening the dedicated settings route', async () => {
    const scrollIntoView = vi.fn()
    const previousScrollIntoView = Element.prototype.scrollIntoView
    Element.prototype.scrollIntoView = scrollIntoView
    try {
      const { context } = makeConsoleContext()

      mountManager(context, { initialSection: 'settings' })
      await Promise.resolve()

      expect(scrollIntoView).not.toHaveBeenCalled()
    } finally {
      Element.prototype.scrollIntoView = previousScrollIntoView
    }
  })

  // 覆盖设置页字段说明：每个主要配置字段后都应有帮助入口，并展示业务解释文案。
  it('shows help tooltips for settings form fields', () => {
    const { context } = makeConsoleContext()
    const wrapper = mountManager(context, { initialSection: 'settings' })

    const helpTriggers = wrapper.findAll('[data-test="field-help"]')

    expect(helpTriggers).toHaveLength(15)
    expect(helpTriggers.every(trigger => trigger.text() === '?')).toBe(true)
    expect(helpTriggers.some(trigger =>
      trigger.attributes('aria-label') === '开启后，系统会根据敏感词命中、异常频率等规则标记高风险访客，并阻止其继续发送消息。',
    )).toBe(true)
    expect(helpTriggers.some(trigger =>
      trigger.attributes('aria-label') === '当前客服回答访客问题时会结合这里维护的资料，适合放产品说明、服务话术和常见问题。',
    )).toBe(true)
  })

  // 覆盖浏览器表单识别：设置页用户可输入控件必须带 id 或 name，避免 Chrome DevTools 可访问性提示。
  it('adds id or name to settings form controls', () => {
    const { context } = makeConsoleContext()
    const wrapper = mountManager(context, { initialSection: 'settings' })

    const fields = wrapper.findAll('input:not([type="checkbox"]), textarea')
    const missingIdentity = fields.filter(field => !field.attributes('id') && !field.attributes('name'))

    expect(missingIdentity).toHaveLength(0)
  })

  // 覆盖顶部新建智能体联动：顶部选择区清空智能体后，内容区表单必须进入新建态且不残留旧名称。
  it('clears the editor form when the top-level agent selection enters create mode', async () => {
    const { context } = makeConsoleContext()
    const wrapper = mountManager(context)

    expect(wrapper.text()).toContain('售前接待')

    context.startCreateAgent()
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('未命名接待员')
    expect(wrapper.text()).not.toContain('售前接待')
  })
})
