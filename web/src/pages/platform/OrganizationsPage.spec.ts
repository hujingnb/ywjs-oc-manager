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
const routerPush = vi.hoisted(() => vi.fn())
const bytesPerGB = 1024 * 1024 * 1024

// deferredPromise 让用例控制 PATCH 完成时机，用于复现请求期间关闭或切换企业的竞态。
function deferredPromise<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve
    reject = promiseReject
  })
  return { promise, resolve, reject }
}

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
    industry_knowledge_base_ids: ['industry-1'],
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
  isFetching: { value: false },
  isError: { value: false },
}))

// modelsState 模拟实时模型目录，覆盖正常加载、目录失败与换模选择场景。
const modelsState = vi.hoisted(() => ({
  data: { value: [
    { id: 'qwen3.5:27b', name: 'Qwen 3.5 27B' },
    { id: 'deepseek-v3', name: 'DeepSeek V3' },
  ] },
  isLoading: { value: false },
  isFetching: { value: false },
  isError: { value: false },
}))

// aiccConfigState 模拟独立 AICC 配置 GET，确保编辑页不再依赖组织列表中的兼容字段。
const aiccConfigState = vi.hoisted(() => ({
  data: { value: {
    org_id: 'org-1',
    enabled: true,
    model: 'qwen3.5:27b',
    agent_limit: 5,
    revision: 2,
    industry_knowledge_bases: [{ id: 'industry-1', name: '行业库 A' }],
  } },
  isLoading: { value: false },
  isFetching: { value: false },
  isError: { value: false },
  error: { value: null },
}))

// 组织列表页测试只 mock 列表和充值 hooks，验证充值留在弹框内完成而不跳转旧页面。
vi.mock('@/api/hooks/useOrganizations', () => ({
  useOrganizationsQuery: () => ({
    data: ref(organizationsState.items),
    isLoading: ref(false),
    error: ref(null),
  }),
  // useModelsQuery 返回可切换状态，覆盖模型正常加载和失败封闭两类交互。
  useModelsQuery: () => modelsState,
  // useOrganizationAICCConfigQuery 返回独立配置，页面应在打开编辑表单后据此回填。
  useOrganizationAICCConfigQuery: () => aiccConfigState,
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

// mock 平台行业知识库查询，供企业 AICC 授权多选的回显和保存测试使用。
vi.mock('@/api/hooks/useIndustryKnowledge', () => ({
  useIndustryKnowledgeBasesQuery: () => ({
    data: ref({ items: [{ id: 'industry-1', name: '行业库 A' }, { id: 'industry-2', name: '行业库 B' }] }),
  }),
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

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: routerPush }),
}))

describe('OrganizationsPage', () => {
  // clipboardMock 捕获复制信息动作，避免测试依赖真实浏览器剪贴板权限。
  const clipboardMock = vi.fn()

  beforeEach(() => {
    createOrganization.mockReset()
    updateOrganization.mockReset()
    updateOrganizationAICCConfig.mockReset()
    routerPush.mockReset()
    clipboardMock.mockReset()
    organizationsState.items = [{ ...organizationsState.defaultOrg }]
    modelsState.data.value = [
      { id: 'qwen3.5:27b', name: 'Qwen 3.5 27B' },
      { id: 'deepseek-v3', name: 'DeepSeek V3' },
    ]
    modelsState.isLoading.value = false
    modelsState.isFetching.value = false
    modelsState.isError.value = false
    aiccConfigState.data.value = {
      org_id: 'org-1',
      enabled: true,
      model: 'qwen3.5:27b',
      agent_limit: 5,
      revision: 2,
      industry_knowledge_bases: [{ id: 'industry-1', name: '行业库 A' }],
    }
    aiccConfigState.isLoading.value = false
    aiccConfigState.isFetching.value = false
    aiccConfigState.isError.value = false
    aiccConfigState.error.value = null
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
          props: ['value', 'disabled'],
          emits: ['update:value'],
          setup(props, { emit }) {
            return () => h('input', {
              value: props.value ?? '',
              disabled: props.disabled,
              onInput: (event: Event) => emit('update:value', Number((event.target as HTMLInputElement).value)),
            })
          },
        }),
        NSwitch: defineComponent({
          props: ['value', 'disabled'],
          emits: ['update:value'],
          setup(props, { emit }) {
            return () => h('input', {
              checked: Boolean(props.value),
              type: 'checkbox',
              disabled: props.disabled,
              onChange: (event: Event) => emit('update:value', (event.target as HTMLInputElement).checked),
            })
          },
        }),
        'n-select': defineComponent({
          name: 'NSelect',
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
            }, [
              ...((props.options ?? []) as Array<{ label: string; value: string }>).map(option =>
                h('option', { value: option.value }, option.label),
              ),
              // 仅模型选择器追加已下架值，用于模拟目录刷新后当前选择失效。
              ...((props.options ?? []) as Array<{ value: string }>).some(option => option.value === 'qwen3.5:27b')
                ? [h('option', { value: 'removed-model' }, '已下架模型')]
                : [],
            ])
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
            }, [
              ...((props.options ?? []) as Array<{ label: string; value: string }>).map(option =>
                h('option', { value: option.value }, option.label),
              ),
              // 仅模型选择器追加已下架值，用于模拟目录刷新后当前选择失效。
              ...((props.options ?? []) as Array<{ value: string }>).some(option => option.value === 'qwen3.5:27b')
                ? [h('option', { value: 'removed-model' }, '已下架模型')]
                : [],
            ])
          },
        }),
        NSpace: defineComponent({
          setup(_, { slots }) {
            return () => h('div', slots.default?.())
          },
        }),
        NModal: true,
        ConfirmActionModal: defineComponent({
          props: ['visible', 'title', 'message'],
          emits: ['confirm', 'cancel'],
          setup(props, { emit }) {
            return () => props.visible
              ? h('section', { 'data-testid': 'model-change-confirm' }, [
                  h('h3', props.title),
                  h('p', props.message),
                  h('button', { onClick: () => emit('cancel') }, '取消换模'),
                  h('button', { onClick: () => emit('confirm') }, '确认换模'),
                ])
              : null
          },
        }),
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

  // AICC 入口位于企业行操作内；只对已开通 AICC 的企业展示，并携带企业 ID 进入独立工作台。
  it('为已开通 AICC 的企业提供进入 AICC 工作台入口', async () => {
    const wrapper = mountPage()

    const aiccButton = wrapper.findAll('button').find(button => button.text().includes('进入 AICC'))
    expect(aiccButton).toBeTruthy()
    await aiccButton!.trigger('click')

    expect(routerPush).toHaveBeenCalledWith({ path: '/aicc-console', query: { org_id: 'org-1' } })
  })

  // 未开通 AICC 的企业不能展示工作台入口，避免平台管理员进入后看到无数据或误以为已开通。
  it('未开通 AICC 的企业不展示进入 AICC 工作台入口', () => {
    organizationsState.items = [{ ...organizationsState.defaultOrg, aicc_enabled: false }]

    const wrapper = mountPage()

    expect(wrapper.text()).not.toContain('进入 AICC')
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
        model: 'qwen3.5:27b',
        agent_limit: 5,
        industry_knowledge_base_ids: ['industry-1'],
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

  // 编辑企业后点击主保存按钮，AICC 开关、智能体上限和行业库授权必须一并提交。
  it('编辑企业时主保存按钮会提交 AICC 开通配置', async () => {
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
    await nextTick()

    expect(updateOrganizationAICCConfig).toHaveBeenCalledWith(expect.objectContaining({
      id: 'org-1',
      payload: {
        enabled: true,
        model: 'qwen3.5:27b',
        agent_limit: 5,
        industry_knowledge_base_ids: ['industry-1'],
      },
    }))
  })

  // 覆盖真实点击路径：Naive UI 的 attr-type 不应成为唯一提交机制，点击主保存按钮必须触发组织与 AICC 配置写入。
  it('点击编辑表单主保存按钮会提交组织和 AICC 配置', async () => {
    updateOrganization.mockResolvedValue({ id: 'org-1', name: '测试企业', code: 'test-org', status: 'active' })
    updateOrganizationAICCConfig.mockResolvedValue({ id: 'org-1', name: '测试企业', code: 'test-org', status: 'active', aicc_enabled: true })
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    expect(editButton).toBeTruthy()
    await editButton!.trigger('click')
    await nextTick()

    const saveButton = wrapper.findAll('button').find(button => button.text() === '保存')
    expect(saveButton).toBeTruthy()
    await saveButton!.trigger('click')
    await nextTick()

    expect(updateOrganization).toHaveBeenCalledWith(expect.objectContaining({ id: 'org-1' }))
    expect(updateOrganizationAICCConfig).toHaveBeenCalledWith(expect.objectContaining({ id: 'org-1' }))
  })

  // 启用 AICC 时模型是必填项；空模型必须在前端阻断，不能发出不完整的 PUT 请求。
  it('启用 AICC 但未选择模型时阻断保存', async () => {
    aiccConfigState.data.value = {
      ...aiccConfigState.data.value,
      model: '',
    }
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    expect(editButton).toBeTruthy()
    await editButton!.trigger('click')
    await nextTick()

    const saveAICCButton = wrapper.findAll('button').find(button => button.text().includes('保存 AICC 配置'))
    expect(saveAICCButton).toBeTruthy()
    await saveAICCButton!.trigger('click')
    await nextTick()

    expect(updateOrganizationAICCConfig).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('启用 AICC 时必须选择客服模型')
  })

  // 独立配置保存必须提交模型、限额和行业授权的完整快照，匹配后端 PUT 契约。
  it('保存 AICC 配置时提交包含模型的完整载荷', async () => {
    updateOrganizationAICCConfig.mockResolvedValue(aiccConfigState.data.value)
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    expect(editButton).toBeTruthy()
    await editButton!.trigger('click')
    await nextTick()
    const saveAICCButton = wrapper.findAll('button').find(button => button.text().includes('保存 AICC 配置'))
    await saveAICCButton!.trigger('click')
    await nextTick()

    expect(updateOrganizationAICCConfig).toHaveBeenCalledWith({
      id: 'org-1',
      payload: {
        enabled: true,
        model: 'qwen3.5:27b',
        agent_limit: 5,
        industry_knowledge_base_ids: ['industry-1'],
      },
    })
  })

  // 已启用企业更换客服模型会影响运行中智能体，提交前必须展示逐个静默重启确认。
  it('修改已启用企业的模型时确认后才提交', async () => {
    updateOrganizationAICCConfig.mockResolvedValue(aiccConfigState.data.value)
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    await editButton!.trigger('click')
    await nextTick()
    const modelSelect = wrapper.findAll('select').find(select => select.text().includes('Qwen 3.5 27B'))
    expect(modelSelect).toBeTruthy()
    await modelSelect!.setValue('deepseek-v3')
    const saveAICCButton = wrapper.findAll('button').find(button => button.text().includes('保存 AICC 配置'))
    await saveAICCButton!.trigger('click')
    await nextTick()

    expect(updateOrganizationAICCConfig).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('逐个静默重启该企业正在运行的智能客服')
    await wrapper.get('[data-testid="model-change-confirm"] button:last-child').trigger('click')
    await nextTick()

    expect(updateOrganizationAICCConfig).toHaveBeenCalledWith(expect.objectContaining({
      id: 'org-1',
      payload: expect.objectContaining({ model: 'deepseek-v3' }),
    }))
  })

  // 确认窗口期间模型目录进入后台刷新时必须重新校验，不能沿用打开弹框时的旧目录提交 PUT。
  it('换模确认时模型目录正在刷新则阻断提交', async () => {
    const wrapper = mountPage()

    await wrapper.findAll('button').find(button => button.text().includes('编辑'))!.trigger('click')
    await nextTick()
    const modelSelect = wrapper.findAll('select').find(select => select.text().includes('Qwen 3.5 27B'))
    await modelSelect!.setValue('deepseek-v3')
    await wrapper.findAll('button').find(button => button.text().includes('保存 AICC 配置'))!.trigger('click')
    await nextTick()
    modelsState.isFetching.value = true
    await wrapper.get('[data-testid="model-change-confirm"] button:last-child').trigger('click')
    await nextTick()

    expect(updateOrganizationAICCConfig).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('企业 AICC 配置或客服模型目录尚未加载完成')
  })

  // 确认窗口期间模型目录请求失败时必须 fail-closed，并展示目录加载错误。
  it('换模确认时模型目录失败则阻断提交', async () => {
    const wrapper = mountPage()

    await wrapper.findAll('button').find(button => button.text().includes('编辑'))!.trigger('click')
    await nextTick()
    const modelSelect = wrapper.findAll('select').find(select => select.text().includes('Qwen 3.5 27B'))
    await modelSelect!.setValue('deepseek-v3')
    await wrapper.findAll('button').find(button => button.text().includes('保存 AICC 配置'))!.trigger('click')
    await nextTick()
    modelsState.isError.value = true
    await wrapper.get('[data-testid="model-change-confirm"] button:last-child').trigger('click')
    await nextTick()

    expect(updateOrganizationAICCConfig).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('客服模型目录加载失败，暂时无法保存')
  })

  // 确认窗口期间新选模型被下架时必须按最新目录重新判断，不能提交已失效模型。
  it('换模确认时所选模型已下架则阻断提交', async () => {
    const wrapper = mountPage()

    await wrapper.findAll('button').find(button => button.text().includes('编辑'))!.trigger('click')
    await nextTick()
    const modelSelect = wrapper.findAll('select').find(select => select.text().includes('Qwen 3.5 27B'))
    await modelSelect!.setValue('deepseek-v3')
    await wrapper.findAll('button').find(button => button.text().includes('保存 AICC 配置'))!.trigger('click')
    await nextTick()
    modelsState.data.value = [{ id: 'qwen3.5:27b', name: 'Qwen 3.5 27B' }]
    // mock 查询状态不是 Vue ref，重新触发模型选择以让计算属性读取最新目录。
    await modelSelect!.setValue('qwen3.5:27b')
    await modelSelect!.setValue('deepseek-v3')
    await wrapper.get('[data-testid="model-change-confirm"] button:last-child').trigger('click')
    await nextTick()

    expect(updateOrganizationAICCConfig).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('当前选择的客服模型 deepseek-v3 已不在实时模型目录中')
  })

  // 换模确认弹框取消后不应写入配置，避免误点保存触发运行中客服重启。
  it('取消客服模型变更确认时不提交', async () => {
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    await editButton!.trigger('click')
    await nextTick()
    const modelSelect = wrapper.findAll('select').find(select => select.text().includes('Qwen 3.5 27B'))
    expect(modelSelect).toBeTruthy()
    await modelSelect!.setValue('deepseek-v3')
    const saveAICCButton = wrapper.findAll('button').find(button => button.text().includes('保存 AICC 配置'))
    await saveAICCButton!.trigger('click')
    await nextTick()
    await wrapper.get('[data-testid="model-change-confirm"] button:first-of-type').trigger('click')

    expect(updateOrganizationAICCConfig).not.toHaveBeenCalled()
  })

  // 实时模型目录加载失败时采用 fail-closed：禁用保存并给出明确提示。
  it('模型目录加载失败时禁用 AICC 保存', async () => {
    modelsState.isError.value = true
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    await editButton!.trigger('click')
    await nextTick()
    const saveAICCButton = wrapper.findAll('button').find(button => button.text().includes('保存 AICC 配置'))

    expect(saveAICCButton!.attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('客服模型目录加载失败，暂时无法保存')
  })

  // 已有缓存进入后台刷新时仍可能过期，刷新完成前必须禁用完整快照 PUT。
  it('AICC 配置或模型目录后台刷新时禁用保存', async () => {
    aiccConfigState.isFetching.value = true
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    await editButton!.trigger('click')
    await nextTick()
    const saveAICCButton = wrapper.findAll('button').find(button => button.text().includes('保存 AICC 配置'))

    expect(saveAICCButton!.attributes('disabled')).toBeDefined()
    expect(updateOrganizationAICCConfig).not.toHaveBeenCalled()
  })

  // AICC 独立 GET 首次加载或失败前，开关、限额、模型和行业授权都不能编辑，避免把空白临时态当作配置保存。
  it('AICC 配置首次加载时禁用全部可编辑控件', async () => {
    aiccConfigState.isLoading.value = true
    const wrapper = mountPage()

    await wrapper.findAll('button').find(button => button.text().includes('编辑'))!.trigger('click')
    await nextTick()
    const disabledInputs = wrapper.findAll('input').filter(input => input.attributes('disabled') !== undefined)
    const modelSelect = wrapper.findAll('select').find(select => select.text().includes('Qwen 3.5 27B'))
    const industrySelect = wrapper.findAll('select').find(select => select.text().includes('行业库 B'))

    expect(wrapper.find('[role="switch"]').classes()).toContain('n-switch--disabled')
    expect(disabledInputs.length).toBeGreaterThan(0)
    expect(modelSelect!.attributes('disabled')).toBeDefined()
    expect(industrySelect!.attributes('disabled')).toBeDefined()
  })

  // 当前新选模型若已不在最新目录中，即使原模型仍可用也必须 fail-closed。
  it('当前选择的客服模型不在目录时禁用保存', async () => {
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    await editButton!.trigger('click')
    await nextTick()
    const modelSelect = wrapper.findAll('select').find(select => select.text().includes('已下架模型'))
    expect(modelSelect).toBeTruthy()
    await modelSelect!.setValue('removed-model')
    await nextTick()
    const saveAICCButton = wrapper.findAll('button').find(button => button.text().includes('保存 AICC 配置'))

    expect(saveAICCButton!.attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('当前选择的客服模型 removed-model 已不在实时模型目录中')
  })

  // 原模型已下架时允许改选目录内有效模型，确认换模后即可解除 fail-closed 并保存。
  it('原客服模型下架后改选有效模型可以确认保存', async () => {
    aiccConfigState.data.value = { ...aiccConfigState.data.value, model: 'retired-model' }
    updateOrganizationAICCConfig.mockResolvedValue(aiccConfigState.data.value)
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    await editButton!.trigger('click')
    await nextTick()
    const modelSelect = wrapper.findAll('select').find(select => select.text().includes('Qwen 3.5 27B'))
    await modelSelect!.setValue('qwen3.5:27b')
    const saveAICCButton = wrapper.findAll('button').find(button => button.text().includes('保存 AICC 配置'))
    expect(saveAICCButton!.attributes('disabled')).toBeUndefined()
    await saveAICCButton!.trigger('click')
    await nextTick()
    await wrapper.get('[data-testid="model-change-confirm"] button:last-child').trigger('click')

    expect(updateOrganizationAICCConfig).toHaveBeenCalledWith(expect.objectContaining({
      payload: expect.objectContaining({ model: 'qwen3.5:27b' }),
    }))
  })

  // 企业虽已关闭 AICC，但修改保留模型仍会触发后端 rollout，必须二次确认。
  it('已关闭企业修改保留模型时仍要求确认', async () => {
    aiccConfigState.data.value = { ...aiccConfigState.data.value, enabled: false }
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    await editButton!.trigger('click')
    await nextTick()
    const modelSelect = wrapper.findAll('select').find(select => select.text().includes('Qwen 3.5 27B'))
    await modelSelect!.setValue('deepseek-v3')
    const saveAICCButton = wrapper.findAll('button').find(button => button.text().includes('保存 AICC 配置'))
    await saveAICCButton!.trigger('click')
    await nextTick()

    expect(updateOrganizationAICCConfig).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="model-change-confirm"]').exists()).toBe(true)
  })

  // 仅调整智能体限额和行业授权不会触发运行时换模，直接提交完整 PUT 快照。
  it('仅修改智能体限额和行业授权时不弹换模确认', async () => {
    updateOrganizationAICCConfig.mockResolvedValue(aiccConfigState.data.value)
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    await editButton!.trigger('click')
    await nextTick()
    const limitInput = wrapper.findAll('input').find(input => (input.element as HTMLInputElement).value === '5')
    expect(limitInput).toBeTruthy()
    await limitInput!.setValue('6')
    await limitInput!.trigger('blur')
    const industrySelect = wrapper.findAll('select').find(select => select.text().includes('行业库 B'))
    expect(industrySelect).toBeTruthy()
    await industrySelect!.setValue(['industry-2'])
    const saveAICCButton = wrapper.findAll('button').find(button => button.text().includes('保存 AICC 配置'))
    await saveAICCButton!.trigger('click')
    await nextTick()

    expect(wrapper.find('[data-testid="model-change-confirm"]').exists()).toBe(false)
    expect(updateOrganizationAICCConfig).toHaveBeenCalledWith(expect.objectContaining({
      payload: expect.objectContaining({
        agent_limit: 6,
        industry_knowledge_base_ids: ['industry-2'],
      }),
    }))
  })

  // 任一保存入口执行期间都必须锁住另一入口，避免并发重复 PUT。
  it('AICC 独立保存执行期间禁用主保存入口', async () => {
    updateOrganizationAICCConfig.mockImplementation(() => new Promise(() => {}))
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    await editButton!.trigger('click')
    await nextTick()
    const saveAICCButton = wrapper.findAll('button').find(button => button.text().includes('保存 AICC 配置'))
    await saveAICCButton!.trigger('click')
    await nextTick()
    const mainSaveButton = wrapper.findAll('button').find(button => button.text() === '保存')

    expect(saveAICCButton!.attributes('disabled')).toBeDefined()
    expect(mainSaveButton!.attributes('disabled')).toBeDefined()
  })

  // 组织 PATCH 失败时不得继续发送 AICC PUT，避免产生与页面提示不一致的部分写入。
  it('主保存的组织 PATCH 失败时不提交 AICC 配置', async () => {
    updateOrganization.mockRejectedValue(new Error('组织更新失败'))
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    await editButton!.trigger('click')
    await nextTick()
    const mainSaveButton = wrapper.findAll('button').find(button => button.text() === '保存')
    await mainSaveButton!.trigger('click')
    await nextTick()

    expect(updateOrganizationAICCConfig).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('组织更新失败')
  })

  // PATCH 成功但 AICC PUT 失败属于部分成功，表单保留并允许管理员再次提交。
  it('AICC PUT 失败时提示部分成功并保留表单重试', async () => {
    updateOrganization.mockResolvedValue({ id: 'org-1' })
    updateOrganizationAICCConfig.mockRejectedValueOnce(new Error('AICC 写入失败'))
    const wrapper = mountPage()

    const editButton = wrapper.findAll('button').find(button => button.text().includes('编辑'))
    await editButton!.trigger('click')
    await nextTick()
    const mainSaveButton = wrapper.findAll('button').find(button => button.text() === '保存')
    await mainSaveButton!.trigger('click')
    await nextTick()

    expect(wrapper.text()).toContain('企业资料已保存，但 AICC 配置保存失败：AICC 写入失败')
    expect(wrapper.text()).toContain('编辑企业')
    updateOrganizationAICCConfig.mockResolvedValueOnce(aiccConfigState.data.value)
    await mainSaveButton!.trigger('click')
    await nextTick()
    expect(updateOrganizationAICCConfig).toHaveBeenCalledTimes(2)
    expect(updateOrganization).toHaveBeenCalledTimes(1)
  })

  // 部分成功后若管理员修改了企业资料，重试必须重新 PATCH 新快照，再提交 AICC 配置。
  it('AICC PUT 失败后修改企业资料会重新提交 PATCH', async () => {
    updateOrganization.mockResolvedValue({ id: 'org-1' })
    updateOrganizationAICCConfig.mockRejectedValueOnce(new Error('AICC 写入失败'))
    const wrapper = mountPage()

    await wrapper.findAll('button').find(button => button.text().includes('编辑'))!.trigger('click')
    await nextTick()
    const mainSaveButton = wrapper.findAll('button').find(button => button.text() === '保存')
    await mainSaveButton!.trigger('click')
    await nextTick()
    const nameInput = wrapper.findAll('input').find(input => (input.element as HTMLInputElement).value === '测试企业')
    await nameInput!.setValue('测试企业新名称')
    updateOrganizationAICCConfig.mockResolvedValueOnce(aiccConfigState.data.value)
    await mainSaveButton!.trigger('click')
    await nextTick()

    expect(updateOrganization).toHaveBeenCalledTimes(2)
    expect(updateOrganization.mock.calls[1][0]).toEqual(expect.objectContaining({
      id: 'org-1',
      payload: expect.objectContaining({ name: '测试企业新名称' }),
    }))
    expect(updateOrganizationAICCConfig).toHaveBeenCalledTimes(2)
  })

  // PATCH 等待期间关闭并切换企业时，后续 PUT 仍必须使用点击保存时捕获的原企业和完整配置。
  it('主保存请求期间切换企业仍使用原始企业与载荷', async () => {
    organizationsState.items = [
      { ...organizationsState.defaultOrg },
      { ...organizationsState.defaultOrg, id: 'org-2', code: 'second-org', name: '第二企业' },
    ]
    const pendingPatch = deferredPromise<{ id: string }>()
    updateOrganization.mockReturnValueOnce(pendingPatch.promise)
    updateOrganizationAICCConfig.mockResolvedValueOnce(aiccConfigState.data.value)
    const wrapper = mountPage()

    const editButtons = wrapper.findAll('button').filter(button => button.text().includes('编辑'))
    await editButtons[0].trigger('click')
    await nextTick()
    const nameInput = wrapper.findAll('input').find(input => (input.element as HTMLInputElement).value === '测试企业')
    await nameInput!.setValue('保存时名称')
    const mainSaveButton = wrapper.findAll('button').find(button => button.text() === '保存')
    await mainSaveButton!.trigger('click')
    await nextTick()
    await wrapper.findAll('button').find(button => button.text() === '取消')!.trigger('click')
    await editButtons[1].trigger('click')
    await nextTick()
    pendingPatch.resolve({ id: 'org-1' })
    await pendingPatch.promise
    await nextTick()

    expect(updateOrganization).toHaveBeenCalledWith(expect.objectContaining({
      id: 'org-1',
      payload: expect.objectContaining({ name: '保存时名称' }),
    }))
    expect(updateOrganizationAICCConfig).toHaveBeenCalledWith({
      id: 'org-1',
      payload: {
        enabled: true,
        model: 'qwen3.5:27b',
        agent_limit: 5,
        industry_knowledge_base_ids: ['industry-1'],
      },
    })
  })

  // 独立 AICC 保存失败后切换企业时，迟到错误只能归属原编辑会话，不能污染新企业表单。
  it('独立 AICC 保存失败后切换企业不显示旧错误且保留原始请求快照', async () => {
    organizationsState.items = [
      { ...organizationsState.defaultOrg },
      { ...organizationsState.defaultOrg, id: 'org-2', code: 'second-org', name: '第二企业' },
    ]
    const pendingPut = deferredPromise<never>()
    updateOrganizationAICCConfig.mockReturnValueOnce(pendingPut.promise)
    const wrapper = mountPage()

    const editButtons = wrapper.findAll('button').filter(button => button.text().includes('编辑'))
    await editButtons[0].trigger('click')
    await nextTick()
    const saveAICCButton = wrapper.findAll('button').find(button => button.text().includes('保存 AICC 配置'))
    await saveAICCButton!.trigger('click')
    await nextTick()
    await wrapper.findAll('button').find(button => button.text() === '取消')!.trigger('click')
    aiccConfigState.data.value = {
      ...aiccConfigState.data.value,
      org_id: 'org-2',
      model: 'deepseek-v3',
      agent_limit: 9,
    }
    await editButtons[1].trigger('click')
    await nextTick()
    pendingPut.reject(new Error('企业 A 写入失败'))
    await pendingPut.promise.catch(() => undefined)
    await nextTick()

    expect(updateOrganizationAICCConfig).toHaveBeenCalledWith({
      id: 'org-1',
      payload: {
        enabled: true,
        model: 'qwen3.5:27b',
        agent_limit: 5,
        industry_knowledge_base_ids: ['industry-1'],
      },
    })
    expect(wrapper.text()).not.toContain('企业 A 写入失败')
    expect(wrapper.text()).toContain('第二企业')
  })

  // 独立 AICC GET 只能首次水合当前编辑会话，后台刷新和同企业新数据不得覆盖未保存输入；切换企业后必须重新水合。
  it('AICC 配置只首次水合编辑会话并在切换企业后重新水合', async () => {
    organizationsState.items = [
      { ...organizationsState.defaultOrg },
      { ...organizationsState.defaultOrg, id: 'org-2', code: 'second-org', name: '第二企业' },
    ]
    const wrapper = mountPage()
    const editButtons = wrapper.findAll('button').filter(button => button.text().includes('编辑'))

    await editButtons[0].trigger('click')
    await nextTick()
    const aiccSwitch = wrapper.find('[role="switch"]')
    const limitInput = wrapper.findAll('input').find(input => (input.element as HTMLInputElement).value === '5')
    const modelSelect = wrapper.findAll('select').find(select => select.text().includes('Qwen 3.5 27B'))
    const industrySelect = wrapper.findAll('select').find(select => select.text().includes('行业库 B'))
    expect(aiccSwitch.attributes('aria-checked')).toBe('true')
    expect(limitInput).toBeTruthy()
    expect(modelSelect).toBeTruthy()
    expect(industrySelect).toBeTruthy()

    await aiccSwitch.trigger('click')
    await limitInput!.setValue('7')
    await modelSelect!.setValue('deepseek-v3')
    await industrySelect!.setValue(['industry-2'])
    aiccConfigState.data.value = {
      ...aiccConfigState.data.value,
      enabled: true,
      model: 'qwen3.5:27b',
      agent_limit: 3,
      revision: 3,
      industry_knowledge_bases: [{ id: 'industry-1', name: '行业库 A' }],
    }
    await nextTick()

    expect(aiccSwitch.attributes('aria-checked')).toBe('false')
    expect((limitInput!.element as HTMLInputElement).value).toBe('7')
    expect((modelSelect!.element as HTMLSelectElement).value).toBe('deepseek-v3')
    expect(Array.from((industrySelect!.element as HTMLSelectElement).selectedOptions).map(option => option.value)).toEqual(['industry-2'])

    await wrapper.findAll('button').find(button => button.text() === '取消')!.trigger('click')
    aiccConfigState.data.value = {
      org_id: 'org-2',
      enabled: true,
      model: 'qwen3.5:27b',
      agent_limit: 11,
      revision: 1,
      industry_knowledge_bases: [{ id: 'industry-1', name: '行业库 A' }],
    }
    await editButtons[1].trigger('click')
    await nextTick()

    expect((wrapper.findAll('input').find(input => (input.element as HTMLInputElement).value === '11')!.element as HTMLInputElement).value).toBe('11')
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
