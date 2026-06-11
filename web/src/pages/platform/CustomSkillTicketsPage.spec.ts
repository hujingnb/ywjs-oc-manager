import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref, type VNodeChild } from 'vue'

import CustomSkillTicketsPage from './CustomSkillTicketsPage.vue'

const ticketsState = {
  data: ref<Record<string, unknown>[]>([]),
  isLoading: ref(false),
  error: ref<Error | null>(null),
}
const organizationsState = {
  data: ref<Record<string, unknown>[]>([]),
}
const router = { push: vi.fn() }

vi.mock('vue-router', () => ({
  useRouter: () => router,
}))

vi.mock('@/api/hooks/useSkillTickets', () => ({
  useAdminSkillTicketsQuery: () => ticketsState,
}))

vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationsQuery: () => organizationsState,
}))

vi.mock('naive-ui', async () => {
  const { defineComponent, h } = await import('vue')
  type Row = Record<string, unknown>
  interface Col { key: string; title?: string; render?: (row: Row) => VNodeChild }
  type RowProps = (row: Row) => Record<string, unknown>
  interface SelectOption { label: string; value: string }
  const NDataTable = defineComponent({
    props: {
      columns: { type: Array, default: () => [] },
      data: { type: Array, default: () => [] },
      rowProps: { type: Function, default: undefined },
    },
    setup(props: { columns: Col[]; data: Row[]; rowProps?: RowProps }) {
      return () => h('table', [
        h('thead', props.columns.map((col) => h('th', { class: `head-${col.key}` }, col.title ?? ''))),
        h('tbody', props.data.map((row) => h('tr', props.rowProps?.(row) ?? {}, props.columns.map((col) => h('td', { class: `cell-${col.key}` }, col.render ? [col.render(row)] : String(row[col.key] ?? '')))))),
      ])
    },
  })
  return {
    NDataTable,
    NButton: { template: '<button v-bind="$attrs"><slot /></button>' },
    NTag: { template: '<span><slot /></span>' },
    NInput: {
      props: ['value', 'size', 'clearable', 'placeholder'],
      emits: ['update:value'],
      template: '<input :value="value" @input="$emit(\'update:value\', $event.target.value)" />',
    },
    NSelect: {
      props: ['value', 'options', 'size'],
      emits: ['update:value'],
      setup(props: { value: string; options: SelectOption[] }, { emit, attrs }: { emit: (event: 'update:value', value: string) => void; attrs: Record<string, unknown> }) {
        return () => h('select', {
          ...attrs,
          value: props.value,
          onChange: (event: Event) => emit('update:value', (event.target as HTMLSelectElement).value),
        }, props.options.map((option) => h('option', { value: option.value }, option.label)))
      },
    },
  }
})

describe('CustomSkillTicketsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ticketsState.data.value = []
    organizationsState.data.value = []
    ticketsState.isLoading.value = false
    ticketsState.error.value = null
  })

  // 队列渲染状态/报价；操作列去掉后，点击工单整行进入详情页。
  it('renders queue and navigates to detail', async () => {
    ticketsState.data.value = [{ id: 't-1', title: '需求', status: 'pending', requester_role: 'org_member', quote_amount_cents: 12000 }]
    const wrapper = mount(CustomSkillTicketsPage)
    expect(wrapper.text()).toContain('需求')
    expect(wrapper.text()).toContain('待处理')
    expect(wrapper.text()).toContain('¥120.00')
    expect(wrapper.text()).not.toContain('操作')
    expect(wrapper.find('.head-actions').exists()).toBe(false)
    expect(wrapper.find('.cell-actions').exists()).toBe(false)
    expect(wrapper.find('button').exists()).toBe(false)
    await wrapper.find('[data-test="skill-ticket-row-t-1"]').trigger('click')
    expect(router.push).toHaveBeenCalledWith('/skill-tickets/t-1')
  })

  // 平台管理员可按组织过滤工单；组织、状态、标题过滤应在同一个列表上组合生效。
  it('filters queue by organization', async () => {
    organizationsState.data.value = [
      { id: 'org-1', name: '甲公司', code: 'alpha' },
      { id: 'org-2', name: '乙公司', code: 'beta' },
    ]
    ticketsState.data.value = [
      { id: 't-1', org_id: 'org-1', title: '甲公司需求', status: 'pending', requester_role: 'org_member' },
      { id: 't-2', org_id: 'org-2', title: '乙公司需求', status: 'pending', requester_role: 'org_member' },
    ]

    const wrapper = mount(CustomSkillTicketsPage)
    expect(wrapper.text()).toContain('甲公司需求')
    expect(wrapper.text()).toContain('乙公司需求')
    expect(wrapper.text()).toContain('甲公司（alpha）')

    await wrapper.find('select.org-filter').setValue('org-1')
    expect(wrapper.text()).toContain('甲公司需求')
    expect(wrapper.text()).not.toContain('乙公司需求')
  })
})
