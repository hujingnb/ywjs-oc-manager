import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'

import TicketDetailPage from './TicketDetailPage.vue'

const detailState = {
  data: ref<Record<string, unknown> | null>(null),
  isLoading: ref(false),
  error: ref<Error | null>(null),
  refetch: vi.fn(),
}
const orgsState = {
  data: ref<Record<string, unknown>[]>([{ id: 'org-1', name: '甲公司' }]),
}
const authState = {
  user: { id: 'admin-1', role: 'platform_admin' } as { id: string; role: string },
}
const router = { push: vi.fn(), back: vi.fn() }

const mocks = vi.hoisted(() => ({
  start: vi.fn(),
  reopen: vi.fn(),
  quote: vi.fn(),
  reject: vi.fn(),
  updateTargets: vi.fn(),
  success: vi.fn(),
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: 't-1' } }),
  useRouter: () => router,
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationsQuery: () => orgsState,
}))

vi.mock('@/api/hooks/useSkillTickets', () => ({
  useSkillTicketDetailQuery: () => detailState,
  useStartTicket: () => ({ mutateAsync: mocks.start, isPending: ref(false) }),
  useReopenTicket: () => ({ mutateAsync: mocks.reopen, isPending: ref(false) }),
  useSetSkillTicketQuote: () => ({ mutateAsync: mocks.quote, isPending: ref(false) }),
  useRejectSkillTicket: () => ({ mutateAsync: mocks.reject, isPending: ref(false) }),
  useUpdateTicketTargets: () => ({ mutateAsync: mocks.updateTargets, isPending: ref(false) }),
}))

vi.mock('naive-ui', () => ({
  useMessage: () => ({ success: mocks.success }),
  NButton: { template: '<button v-bind="$attrs"><slot /></button>' },
  NInput: {
    props: ['value'],
    emits: ['update:value'],
    template: '<textarea :value="value" @input="$emit(\'update:value\', $event.target.value)" />',
  },
  NInputNumber: {
    props: ['value'],
    emits: ['update:value'],
    template: '<input class="quote-input" :value="value" @input="$emit(\'update:value\', Number($event.target.value))" />',
  },
  NModal: { props: ['show'], template: '<div v-if="show" class="n-modal"><slot /><slot name="footer" /></div>' },
}))

vi.mock('@/components/ticket/TicketStatusStepper.vue', () => ({
  default: { props: ['status'], template: '<div class="stepper">{{ status }}</div>' },
}))
vi.mock('@/components/ticket/TicketConversation.vue', () => ({
  default: { template: '<div class="conversation" />' },
}))
vi.mock('@/components/ticket/DeliverCustomSkillModal.vue', () => ({
  default: { template: '<div class="deliver-modal" />' },
}))
vi.mock('@/components/ticket/TicketTargetsEditor.vue', () => ({
  default: { template: '<div class="targets-editor" />' },
}))

function mountPage() {
  return mount(TicketDetailPage)
}

describe('TicketDetailPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    detailState.isLoading.value = false
    detailState.error.value = null
    detailState.refetch.mockResolvedValue(undefined)
    authState.user = { id: 'admin-1', role: 'platform_admin' }
  })

  // 平台管理员按状态看到动作按钮:pending 开始制作,delivered 编辑可见范围,rejected 重新受理。
  it('renders admin actions per status', async () => {
    detailState.data.value = { id: 't-1', title: '需求', status: 'pending', messages: [] }
    let wrapper = mountPage()
    expect(wrapper.text()).toContain('开始制作')
    await wrapper.findAll('button').find((button) => button.text() === '开始制作')!.trigger('click')
    expect(mocks.start).toHaveBeenCalledWith({ id: 't-1' })

    detailState.data.value = { id: 't-1', title: '需求', status: 'delivered', messages: [] }
    wrapper = mountPage()
    expect(wrapper.text()).toContain('迭代交付')
    expect(wrapper.text()).toContain('编辑可见范围')

    detailState.data.value = { id: 't-1', title: '需求', status: 'rejected', messages: [] }
    wrapper = mountPage()
    expect(wrapper.text()).toContain('重新受理')
  })

  // 平台管理员查看工单详情时展示提交者和所属企业，便于识别需求来源。
  it('renders requester and organization for platform admin', () => {
    detailState.data.value = {
      id: 't-1',
      title: '需求',
      status: 'pending',
      requester_name: '张三',
      requester_role: 'org_member',
      org_name: '甲公司',
      messages: [],
    }
    const wrapper = mountPage()
    expect(wrapper.text()).toContain('提交信息')
    expect(wrapper.text()).toContain('提交者')
    expect(wrapper.text()).toContain('张三')
    expect(wrapper.text()).toContain('成员')
    expect(wrapper.text()).toContain('所属企业')
    expect(wrapper.text()).toContain('甲公司')
  })

  // 已交付详情页展示可见范围时，org_admins 应明确标注为企业管理员，避免误解为平台管理员。
  it('renders org admins audience as enterprise admins', () => {
    detailState.data.value = {
      id: 't-1',
      title: '需求',
      status: 'delivered',
      org_id: 'org-1',
      targets: [{ org_id: 'org-1', audience: 'org_admins' }],
      messages: [],
    }
    const wrapper = mountPage()
    expect(wrapper.text()).toContain('甲公司 · 仅企业管理员')
    expect(wrapper.text()).not.toContain('甲公司 · 仅管理员')
  })

  // 需求描述统一进入对话消息流后,详情页不再渲染独立“需求”区块。
  it('does not render standalone requirement description section', () => {
    detailState.data.value = {
      id: 't-1',
      title: '需求',
      status: 'pending',
      description: '旧字段描述',
      messages: [],
    }
    const wrapper = mountPage()
    expect(wrapper.text()).not.toContain('需求旧字段描述')
    expect(wrapper.text()).not.toContain('暂无描述')
  })

  // 需求方详情页不展示来源信息，避免对本人重复展示冗余字段。
  it('hides requester and organization for requester', () => {
    authState.user = { id: 'u-1', role: 'org_member' }
    detailState.data.value = {
      id: 't-1',
      title: '需求',
      status: 'pending',
      requester_name: '张三',
      requester_role: 'org_member',
      org_name: '甲公司',
      messages: [],
    }
    const wrapper = mountPage()
    expect(wrapper.text()).not.toContain('提交信息')
    expect(wrapper.text()).not.toContain('所属企业')
  })

  // 需求方只读,已交付时显示去安装且不显示管理员动作。
  it('renders requester read-only with go-install on delivered', async () => {
    authState.user = { id: 'u-1', role: 'org_member' }
    detailState.data.value = { id: 't-1', title: '需求', status: 'delivered', messages: [] }
    const wrapper = mountPage()
    expect(wrapper.text()).not.toContain('编辑可见范围')
    const install = wrapper.findAll('button').find((button) => button.text() === '去安装')!
    await install.trigger('click')
    expect(router.push).toHaveBeenCalledWith('/skills')
  })

  // 报价只有平台管理员在 pending/processing 可编辑,交付后或需求方只读。
  it('quote editable only for admin in pending/processing', () => {
    detailState.data.value = { id: 't-1', title: '需求', status: 'processing', quote_amount_cents: 12000, messages: [] }
    let wrapper = mountPage()
    expect(wrapper.find('.quote-input').exists()).toBe(true)

    detailState.data.value = { id: 't-1', title: '需求', status: 'delivered', quote_amount_cents: 12000, messages: [] }
    wrapper = mountPage()
    expect(wrapper.find('.quote-input').exists()).toBe(false)
    expect(wrapper.text()).toContain('¥120.00')

    authState.user = { id: 'u-1', role: 'org_member' }
    detailState.data.value = { id: 't-1', title: '需求', status: 'processing', quote_amount_cents: 12000, messages: [] }
    wrapper = mountPage()
    expect(wrapper.find('.quote-input').exists()).toBe(false)
  })

  // 工单详情页定时刷新详情 query,用于在没有真实实时通道时模拟对话消息实时更新;组件卸载后必须清理定时器。
  it('polls ticket detail periodically and stops after unmount', () => {
    vi.useFakeTimers()
    try {
      detailState.data.value = { id: 't-1', title: '需求', status: 'pending', messages: [] }

      const wrapper = mountPage()
      expect(detailState.refetch).not.toHaveBeenCalled()

      vi.advanceTimersByTime(5_000)
      expect(detailState.refetch).toHaveBeenCalledTimes(1)

      wrapper.unmount()
      vi.advanceTimersByTime(5_000)
      expect(detailState.refetch).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })
})
