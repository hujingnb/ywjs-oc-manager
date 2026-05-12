import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref, type PropType } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import type { DataTableColumn } from 'naive-ui'

import OrganizationsPage from './OrganizationsPage.vue'
import type { Organization } from '@/api'

const createOrganization = vi.hoisted(() => vi.fn())

// 组织列表页测试只 mock 列表和充值 hooks，验证充值留在弹框内完成而不跳转旧页面。
vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationsQuery: () => ({
    data: ref([{
      id: 'org-1',
      name: '测试组织',
      code: 'test-org',
      status: 'active',
      credit_warning_threshold: 20,
    }]),
    isLoading: ref(false),
    error: ref(null),
  }),
  useCreateOrganization: () => ({ mutateAsync: createOrganization, isPending: ref(false) }),
  useUpdateOrganizationStatus: () => ({ mutate: vi.fn() }),
}))

vi.mock('@/api/hooks/useRecharge', () => ({
  useOrgBalanceQuery: () => ({
    data: ref({ newapi_user_id: 4, remain_quota: 0, used_quota: 0 }),
    isLoading: ref(false),
    error: ref(null),
  }),
  useRechargeMutation: () => ({ mutateAsync: vi.fn(), isPending: ref(false) }),
}))

describe('OrganizationsPage', () => {
  const mountPage = () => mount(OrganizationsPage, {
    global: {
      stubs: {
        NButton: defineComponent({
          props: ['loading', 'disabled'],
          emits: ['click'],
          setup(_, { slots, emit }) {
            return () => h('button', { onClick: () => emit('click') }, slots.default?.())
          },
        }),
        NCard: defineComponent({
          setup(_, { slots }) {
            return () => h('section', [slots.header?.(), slots.default?.()])
          },
        }),
        NForm: defineComponent({
          props: ['model'],
          setup(_, { slots }) {
            return () => h('form', slots.default?.())
          },
        }),
        NFormItem: defineComponent({
          props: ['label'],
          setup(props, { slots }) {
            return () => h('label', [h('span', props.label), slots.default?.()])
          },
        }),
        NGrid: defineComponent({
          setup(_, { slots }) {
            return () => h('div', slots.default?.())
          },
        }),
        NGridItem: defineComponent({
          setup(_, { slots }) {
            return () => h('div', slots.default?.())
          },
        }),
        NInput: defineComponent({
          name: 'NInput',
          props: ['value'],
          emits: ['update:value'],
          setup(props, { emit }) {
            return () => h('input', {
              value: props.value,
              onInput: (event: Event) => emit('update:value', (event.target as HTMLInputElement).value),
            })
          },
        }),
        NInputNumber: defineComponent({
          props: ['value'],
          emits: ['update:value'],
          setup(props, { emit }) {
            return () => h('input', {
              value: props.value ?? '',
              onInput: (event: Event) => emit('update:value', Number((event.target as HTMLInputElement).value)),
            })
          },
        }),
        NSpace: defineComponent({
          setup(_, { slots }) {
            return () => h('div', slots.default?.())
          },
        }),
        NModal: true,
        DataTableList: defineComponent({
          props: {
            title: String,
            columns: { type: Array as PropType<DataTableColumn<Organization>[]>, required: true },
            data: { type: Array as PropType<Organization[]>, required: true },
          },
          setup(props, { slots }) {
            const columnTitle = (column: DataTableColumn<Organization>) => {
              if ('title' in column && column.title) return String(column.title)
              if ('key' in column && column.key) return String(column.key)
              return ''
            }
            return () => h('section', [
              slots.toolbar?.(),
              h('table', [
                h('thead', props.columns.map(column => h('th', columnTitle(column)))),
                h('tbody', props.data.map(row => h('tr', { key: row.id }, props.columns.map((column) => {
                  if ('render' in column && column.render) {
                    return h('td', [column.render(row, 0)])
                  }
                  const key = 'key' in column ? column.key : undefined
                  return h('td', key ? String(row[key as keyof Organization] ?? '') : '')
                })))),
              ]),
            ])
          },
        }),
      },
    },
  })

  it('在组织列表中提供弹框充值入口', () => {
    const wrapper = mountPage()

    expect(wrapper.text()).toContain('充值')
    expect(wrapper.text()).not.toContain('返回组织列表')
  })

  it('在组织列表中展示组织标识', () => {
    const wrapper = mountPage()

    expect(wrapper.text()).toContain('组织标识')
    expect(wrapper.text()).toContain('test-org')
  })

  it('创建组织时提交组织标识', async () => {
    createOrganization.mockResolvedValue({ id: 'org-2', name: '新组织', code: 'new-org', status: 'active' })
    const wrapper = mountPage()

    const openButton = wrapper.findAll('button').find(button => button.text().includes('新增组织'))
    expect(openButton).toBeTruthy()
    await openButton!.trigger('click')
    await nextTick()

    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('新组织')
    await inputs[1].setValue('new-org')
    await inputs[2].setValue('org-admin')
    await inputs[3].setValue('组织管理员')
    await inputs[4].setValue('secret-password')
    await wrapper.find('form').trigger('submit')

    expect(createOrganization).toHaveBeenCalledWith(expect.objectContaining({
      name: '新组织',
      code: 'new-org',
      admin_username: 'org-admin',
      admin_display_name: '组织管理员',
      admin_password: 'secret-password',
    }))
  })
})
