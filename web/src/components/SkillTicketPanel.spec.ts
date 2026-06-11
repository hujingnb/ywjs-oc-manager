import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref, nextTick, type VNodeChild } from 'vue'

import SkillTicketPanel from './SkillTicketPanel.vue'

const ticketsState = {
  data: ref<Record<string, unknown>[]>([]),
  isLoading: ref(false),
  error: ref<Error | null>(null),
}
const router = { push: vi.fn() }

const mocks = vi.hoisted(() => ({
  submit: vi.fn(),
  upload: vi.fn(),
  error: vi.fn(),
}))

vi.mock('vue-router', () => ({
  useRouter: () => router,
}))

vi.mock('@/api/hooks/useSkillTickets', () => ({
  useMySkillTicketsQuery: () => ticketsState,
  useSubmitSkillTicket: () => ({ mutateAsync: mocks.submit, isPending: ref(false) }),
  useUploadTicketMessage: () => ({ mutateAsync: mocks.upload, isPending: ref(false) }),
}))

vi.mock('naive-ui', async () => {
  const { defineComponent, h } = await import('vue')
  type Row = Record<string, unknown>
  interface Col { key: string; title?: string; render?: (row: Row) => VNodeChild }
  const NDataTable = defineComponent({
    props: { columns: { type: Array, default: () => [] }, data: { type: Array, default: () => [] } },
    setup(props: { columns: Col[]; data: Row[] }) {
      return () => h('div', props.data.flatMap((row) => props.columns.map((col) => h('div', { class: `cell-${col.key}` }, col.render ? [col.render(row)] : String(row[col.key] ?? '')))))
    },
  })
  return {
    useMessage: () => ({ error: mocks.error }),
    NDataTable,
    NButton: { template: '<button v-bind="$attrs"><slot /></button>' },
    NTag: { template: '<span><slot /></span>' },
    NModal: { props: ['show'], template: '<div v-if="show" class="n-modal"><slot /><slot name="footer" /></div>' },
    NForm: { template: '<div><slot /></div>' },
    NFormItem: { template: '<label><slot /></label>' },
    NInput: { props: ['value'], emits: ['update:value'], template: '<input :value="value" @input="$emit(\'update:value\', $event.target.value)" />' },
  }
})

describe('SkillTicketPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ticketsState.data.value = []
    ticketsState.isLoading.value = false
    ticketsState.error.value = null
  })

  // 查看按钮跳转共享详情页,delivered 行保留去安装 emit。
  it('navigates to detail and emits goInstall for delivered ticket', async () => {
    ticketsState.data.value = [{ id: 't-1', title: '需求', status: 'delivered', custom_skill_name: 'weekly' }]
    const wrapper = mount(SkillTicketPanel)
    await wrapper.findAll('button').find((button) => button.text() === '查看')!.trigger('click')
    expect(router.push).toHaveBeenCalledWith('/skill-tickets/t-1')
    await wrapper.findAll('button').find((button) => button.text() === '去安装')!.trigger('click')
    expect(wrapper.emitted('goInstall')?.[0]).toEqual(['weekly'])
  })

  // 提交需求后先创建工单,再把选择的附件逐个作为消息上传,最后跳详情页。
  it('submits ticket and uploads selected files', async () => {
    mocks.submit.mockResolvedValueOnce({ id: 't-new', status: 'pending', title: '新需求' })
    const wrapper = mount(SkillTicketPanel)
    await wrapper.findAll('button').find((button) => button.text() === '提交需求')!.trigger('click')
    await nextTick()
    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('新需求')
    await inputs[1].setValue('详细描述')
    const file = new File(['pdf'], '说明.pdf', { type: 'application/pdf' })
    Object.defineProperty(wrapper.find('input[type="file"]').element, 'files', { value: [file] })
    await wrapper.find('input[type="file"]').trigger('change')
    await wrapper.findAll('button').find((button) => button.text() === '提交')!.trigger('click')

    expect(mocks.submit).toHaveBeenCalledWith({ title: '新需求', description: '详细描述' })
    expect(mocks.upload).toHaveBeenCalledWith(file)
    expect(router.push).toHaveBeenCalledWith('/skill-tickets/t-new')
  })
})
