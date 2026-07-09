import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref, type PropType } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import type { DataTableColumn } from 'naive-ui'

import { i18n } from '@/i18n'
import OrganizationsPage from './OrganizationsPage.vue'
import type { Organization } from '@/api'

const createOrganization = vi.hoisted(() => vi.fn())
const updateOrganization = vi.hoisted(() => vi.fn())
const updateOrganizationAICCConfig = vi.hoisted(() => vi.fn())
const bytesPerGB = 1024 * 1024 * 1024

// organizationsState 允许单个测试覆盖组织容量，验证编辑表单不会丢失非整 GB bytes。
const organizationsState = vi.hoisted(() => {
  const defaultOrg = {
    id: 'org-1',
    name: '测试企业',
    code: 'test-org',
    status: 'active',
    credit_warning_threshold: 20,
    knowledge_quota_bytes: 1073741824,
    admin_username: 'org-admin',
    contact_name: '张三',
    contact_phone: '13800138000',
    remark: '测试备注',
    assistant_version_ids: ['v-1'],
    aicc_enabled: true,
    aicc_agent_limit: 5,
  }
  return {
    defaultOrg,
    items: [{ ...defaultOrg }],
  }
})

// versionsState 模拟助手版本列表查询状态，供创建组织表单多选使用。
const versionsState = vi.hoisted(() => ({
  data: { value: [
    { id: 'v-1', name: '版本 A' },
    { id: 'v-2', name: '版本 B' },
  ] },
  isLoading: { value: false },
  isError: { value: false },
}))

// 组织列表页测试只 mock 列表和充值 hooks，验证充值留在弹框内完成而不跳转旧页面。
vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationsQuery: () => ({
    data: ref(organizationsState.items),
    isLoading: ref(false),
    error: ref(null),
  }),
  // useModelsQuery 保留 mock：其他页面（如版本编辑页）仍依赖此导出，避免影响其他测试。
  useModelsQuery: () => ({ data: ref([]), isLoading: ref(false), isError: ref(false) }),
  useCreateOrganization: () => ({ mutateAsync: createOrganization, isPending: ref(false) }),
  // useUpdateOrganization mock 供编辑组织场景使用。
  useUpdateOrganization: () => ({ mutateAsync: updateOrganization, isPending: ref(false) }),
  // useUpdateOrganizationAICCConfig mock 供编辑组织时保存 AICC 开通配置。
  useUpdateOrganizationAICCConfig: () => ({ mutateAsync: updateOrganizationAICCConfig, isPending: ref(false) }),
  useUpdateOrganizationStatus: () => ({ mutate: vi.fn() }),
}))

// mock 助手版本查询，供创建表单版本多选使用。
vi.mock('@/api/hooks/useAssistantVersions', () => ({
  useAssistantVersionsQuery: () => versionsState,
}))

vi.mock('@/api/hooks/useRecharge', () => ({
  useBillingStatusQuery: () => ({ data: ref(null) }),
  useOrgBalanceQuery: () => ({
    data: ref({ newapi_user_id: 4, remain_quota: 0, used_quota: 0 }),
    isLoading: ref(false),
    error: ref(null),
  }),
  useRechargeMutation: () => ({ mutateAsync: vi.fn(), isPending: ref(false) }),
  useRechargesQuery: () => ({ data: ref([]), isLoading: ref(false) }),
}))

describe('OrganizationsPage', () => {
  // clipboardMock 捕获复制信息动作，避免测试依赖真实浏览器剪贴板权限。
  const clipboardMock = vi.fn()

  beforeEach(() => {
    createOrganization.mockReset()
    updateOrganization.mockReset()
    updateOrganizationAICCConfig.mockReset()
    clipboardMock.mockReset()
    organizationsState.items = [{ ...organizationsState.defaultOrg }]
    // 测试断言中文文案，设置 zh 语言以匹配 t() 返回值。
    i18n.global.locale.value = 'zh'
  })

  const mountPage = () => mount(OrganizationsPage, {
    global: {
      // 注入 QueryClient，解决 useQueries 调用报 "No 'queryClient' found" 的问题。
      // 注入 i18n 插件，OrganizationsPage 使用 useI18n() 需要。
      plugins: [[VueQueryPlugin, { queryClient: new QueryClient() }], i18n],
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
        NSwitch: defineComponent({
          props: ['value'],
          emits: ['update:value'],
          setup(props, { emit }) {
            return () => h('input', {
              checked: Boolean(props.value),
              type: 'checkbox',
              onChange: (event: Event) => emit('update:value', (event.target as HTMLInputElement).checked),
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

  it('在企业列表中提供弹框充值入口', () => {
    const wrapper = mountPage()

    expect(wrapper.text()).toContain('充值')
    expect(wrapper.text()).not.toContain('返回企业列表')
  })

  it('在企业列表中展示企业标识', () => {
    const wrapper = mountPage()

    expect(wrapper.text()).toContain('企业标识')
    expect(wrapper.text()).toContain('test-org')
  })

  it('复制企业信息时写入指定格式的管理员登录信息', async () => {
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
      '名称： 测试企业',
      '管理员账号： org-admin',
      '管理员密码： <创建时设置，系统不保存明文；如忘记请重置密码>',
    ].join('\n'))
  })

  // 创建组织时选择助手版本，验证提交载荷包含 assistant_version_ids 而不再有 model_id。
  it('创建企业时提交企业标识和助手版本', async () => {
    createOrganization.mockResolvedValue({ id: 'org-2', name: '新企业', code: 'new-org', status: 'active' })
    const wrapper = mountPage()

    const openButton = wrapper.findAll('button').find(button => button.text().includes('新增企业'))
    expect(openButton).toBeTruthy()
    await openButton!.trigger('click')
    await nextTick()

    const inputs = wrapper.findAll('input')
    // 按表单字段顺序填写：名称、组织标识、管理员账号、管理员姓名、管理员密码
    await inputs[0].setValue('新企业')
    await inputs[1].setValue('new-org')
    await inputs[2].setValue('org-admin')
    await inputs[3].setValue('企业管理员')
    await inputs[4].setValue('secret-password')

    // 选择助手版本（版本 A），通过直接设置 option selected 状态并触发 change 事件来模拟多选。
    const versionSelect = wrapper.find('select')
    const options = versionSelect.element.options
    if (options.length > 0) {
      options[0].selected = true
    }
    await versionSelect.trigger('change')
    await wrapper.find('form').trigger('submit')

    expect(createOrganization).toHaveBeenCalledWith(expect.objectContaining({
      name: '新企业',
      code: 'new-org',
      admin_username: 'org-admin',
      admin_display_name: '企业管理员',
      admin_password: 'secret-password',
      assistant_version_ids: expect.any(Array),
    }))
    // 确认不再有 model_id 字段
    expect(createOrganization).not.toHaveBeenCalledWith(expect.objectContaining({ model_id: expect.anything() }))
  })

  // 企业知识库容量由平台管理员在企业表单中设置，提交给后端时使用 bytes。
  it('创建企业时提交企业知识库容量 bytes', async () => {
    createOrganization.mockResolvedValue({ id: 'org-2', name: '新企业', code: 'new-org', status: 'active' })
    const wrapper = mountPage()

    const openButton = wrapper.findAll('button').find(button => button.text().includes('新增企业'))
    expect(openButton).toBeTruthy()
    await openButton!.trigger('click')
    await nextTick()

    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('新企业')
    await inputs[1].setValue('new-org')
    await inputs[2].setValue('org-admin')
    await inputs[3].setValue('企业管理员')
    await inputs[4].setValue('secret-password')

    const quotaInput = wrapper.findAll('input').find(input => (input.element as HTMLInputElement).value === '1')
    expect(quotaInput).toBeTruthy()
    await quotaInput!.setValue('2')
    await quotaInput!.trigger('blur')
    await wrapper.find('form').trigger('submit')

    expect(createOrganization).toHaveBeenCalledWith(expect.objectContaining({
      knowledge_quota_bytes: 2 * 1024 * 1024 * 1024,
    }))
    // knowledge_quota_gb 是页面内部展示字段，提交给后端时不应泄漏。
    expect(createOrganization.mock.calls.at(-1)?.[0]).not.toHaveProperty('knowledge_quota_gb')
  })

  // 编辑组织：点击「编辑」打开表单，验证预填数据正确且提交时携带 id、容量 bytes 与 assistant_version_ids。
  it('编辑企业时预填现有数据并提交 update mutation', async () => {
    updateOrganization.mockResolvedValue({
      id: 'org-1',
      name: '测试企业（已修改）',
      code: 'test-org',
      status: 'active',
    })
    const wrapper = mountPage()

    // 点击「编辑」按钮，应打开编辑模式的表单
    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    expect(editButton).toBeTruthy()
    await editButton!.trigger('click')
    await nextTick()

    // 表单应预填当前组织数据，首个 input 为名称字段
    const inputs = wrapper.findAll('input')
    // 预填名称应为「测试企业」
    const nameInput = inputs.find(i => (i.element as HTMLInputElement).value === '测试企业')
    expect(nameInput).toBeTruthy()
    const quotaInput = wrapper.findAll('input').find(input => (input.element as HTMLInputElement).value === '1')
    expect(quotaInput).toBeTruthy()
    expect((quotaInput!.element as HTMLInputElement).value).toBe('1')

    // 修改名称字段
    await nameInput!.setValue('测试企业（已修改）')
    // 修改知识库容量时，编辑提交应按 GB 转换为后端 bytes。
    await quotaInput!.setValue('3')
    await quotaInput!.trigger('blur')

    // 编辑模式下不应存在管理员账号输入项（create-only 字段）
    const labels = wrapper.findAll('label span')
    expect(labels.some(l => l.text().includes('管理员账号'))).toBe(false)

    // 提交表单
    await wrapper.find('form').trigger('submit')

    // 应调用 updateOrganization 并携带正确的 id 与 assistant_version_ids
    expect(updateOrganization).toHaveBeenCalledWith(expect.objectContaining({
      id: 'org-1',
      payload: expect.objectContaining({
        name: '测试企业（已修改）',
        knowledge_quota_bytes: 3 * bytesPerGB,
        assistant_version_ids: ['v-1'],
      }),
    }))
    expect(updateOrganization.mock.calls.at(-1)?.[0].payload).not.toHaveProperty('knowledge_quota_gb')
    expect(updateOrganizationAICCConfig).toHaveBeenCalledWith(expect.objectContaining({
      id: 'org-1',
      payload: {
        enabled: true,
        agent_limit: 5,
      },
    }))
  })

  // 编辑其他字段但未改动知识库容量时，保留后端原始 bytes，避免整 GB 展示造成静默舍入。
  it('编辑企业未修改知识库容量时保留原始 bytes', async () => {
    const originalBytes = bytesPerGB + 512
    organizationsState.items = [{
      ...organizationsState.defaultOrg,
      knowledge_quota_bytes: originalBytes,
    }]
    updateOrganization.mockResolvedValue({
      id: 'org-1',
      name: '测试企业（已修改）',
      code: 'test-org',
      status: 'active',
    })
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    expect(editButton).toBeTruthy()
    await editButton!.trigger('click')
    await nextTick()

    const quotaInput = wrapper.findAll('input').find(input => (input.element as HTMLInputElement).value === '1')
    expect(quotaInput).toBeTruthy()
    expect((quotaInput!.element as HTMLInputElement).value).toBe('1')

    const nameInput = wrapper.findAll('input').find(i => (i.element as HTMLInputElement).value === '测试企业')
    expect(nameInput).toBeTruthy()
    await nameInput!.setValue('测试企业（已修改）')
    await wrapper.find('form').trigger('submit')

    expect(updateOrganization).toHaveBeenCalledWith(expect.objectContaining({
      id: 'org-1',
      payload: expect.objectContaining({
        name: '测试企业（已修改）',
        knowledge_quota_bytes: originalBytes,
      }),
    }))
    expect(updateOrganization.mock.calls.at(-1)?.[0].payload).not.toHaveProperty('knowledge_quota_gb')
  })

  // 编辑企业 AICC 配置时，开关和智能体上限通过独立 mutation 保存。
  it('编辑企业时提交 AICC 开通配置', async () => {
    updateOrganization.mockResolvedValue({
      id: 'org-1',
      name: '测试企业',
      code: 'test-org',
      status: 'active',
    })
    updateOrganizationAICCConfig.mockResolvedValue({
      id: 'org-1',
      name: '测试企业',
      code: 'test-org',
      status: 'active',
      aicc_enabled: false,
      aicc_agent_limit: 8,
    })
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    expect(editButton).toBeTruthy()
    await editButton!.trigger('click')
    await nextTick()

    expect(wrapper.text()).toContain('开通 AICC')
    expect(wrapper.text()).toContain('AICC 智能体数量上限')
    await wrapper.find('form').trigger('submit')

    expect(updateOrganizationAICCConfig).toHaveBeenCalledWith(expect.objectContaining({
      id: 'org-1',
      payload: {
        enabled: true,
        agent_limit: 5,
      },
    }))
  })

  // 助手版本为可选项，留空时表单仍可正常提交。
  it('不选助手版本时表单仍可提交', async () => {
    createOrganization.mockResolvedValue({ id: 'org-3', name: '空版本企业', code: 'empty-org', status: 'active' })
    const wrapper = mountPage()

    const openButton = wrapper.findAll('button').find(button => button.text().includes('新增企业'))
    expect(openButton).toBeTruthy()
    await openButton!.trigger('click')
    await nextTick()

    const inputs = wrapper.findAll('input')
    // 仅填写必填字段，不选助手版本，验证可以正常提交。
    await inputs[0].setValue('空版本企业')
    await inputs[1].setValue('empty-org')
    await inputs[2].setValue('admin2')
    await inputs[3].setValue('管理员2')
    await inputs[4].setValue('password2')

    await wrapper.find('form').trigger('submit')

    expect(createOrganization).toHaveBeenCalledWith(expect.objectContaining({
      name: '空版本企业',
      assistant_version_ids: [],
    }))
  })
})
