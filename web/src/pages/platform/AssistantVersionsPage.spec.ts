import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref, type PropType } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DataTableColumn } from 'naive-ui'

import AssistantVersionsPage from './AssistantVersionsPage.vue'
import type { AssistantVersionDTO } from '@/api/hooks/useAssistantVersions'

const createVersion = vi.hoisted(() => vi.fn())
const updateVersion = vi.hoisted(() => vi.fn())
const deleteVersion = vi.hoisted(() => vi.fn())
const addSkill = vi.hoisted(() => vi.fn())
const deleteSkill = vi.hoisted(() => vi.fn())

// 含 skill 的样例版本，skills 字段包含 source/version 新字段。
const sampleSkill = { name: 'weather', source: 'platform', source_ref: 'weather', version: '1.0.0' }
const sampleVersion: AssistantVersionDTO = {
  id: 'ver-1', name: '标准版', description: '默认版本', system_prompt: '你是助手',
  image_id: 'v2026.5.16', main_model: 'qwen', routing: { vision: 'gpt' },
  skills: [sampleSkill], revision: 2,
}

vi.mock('@/api/hooks/useAssistantVersions', async () => {
  const actual = await vi.importActual<typeof import('@/api/hooks/useAssistantVersions')>(
    '@/api/hooks/useAssistantVersions',
  )
  return {
    ...actual,
    useAssistantVersionsQuery: () => ({
      data: ref([sampleVersion]), isLoading: ref(false), error: ref(null),
    }),
    useRuntimeImagesQuery: () => ({
      data: ref([{ id: 'v2026.5.16', label: 'Hermes v2026.5.16' }]),
      isLoading: ref(false), isError: ref(false),
    }),
    useCreateAssistantVersion: () => ({ mutateAsync: createVersion }),
    useUpdateAssistantVersion: () => ({ mutateAsync: updateVersion }),
    useDeleteAssistantVersion: () => ({ mutateAsync: deleteVersion }),
    useAddVersionSkill: () => ({ mutateAsync: addSkill }),
    useDeleteAssistantVersionSkill: () => ({ mutateAsync: deleteSkill }),
  }
})

vi.mock('@/api/hooks/useOrganizations', () => ({
  useModelsQuery: () => ({
    data: ref([{ id: 'qwen', name: 'qwen' }]), isLoading: ref(false), isError: ref(false),
  }),
}))

// 平台库 skill 列表 mock：供「从库选」下拉选项使用。
vi.mock('@/api/hooks/useSkills', () => ({
  usePlatformSkillsQuery: () => ({
    data: ref([
      { id: 'ps-1', name: 'weather', version: '1.0.0' },
      { id: 'ps-2', name: 'code-runner', version: '2.0.0' },
    ]),
    isLoading: ref(false),
    isError: ref(false),
  }),
}))

// useMessage stub：测试里不需要弹窗。
vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  return { ...actual, useMessage: () => ({ warning: vi.fn(), success: vi.fn(), error: vi.fn() }) }
})

// stub 出最小可交互的 naive-ui 组件集合，与 OrganizationsPage.spec.ts 保持一致风格。
function mountPage() {
  return mount(AssistantVersionsPage, {
    global: {
      stubs: {
        NButton: defineComponent({
          props: ['loading', 'disabled'],
          emits: ['click'],
          setup(p, { slots, emit }) {
            return () => h('button', { disabled: p.disabled, onClick: () => emit('click') }, slots.default?.())
          },
        }),
        NCard: defineComponent({ setup(_, { slots }) { return () => h('section', [slots.header?.(), slots.default?.()]) } }),
        NForm: defineComponent({ props: ['model'], setup(_, { slots }) { return () => h('form', slots.default?.()) } }),
        NFormItem: defineComponent({
          props: ['label'],
          setup(p, { slots }) { return () => h('label', [h('span', p.label), slots.default?.()]) },
        }),
        NGrid: defineComponent({ setup(_, { slots }) { return () => h('div', slots.default?.()) } }),
        NGridItem: defineComponent({ setup(_, { slots }) { return () => h('div', slots.default?.()) } }),
        NInput: defineComponent({
          props: ['value'],
          emits: ['update:value'],
          setup(p, { emit }) {
            return () => h('input', {
              value: p.value,
              onInput: (e: Event) => emit('update:value', (e.target as HTMLInputElement).value),
            })
          },
        }),
        // NSelect 的三种注册名（naive-ui 内部以多名称解析组件），统一渲染为原生 <select>。
        NSelect: defineComponent({
          props: { value: [String], options: Array, disabled: Boolean },
          emits: ['update:value'],
          setup(p, { emit }) {
            return () => h('select', {
              disabled: p.disabled, value: p.value,
              onChange: (e: Event) => emit('update:value', (e.target as HTMLSelectElement).value),
            }, ((p.options ?? []) as Array<{ label: string; value: string }>).map(o =>
              h('option', { value: o.value }, o.label)))
          },
        }),
        'n-select': defineComponent({
          props: { value: [String], options: Array, disabled: Boolean },
          emits: ['update:value'],
          setup(p, { emit }) {
            return () => h('select', {
              disabled: p.disabled, value: p.value,
              onChange: (e: Event) => emit('update:value', (e.target as HTMLSelectElement).value),
            }, ((p.options ?? []) as Array<{ label: string; value: string }>).map(o =>
              h('option', { value: o.value }, o.label)))
          },
        }),
        Select: defineComponent({
          props: { value: [String], options: Array, disabled: Boolean },
          emits: ['update:value'],
          setup(p, { emit }) {
            return () => h('select', {
              disabled: p.disabled, value: p.value,
              onChange: (e: Event) => emit('update:value', (e.target as HTMLSelectElement).value),
            }, ((p.options ?? []) as Array<{ label: string; value: string }>).map(o =>
              h('option', { value: o.value }, o.label)))
          },
        }),
        NSpace: defineComponent({ setup(_, { slots }) { return () => h('div', slots.default?.()) } }),
        // ConfirmActionModal stub：visible 时渲染一个「确认删除」按钮，点击即 emit confirm。
        ConfirmActionModal: defineComponent({
          props: ['visible'],
          emits: ['confirm', 'cancel'],
          setup(p, { emit }) {
            return () => p.visible
              ? h('div', { class: 'confirm-modal' }, [
                  h('button', { class: 'confirm-yes', onClick: () => emit('confirm') }, '确认删除'),
                ])
              : null
          },
        }),
        DataTableList: defineComponent({
          props: {
            columns: { type: Array as PropType<DataTableColumn<AssistantVersionDTO>[]>, required: true },
            data: { type: Array as PropType<AssistantVersionDTO[]>, required: true },
          },
          setup(p, { slots }) {
            return () => h('section', [
              slots.toolbar?.(),
              h('table', [h('tbody', p.data.map(row =>
                h('tr', { key: row.id }, p.columns.map((col) => {
                  if ('render' in col && col.render) return h('td', [col.render(row, 0)])
                  return h('td', '')
                }))))]),
            ])
          },
        }),
      },
    },
  })
}

describe('AssistantVersionsPage', () => {
  // 各用例间清理 mock 调用历史，避免 toHaveBeenCalled 跨用例累积导致误判。
  beforeEach(() => {
    vi.clearAllMocks()
  })

  // 列表展示已有版本的名称与修订号。
  it('展示版本列表', () => {
    const wrapper = mountPage()
    expect(wrapper.text()).toContain('标准版')
    expect(wrapper.text()).toContain('r2')
  })

  // 点击新增版本打开空白表单。
  it('点击新增版本打开表单', async () => {
    const wrapper = mountPage()
    const addBtn = wrapper.findAll('button').find(b => b.text().includes('新增版本'))
    expect(addBtn).toBeTruthy()
    await addBtn!.trigger('click')
    await nextTick()
    expect(wrapper.text()).toContain('新建助手版本')
  })

  // 填写必填项后提交调用创建接口。
  // NInput text 渲染 <input>；NInput textarea 渲染 <textarea>；
  // NSelect stub 渲染原生 <select>，通过 setValue 触发 update:value 事件回写 form。
  it('创建版本时提交表单数据', async () => {
    createVersion.mockResolvedValue(sampleVersion)
    const wrapper = mountPage()
    await wrapper.findAll('button').find(b => b.text().includes('新增版本'))!.trigger('click')
    await nextTick()

    // inputs[0] = 名称（NInput text）；textareas 是 description 和 system_prompt。
    await wrapper.findAll('input')[0].setValue('新版本')
    const textareas = wrapper.findAll('textarea')
    await textareas[0].setValue('一些描述') // 描述
    await textareas[1].setValue('你是助手') // 内置提示词

    // selects[0] = 使用镜像（image_id）；selects[1] = 主模型（main_model）；
    // selects[2..9] = 8 个智能路由槽位（AUXILIARY_SLOTS v-for 渲染顺序）。
    const selects = wrapper.findAll('select')
    await selects[0].setValue('v2026.5.16') // 使用镜像
    await selects[1].setValue('qwen') // 主模型

    await wrapper.find('form').trigger('submit')
    expect(createVersion).toHaveBeenCalledWith(expect.objectContaining({
      name: '新版本', image_id: 'v2026.5.16', main_model: 'qwen',
    }))
  })

  // 点击编辑用已有版本回填表单并走更新接口。
  // updateMutation.mutateAsync 接收 { id, payload }，断言 objectContaining({ id: 'ver-1' })。
  it('编辑版本时回填并调用更新接口', async () => {
    updateVersion.mockResolvedValue(sampleVersion)
    const wrapper = mountPage()
    const editBtn = wrapper.findAll('button').find(b => b.text() === '编辑')
    expect(editBtn).toBeTruthy()
    await editBtn!.trigger('click')
    await nextTick()
    expect(wrapper.text()).toContain('编辑助手版本')
    await wrapper.find('form').trigger('submit')
    expect(updateVersion).toHaveBeenCalledWith(expect.objectContaining({ id: 'ver-1' }))
  })

  // 点击删除先弹二次确认窗，仅在确认后才调用删除接口。
  it('删除版本经二次确认后才调用删除接口', async () => {
    deleteVersion.mockResolvedValue(undefined)
    const wrapper = mountPage()
    const delBtn = wrapper.findAll('button').find(b => b.text() === '删除')
    expect(delBtn).toBeTruthy()
    await delBtn!.trigger('click')
    await nextTick()
    // 点击删除只打开确认窗，此时尚未发起删除请求。
    expect(deleteVersion).not.toHaveBeenCalled()
    const confirmBtn = wrapper.find('.confirm-yes')
    expect(confirmBtn.exists()).toBe(true)
    await confirmBtn.trigger('click')
    expect(deleteVersion).toHaveBeenCalledWith('ver-1')
  })

  // 编辑态进入后 skill 列表显示已有 skill 的 name 和 version。
  it('编辑态展示已配 skill 的 name 与 version', async () => {
    const wrapper = mountPage()
    const editBtn = wrapper.findAll('button').find(b => b.text() === '编辑')
    await editBtn!.trigger('click')
    await nextTick()
    // skill name 和 version 均应展示在列表区。
    expect(wrapper.text()).toContain('weather')
    expect(wrapper.text()).toContain('v1.0.0')
  })

  // 编辑态可以从平台库选 skill 并点击「添加」调用添加接口，成功后列表刷新。
  it('编辑态从平台库选 skill 并添加', async () => {
    addSkill.mockResolvedValue({
      ...sampleVersion,
      skills: [
        sampleSkill,
        { name: 'code-runner', source: 'platform', source_ref: 'code-runner', version: '2.0.0' },
      ],
    })
    const wrapper = mountPage()
    // 打开编辑态。
    await wrapper.findAll('button').find(b => b.text() === '编辑')!.trigger('click')
    await nextTick()

    // 找到平台库选择下拉框（选项为 "weather|1.0.0" 和 "code-runner|2.0.0"）并选中 code-runner。
    const allSelects = wrapper.findAll('select')
    // 最后一个 select 是 skill 选择器（在智能路由 8 个槽后）
    const skillSelect = allSelects[allSelects.length - 1]
    await skillSelect.setValue('code-runner|2.0.0')
    await nextTick()

    // 点击「添加」按钮调用添加接口。
    const addBtn = wrapper.findAll('button').find(b => b.text() === '添加')
    expect(addBtn).toBeTruthy()
    await addBtn!.trigger('click')
    await flushPromises()

    // 必须用正确参数（source: 'platform', source_ref 与 version 从组合键中拆分）调用 addSkill。
    expect(addSkill).toHaveBeenCalledWith({
      id: 'ver-1',
      input: { source: 'platform', source_ref: 'code-runner', version: '2.0.0' },
    })
    // 添加成功后列表应显示新增 skill 的名称。
    expect(wrapper.text()).toContain('code-runner')
  })

  // 新建态不显示「添加」按钮和平台库下拉框（无版本 ID 无法即时添加）。
  it('新建态 skill 区只提示保存后可配置，不显示添加按钮', async () => {
    const wrapper = mountPage()
    await wrapper.findAll('button').find(b => b.text().includes('新增版本'))!.trigger('click')
    await nextTick()
    // 新建态不应出现「添加」按钮。
    const addBtn = wrapper.findAll('button').find(b => b.text() === '添加')
    expect(addBtn).toBeFalsy()
    // 应提示用户保存后才可配置 skill。
    expect(wrapper.text()).toContain('保存版本后可配置 skill')
  })
})
