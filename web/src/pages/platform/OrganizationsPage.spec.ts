import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref, type PropType } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import type { DataTableColumn } from 'naive-ui'

import OrganizationsPage from './OrganizationsPage.vue'
import type { Organization } from '@/api'

const createOrganization = vi.hoisted(() => vi.fn())
const modelsState = vi.hoisted(() => ({
  data: { value: [{ id: 'qwen2.5:7b', name: 'qwen2.5:7b' }] },
  isLoading: { value: false },
  isError: { value: false },
}))

// 组织列表页测试只 mock 列表和充值 hooks，验证充值留在弹框内完成而不跳转旧页面。
vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationsQuery: () => ({
    data: ref([{
      id: 'org-1',
      name: '测试组织',
      code: 'test-org',
      status: 'active',
      credit_warning_threshold: 20,
      admin_username: 'org-admin',
      model_id: 'qwen2.5:7b',
    }]),
    isLoading: ref(false),
    error: ref(null),
  }),
  useModelsQuery: () => modelsState,
  useCreateOrganization: () => ({ mutateAsync: createOrganization, isPending: ref(false) }),
  useUpdateOrganizationStatus: () => ({ mutate: vi.fn() }),
}))

vi.mock('@/api/hooks/useRecharge', () => ({
  useBillingStatusQuery: () => ({ data: ref(null) }),
  useOrgBalanceQuery: () => ({
    data: ref({ newapi_user_id: 4, remain_quota: 0, used_quota: 0 }),
    isLoading: ref(false),
    error: ref(null),
  }),
  useRechargeMutation: () => ({ mutateAsync: vi.fn(), isPending: ref(false) }),
}))

describe('OrganizationsPage', () => {
  // clipboardMock 捕获复制信息动作，避免测试依赖真实浏览器剪贴板权限。
  const clipboardMock = vi.fn()

  const mountPage = () => mount(OrganizationsPage, {
    global: {
      stubs: {
        NButton: defineComponent({
          props: ['loading', 'disabled'],
          emits: ['click'],
          setup(_, { slots, emit }) {
            return () => h('button', {
              disabled: _.disabled,
              onClick: () => emit('click'),
            }, slots.default?.())
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
        'n-select': defineComponent({
          props: {
            value: [String, Array],
            options: Array,
            disabled: Boolean,
            multiple: Boolean,
          },
          emits: ['update:value'],
          setup(props, { emit }) {
            return () => h('select', {
              disabled: props.disabled,
              multiple: props.multiple,
              value: props.value,
              onChange: (event: Event) => {
                const target = event.target as HTMLSelectElement
                if (props.multiple) {
                  emit('update:value', Array.from(target.selectedOptions).map(option => option.value))
                  return
                }
                emit('update:value', target.value)
              },
            }, ((props.options ?? []) as Array<{ label: string; value: string }>).map(option =>
              h('option', { value: option.value }, option.label),
            ))
          },
        }),
        Select: defineComponent({
          props: {
            value: [String, Array],
            options: Array,
            disabled: Boolean,
            multiple: Boolean,
          },
          emits: ['update:value'],
          setup(props, { emit }) {
            return () => h('select', {
              disabled: props.disabled,
              multiple: props.multiple,
              value: props.value,
              onChange: (event: Event) => {
                const target = event.target as HTMLSelectElement
                if (props.multiple) {
                  emit('update:value', Array.from(target.selectedOptions).map(option => option.value))
                  return
                }
                emit('update:value', target.value)
              },
            }, ((props.options ?? []) as Array<{ label: string; value: string }>).map(option =>
              h('option', { value: option.value }, option.label),
            ))
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

  it('复制组织信息时写入指定格式的管理员登录信息', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: clipboardMock },
    })
    clipboardMock.mockResolvedValue(undefined)
    const wrapper = mountPage()

    const copyButton = wrapper.findAll('button').find(button => button.text().includes('复制信息'))
    expect(copyButton).toBeTruthy()
    await copyButton!.trigger('click')

    expect(clipboardMock).toHaveBeenCalledWith([
      '标识： test-org',
      '名称： 测试组织',
      '管理员用户名： org-admin',
      '管理员密码： <创建时设置，系统不保存明文；如忘记请重置密码>',
    ].join('\n'))
  })

  it('创建组织时提交组织标识', async () => {
    modelsState.isError.value = false
    modelsState.data.value = [{ id: 'qwen2.5:7b', name: 'qwen2.5:7b' }]
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
    const modelSelect = wrapper.find('select')
    await modelSelect.setValue('qwen2.5:7b')
    await wrapper.find('form').trigger('submit')

    expect(createOrganization).toHaveBeenCalledWith(expect.objectContaining({
      name: '新组织',
      code: 'new-org',
      model_id: 'qwen2.5:7b',
      admin_username: 'org-admin',
      admin_display_name: '组织管理员',
      admin_password: 'secret-password',
    }))
  })

  it('模型列表加载失败时禁用保存并阻止提交', async () => {
    modelsState.isError.value = true
    createOrganization.mockClear()
    const wrapper = mountPage()

    const openButton = wrapper.findAll('button').find(button => button.text().includes('新增组织'))
    expect(openButton).toBeTruthy()
    await openButton!.trigger('click')
    await nextTick()

    const saveButton = wrapper.findAll('button').find(button => button.text() === '保存')
    expect(saveButton?.attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('模型列表获取失败，请重试')

    await wrapper.find('form').trigger('submit')
    expect(createOrganization).not.toHaveBeenCalled()
  })
})
