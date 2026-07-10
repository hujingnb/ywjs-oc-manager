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
    return () => h('label', [h('span', String(props.label ?? '')), slots.default?.()])
  },
})

const InputStub = defineComponent({
  props: ['value', 'placeholder'],
  emits: ['update:value'],
  setup(props, { emit }) {
    return () => h('input', {
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

function mountManager(context: AICCConsoleContext) {
  i18n.global.locale.value = 'zh'
  return mount(AICCManagerPage, {
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
    expect(wrapper.text()).toContain('售前接待')
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
