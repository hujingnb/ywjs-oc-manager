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
    organizationConfig: {
      data: ref<{
        org_id: string
        enabled: boolean
        model: string
        revision: number
        industry_knowledge_bases: Array<{ id: string; name: string }>
      }>({ org_id: 'org-1', enabled: true, model: 'gpt-aicc', revision: 1, industry_knowledge_bases: [] }),
      isFetching: ref(false),
      error: ref<Error | null>(null),
      lastOrgIdRef: ref<{ value?: string } | undefined>(undefined),
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
  useAICCOrganizationConfigQuery: (orgId?: { value?: string }) => {
    queryState.organizationConfig.lastOrgIdRef.value = orgId
    return queryState.organizationConfig
  },
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
    industry_knowledge_base_ids: [],
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
  setup(_, { attrs, slots }) {
    return () => h('form', attrs, slots.default?.())
  },
})

const FormItemStub = defineComponent({
  props: ['label'],
  setup(props, { slots }) {
    return () => h('label', [slots.label?.() ?? h('span', String(props.label ?? '')), slots.default?.()])
  },
})

const InputStub = defineComponent({
  props: ['value', 'placeholder', 'inputProps', 'type', 'maxlength'],
  emits: ['update:value'],
  setup(props, { emit }) {
    return () => h(props.type === 'textarea' ? 'textarea' : 'input', {
      ...(props.inputProps as Record<string, unknown> | undefined),
      value: props.value ?? '',
      placeholder: props.placeholder as string,
      maxlength: props.maxlength as number | undefined,
      onInput: (event: Event) => emit('update:value', (event.target as HTMLInputElement).value),
    })
  },
})

const InputNumberStub = InputStub
const SelectStub = defineComponent({
  props: ['value', 'options', 'inputProps', 'multiple', 'disabled', 'loading'],
  emits: ['update:value'],
  setup(props, { attrs, emit }) {
    return () => h('select', {
      ...attrs,
      ...(props.inputProps as Record<string, unknown> | undefined),
      multiple: Boolean(props.multiple),
      disabled: Boolean(props.disabled),
      value: props.value,
      onChange: (event: Event) => {
        const select = event.target as HTMLSelectElement
        emit('update:value', props.multiple
          ? Array.from(select.selectedOptions).map(option => option.value)
          : select.value)
      },
    }, (props.options as Array<{ label: string; value: string; disabled?: boolean }> | undefined)?.map(option => h('option', {
      value: option.value,
      selected: Array.isArray(props.value) ? props.value.includes(option.value) : props.value === option.value,
      disabled: option.disabled,
    }, option.label)))
  },
})
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

// makeConsoleContext：按角色与企业上下文构造工作台注入对象，覆盖平台管理员代管企业客服的权限边界。
function makeConsoleContext(options: { platformAdmin?: boolean; selectedOrgId?: string } = {}) {
  const agents = ref<AICCAgent[]>([
    makeAgent(),
    makeAgent({ id: 'agent-support', app_id: 'app-2', name: '售后支持', status: 'paused' }),
  ])
  const selectedAgentIdState = ref<string | undefined>('agent-sales')
  const selectedAgent = computed(() => agents.value.find(agent => agent.id === selectedAgentIdState.value))
  const context: AICCConsoleContext = {
    agents: computed(() => agents.value),
    selectedOrgId: computed(() => options.selectedOrgId ?? 'org-1'),
    isPlatformAdmin: computed(() => options.platformAdmin ?? false),
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

  return { context, agentsState: agents, selectedAgentIdState }
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
    queryState.organizationConfig.data.value = {
      org_id: 'org-1', enabled: true, model: 'gpt-aicc', revision: 1, industry_knowledge_bases: [],
    }
    queryState.organizationConfig.isFetching.value = false
    queryState.organizationConfig.error.value = null
    queryState.organizationConfig.lastOrgIdRef.value = undefined
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

  // 覆盖运行时初始化失败：接待台必须展示后端提供的安全错误摘要，便于企业管理员定位异常。
  it('shows the runtime error detail when the selected agent is abnormal', () => {
    const { context } = makeConsoleContext()
    const agents = context.agents.value
    agents[0] = makeAgent({
      runtime_status: 'error',
      runtime_message: '创建 AICC Deployment 失败：集群拒绝该资源版本',
    })
    const wrapper = mountManager(context, { initialSection: 'reception' })

    expect(wrapper.text()).toContain('运行异常详情')
    expect(wrapper.text()).toContain('创建 AICC Deployment 失败：集群拒绝该资源版本')
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

    expect(helpTriggers).toHaveLength(17)
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

  // 覆盖平台管理员代管企业：从企业列表携带 org_id 进入时，设置页应允许保存该企业的客服配置。
  it('allows a platform admin with a selected organization to save agent configuration', () => {
    const { context } = makeConsoleContext({ platformAdmin: true, selectedOrgId: 'org-1' })
    const wrapper = mountManager(context, { initialSection: 'settings' })
    const saveButton = wrapper.findAll('button').find(button => button.text().includes('保存配置'))

    expect(saveButton).toBeDefined()
    expect(saveButton?.attributes('disabled')).toBeUndefined()
  })

  // 场景：新建客服只能查看企业已配置模型，并从企业授权范围选择行业知识库，不能编辑模型或助手版本路由。
  it('shows the organization model as read-only and industry knowledge candidates in the agent form', () => {
    queryState.organizationConfig.data.value = {
      org_id: 'org-1',
      enabled: true,
      model: 'gpt-aicc',
      revision: 1,
      industry_knowledge_bases: [{ id: 'industry-retail', name: '零售知识库' }],
    }
    const { context } = makeConsoleContext()
    context.startCreateAgent()
    const wrapper = mountManager(context, { initialSection: 'settings' })

    expect(wrapper.text()).toContain('企业客服模型')
    expect(wrapper.text()).toContain('gpt-aicc')
    expect(wrapper.find('#aicc-industry-knowledge').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('助手版本')
    expect(wrapper.text()).not.toContain('路由')
    expect(wrapper.findAll('select').filter(select => select.attributes('id')?.includes('model'))).toHaveLength(0)
  })

  // 覆盖独立配置查询：企业管理员必须向 AICC 配置 hook 传入自身 org_id，避免 query key 和模型候选丢失企业隔离。
  it('queries the organization AICC config with the org-admin organization id', () => {
    const { context } = makeConsoleContext({ selectedOrgId: 'org-7' })
    mountManager(context, { initialSection: 'settings' })

    expect(queryState.organizationConfig.lastOrgIdRef.value?.value).toBe('org-7')
  })

  // 覆盖配置加载保护：候选行业库尚未就绪时，不能保存表单把既有行业授权误写为空数组。
  it('disables industry selection and primary save while the organization config is loading', () => {
    queryState.organizationConfig.isFetching.value = true
    const { context } = makeConsoleContext()
    const wrapper = mountManager(context, { initialSection: 'settings' })

    expect(wrapper.find('#aicc-industry-knowledge').attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('button').find(button => button.text().includes('保存配置'))?.attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('正在加载企业客服配置')
  })

  // 覆盖配置失败保护：读取企业授权失败时，页面必须阻止保存并展示可操作的失败提示。
  it('disables primary save and shows an error when the organization config cannot load', () => {
    queryState.organizationConfig.error.value = new Error('network failed')
    const { context } = makeConsoleContext()
    const wrapper = mountManager(context, { initialSection: 'settings' })

    expect(wrapper.findAll('button').find(button => button.text().includes('保存配置'))?.attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('企业客服配置加载失败')
  })

  // 覆盖已撤销授权的回显：历史行业库 ID 不在候选列表时仍须有警告标签，避免管理员无法识别并移除。
  it('keeps revoked industry knowledge ids visible as disabled warning options', async () => {
    queryState.organizationConfig.data.value = {
      org_id: 'org-1', enabled: true, model: 'gpt-aicc', revision: 1,
      industry_knowledge_bases: [{ id: 'industry-current', name: '当前授权库' }],
    }
    const { context, agentsState } = makeConsoleContext()
    agentsState.value = [makeAgent({ industry_knowledge_base_ids: ['industry-revoked'] })]
    const wrapper = mountManager(context, { initialSection: 'settings' })

    const options = (wrapper.vm as unknown as { industryKnowledgeOptions: Array<{ value: string; label: string; disabled?: boolean }> }).industryKnowledgeOptions
    expect(options).toContainEqual({ value: 'industry-revoked', label: '已撤销授权（industry-revoked）', disabled: true })
  })

  // 场景：保存新建客服时，人设和行业库选择必须进入创建载荷，避免仅在界面暂存。
  it('sends persona and industry knowledge ids when creating an agent', async () => {
    mutationState.mutateAsync.mockResolvedValue(makeAgent({ persona: '已保存人设', industry_knowledge_base_ids: ['industry-retail'] }))
    queryState.organizationConfig.data.value = {
      org_id: 'org-1',
      enabled: true,
      model: 'gpt-aicc',
      revision: 1,
      industry_knowledge_bases: [{ id: 'industry-retail', name: '零售知识库' }],
    }
    const { context } = makeConsoleContext()
    context.startCreateAgent()
    const wrapper = mountManager(context, { initialSection: 'settings' })

    await wrapper.find('#aicc-agent-name').setValue('零售顾问')
    await wrapper.find('#aicc-persona').setValue('专业售前顾问')
    ;(wrapper.vm as unknown as { form: { industry_knowledge_base_ids: string[] } }).form.industry_knowledge_base_ids = ['industry-retail']
    await wrapper.vm.$nextTick()
    await wrapper.find('form').trigger('submit')
    await Promise.resolve()
    await wrapper.vm.$nextTick()

    expect(mutationState.mutateAsync).toHaveBeenCalledWith(expect.objectContaining({
      name: '零售顾问',
      persona: '专业售前顾问',
      industry_knowledge_base_ids: ['industry-retail'],
    }))
    expect((wrapper.find('#aicc-persona').element as HTMLTextAreaElement).value).toBe('已保存人设')
  })

  // 场景：切换编辑对象时必须从各自 agent 响应回填人设和行业库，不能残留上一个客服的表单值。
  it('restores each selected agent persona and industry knowledge ids', async () => {
    queryState.organizationConfig.data.value = {
      org_id: 'org-1',
      enabled: true,
      model: 'gpt-aicc',
      revision: 1,
      industry_knowledge_bases: [
        { id: 'industry-sales', name: '销售知识库' },
        { id: 'industry-support', name: '售后知识库' },
      ],
    }
    const { context, selectedAgentIdState } = makeConsoleContext()
    const agents = context.agents.value
    agents[0] = makeAgent({ persona: '售前人设', industry_knowledge_base_ids: ['industry-sales'] })
    agents[1] = makeAgent({ id: 'agent-support', persona: '售后人设', industry_knowledge_base_ids: ['industry-support'] })
    const wrapper = mountManager(context, { initialSection: 'settings' })

    expect((wrapper.find('#aicc-persona').element as HTMLTextAreaElement).value).toBe('售前人设')
    expect((wrapper.vm as unknown as { form: { industry_knowledge_base_ids: string[] } }).form.industry_knowledge_base_ids).toEqual(['industry-sales'])

    selectedAgentIdState.value = 'agent-support'
    await wrapper.vm.$nextTick()

    expect((wrapper.find('#aicc-persona').element as HTMLTextAreaElement).value).toBe('售后人设')
    expect((wrapper.vm as unknown as { form: { industry_knowledge_base_ids: string[] } }).form.industry_knowledge_base_ids).toEqual(['industry-support'])
  })

  // 覆盖主表单与知识面板联动：刚保存的新行业范围必须成为知识保存载荷，不能回写旧 agent 快照。
  it('uses the latest saved industry knowledge ids when saving knowledge settings', async () => {
    mutationState.mutateAsync
      .mockResolvedValueOnce(makeAgent({ industry_knowledge_base_ids: ['industry-new'] }))
      .mockResolvedValueOnce(undefined)
    const { context } = makeConsoleContext()
    const wrapper = mountManager(context, { initialSection: 'settings' })

    ;(wrapper.vm as unknown as { form: { industry_knowledge_base_ids: string[] } }).form.industry_knowledge_base_ids = ['industry-new']
    await wrapper.find('form').trigger('submit')
    await Promise.resolve()
    await wrapper.vm.$nextTick()
    await wrapper.findAll('button').find(button => button.text().includes('保存知识范围'))!.trigger('click')
    await Promise.resolve()

    expect(mutationState.mutateAsync).toHaveBeenLastCalledWith({
      agentId: 'agent-sales',
      payload: {
        use_org_knowledge: true,
        industry_knowledge_base_ids: ['industry-new'],
      },
    })
  })
})
