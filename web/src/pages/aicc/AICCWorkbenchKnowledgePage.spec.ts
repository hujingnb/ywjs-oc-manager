import { mount } from '@vue/test-utils'
import { computed, defineComponent, h, nextTick, ref, type Ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { AppDTO } from '@/api/hooks/useApps'
import { i18n } from '@/i18n'
import type { AICCAgent } from '@/domain/aicc'
import { AICCConsoleContextKey, type AICCConsoleContext } from './aiccConsoleContext'
import AICCWorkbenchKnowledgePage from './AICCWorkbenchKnowledgePage.vue'

const routerPush = vi.hoisted(() => vi.fn())
const routerReplace = vi.hoisted(() => vi.fn())
const useAppQuery = vi.hoisted(() => vi.fn())

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: routerPush, replace: routerReplace }),
}))

vi.mock('@/api/hooks/useApps', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/hooks/useApps')>()

  return {
    ...actual,
    useAppQuery,
  }
})

vi.mock('@/pages/apps/AppKnowledgeTab.vue', () => ({
  default: defineComponent({
    props: {
      appId: {
        type: String,
        required: true,
      },
    },
    setup(props) {
      return () => h('section', { 'data-test': 'embedded-knowledge' }, [
        h('span', { 'data-test': 'embedded-app-id' }, props.appId),
      ])
    },
  }),
}))

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

function makeApp(overrides: Partial<AppDTO> = {}): AppDTO {
  return {
    id: 'app-1',
    org_id: 'org-1',
    owner_user_id: 'user-1',
    name: '售前接待隐藏实例',
    status: 'running',
    api_key_status: 'bound',
    knowledge_quota_bytes: 1024 * 1024 * 1024,
    ...overrides,
  }
}

function makeContext(options: {
  selectedAgent?: AICCAgent
  agentsLoading?: boolean
  agentsError?: Error | null
} = {}): AICCConsoleContext {
  const selectedAgent = ref<AICCAgent | undefined>(options.selectedAgent)
  const agents = computed(() => selectedAgent.value ? [selectedAgent.value] : [])

  return {
    agents,
    selectedOrgId: computed(() => selectedAgent.value?.org_id),
    isPlatformAdmin: computed(() => false),
    selectedAgentId: computed(() => selectedAgent.value?.id),
    selectedAgent: computed(() => selectedAgent.value),
    agentsLoading: computed(() => options.agentsLoading ?? false),
    agentsError: computed(() => options.agentsError ?? null),
    selectAgent: vi.fn(),
    startCreateAgent: vi.fn(),
  }
}

function mountKnowledgePage(context: AICCConsoleContext = makeContext({ selectedAgent: makeAgent() })) {
  i18n.global.locale.value = 'zh'

  return mount(AICCWorkbenchKnowledgePage, {
    global: {
      plugins: [i18n],
      provide: {
        [AICCConsoleContextKey as symbol]: context,
      },
    },
  })
}

describe('AICCWorkbenchKnowledgePage', () => {
  beforeEach(() => {
    routerPush.mockClear()
    routerReplace.mockClear()
    useAppQuery.mockReset()
    useAppQuery.mockReturnValue({
      data: ref<AppDTO | null>(makeApp()),
      isLoading: ref(false),
      error: ref(null),
    })
  })

  // 覆盖 AICC 工作台知识库：必须停留在当前子页面，不能跳回主站实例详情页。
  it('embeds the selected agent knowledge base without navigating away from the console', () => {
    const wrapper = mountKnowledgePage()

    expect(wrapper.find('[data-test="embedded-knowledge"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="embedded-app-id"]').text()).toBe('app-1')
    expect(routerPush).not.toHaveBeenCalled()
    expect(routerReplace).not.toHaveBeenCalled()
  })

  // 覆盖隐藏实例查询参数：知识库页使用当前智能体绑定的 app_id 拉取实例上下文。
  it('loads the hidden app context for the selected agent', () => {
    mountKnowledgePage()

    const appIdRef = useAppQuery.mock.calls[0][0] as Ref<string | undefined>

    expect(appIdRef.value).toBe('app-1')
  })

  // 覆盖无智能体边界：未选择智能体时显示工作台内空态，不触发路由跳转。
  it('shows an in-place empty state when no agent is selected', async () => {
    const wrapper = mountKnowledgePage(makeContext())

    await nextTick()

    expect(wrapper.text()).toContain('请先选择智能体')
    expect(wrapper.find('[data-test="embedded-knowledge"]').exists()).toBe(false)
    expect(routerPush).not.toHaveBeenCalled()
    expect(routerReplace).not.toHaveBeenCalled()
  })
})
