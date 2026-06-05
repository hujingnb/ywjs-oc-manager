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
const industryKnowledgeBases = vi.hoisted(() => [
  { id: 'industry-1', name: '保险', document_count: 1, created_at: '2026-06-05T00:00:00Z', updated_at: '2026-06-05T00:00:00Z' },
  { id: 'industry-2', name: '银行', document_count: 2, created_at: '2026-06-05T00:00:00Z', updated_at: '2026-06-05T00:00:00Z' },
])

// SkillMarketBrowser stub：声明全部 props，使 wrapper.findComponent({ name: 'SkillMarketBrowser' }).props()
// 可读到 allowVersionPick 等传入值，同时提供占位元素便于 findComponent 定位。
vi.mock('@/components/SkillMarketBrowser.vue', () => ({
  default: {
    name: 'SkillMarketBrowser',
    props: ['existingNames', 'actionLabel', 'existingLabel', 'actionPending', 'canAction', 'allowVersionPick'],
    emits: ['action'],
    template: '<div class="stub-market-browser" />',
  },
}))

// 含 skill 的样例版本，skills 字段包含 source/version 新字段。
const sampleSkill = { name: 'weather', source: 'platform', source_ref: 'weather', version: '1.0.0' }
const sampleVersion: AssistantVersionDTO = {
  id: 'ver-1', name: '标准版', description: '默认版本', system_prompt: '你是助手',
  image_id: 'v2026.5.16', main_model: 'qwen', routing: { vision: 'gpt' },
  skills: [sampleSkill], revision: 2,
  industry_knowledge_bases: [{ id: 'industry-1', name: '保险' }],
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

vi.mock('@/api/hooks/useIndustryKnowledge', () => ({
  useIndustryKnowledgeBasesQuery: () => ({
    data: ref({ items: industryKnowledgeBases, total: industryKnowledgeBases.length }),
    isLoading: ref(false),
    isError: ref(false),
    error: ref(null),
  }),
}))

// useSkills mock：SkillMarketBrowser 已被 stub，此处 mock 保留兜底以防其它组件路径引入。
// 无需提供 usePlatformSkillsQuery（Task 7 移除了平台库下拉），亦无需 useSkillMarketQuery（browser 已 stub）。
vi.mock('@/api/hooks/useSkills', () => ({}))

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
          props: { value: [String, Array], options: Array, disabled: Boolean, multiple: Boolean },
          emits: ['update:value'],
          setup(p, { emit }) {
            return () => h('select', {
              disabled: p.disabled,
              multiple: p.multiple,
              value: p.value,
              onChange: (e: Event) => {
                const target = e.target as HTMLSelectElement
                const value = p.multiple
                  ? Array.from(target.selectedOptions).map(o => o.value)
                  : target.value
                emit('update:value', value)
              },
            }, ((p.options ?? []) as Array<{ label: string; value: string }>).map(o =>
              h('option', { value: o.value }, o.label)))
          },
        }),
        'n-select': defineComponent({
          props: { value: [String, Array], options: Array, disabled: Boolean, multiple: Boolean },
          emits: ['update:value'],
          setup(p, { emit }) {
            return () => h('select', {
              disabled: p.disabled,
              multiple: p.multiple,
              value: p.value,
              onChange: (e: Event) => {
                const target = e.target as HTMLSelectElement
                const value = p.multiple
                  ? Array.from(target.selectedOptions).map(o => o.value)
                  : target.value
                emit('update:value', value)
              },
            }, ((p.options ?? []) as Array<{ label: string; value: string }>).map(o =>
              h('option', { value: o.value }, o.label)))
          },
        }),
        Select: defineComponent({
          props: { value: [String, Array], options: Array, disabled: Boolean, multiple: Boolean },
          emits: ['update:value'],
          setup(p, { emit }) {
            return () => h('select', {
              disabled: p.disabled,
              multiple: p.multiple,
              value: p.value,
              onChange: (e: Event) => {
                const target = e.target as HTMLSelectElement
                const value = p.multiple
                  ? Array.from(target.selectedOptions).map(o => o.value)
                  : target.value
                emit('update:value', value)
              },
            }, ((p.options ?? []) as Array<{ label: string; value: string }>).map(o =>
              h('option', { value: o.value }, o.label)))
          },
        }),
        NAlert: defineComponent({ setup(_, { slots }) { return () => h('div', { class: 'alert' }, slots.default?.()) } }),
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
  // updateMutation.mutateAsync 接收 { id, payload }，断言不会因当前页面暂未渲染选择器而清空已有行业库关联。
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
    expect(updateVersion.mock.calls.at(-1)?.[0].payload.industry_knowledge_base_ids).toEqual(['industry-1'])
  })

  // 编辑助手版本时展示行业库 top_k 上下文膨胀提示，并允许选择多个行业库提交。
  it('编辑版本时提示行业库 top_k 风险并提交多选行业库', async () => {
    updateVersion.mockResolvedValue(sampleVersion)
    const wrapper = mountPage()
    await wrapper.findAll('button').find(b => b.text() === '编辑')!.trigger('click')
    await nextTick()

    expect(wrapper.text()).toContain('每个行业知识库都会单独召回最多 top_k 条结果')
    const industrySelect = wrapper.findAll('select').find(s => s.attributes('multiple') !== undefined)
    expect(industrySelect).toBeTruthy()
    await industrySelect!.setValue(['industry-1', 'industry-2'])

    await wrapper.find('form').trigger('submit')
    expect(updateVersion.mock.calls.at(-1)?.[0].payload.industry_knowledge_base_ids).toEqual(['industry-1', 'industry-2'])
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

  // 编辑态从市场添加 skill：SkillMarketBrowser 触发 action（clawhub + 指定版本），调 AddSkillFromLibrary 接口并带正确 payload。
  // Task 7 改造后，平台库下拉已替换为 SkillMarketBrowser，此用例覆盖新添加流程。
  it('编辑态从市场添加 skill 调 AddSkillFromLibrary 并刷新列表', async () => {
    // 模拟添加接口返回更新后的版本，skills 包含新增的 clawhub skill。
    addSkill.mockResolvedValue({
      ...sampleVersion,
      skills: [
        sampleSkill,
        { name: 'Skill Vetter', source: 'clawhub', source_ref: 'sv', version: '1.0.0' },
      ],
    })
    // mountInEditMode：挂载页面并打开 ver-1 的编辑态。
    const wrapper = mountPage()
    await wrapper.findAll('button').find(b => b.text() === '编辑')!.trigger('click')
    await nextTick()

    // 编辑态应渲染 SkillMarketBrowser（stub）。
    const browser = wrapper.findComponent({ name: 'SkillMarketBrowser' })
    expect(browser.exists()).toBe(true)

    // 版本场景下必须传 allowVersionPick=true，保证抽屉版本行有「添加此版本」入口。
    expect(browser.props('allowVersionPick')).toBe(true)

    // 模拟 SkillMarketBrowser 触发 action 事件（clawhub 来源、指定版本）。
    browser.vm.$emit('action', { source: 'clawhub', source_ref: 'sv', name: 'Skill Vetter', version: '1.0.0' })
    await flushPromises()

    // addSkill（即 useAddVersionSkill().mutateAsync）必须以正确 payload 被调用。
    expect(addSkill).toHaveBeenCalledWith({
      id: 'ver-1',
      input: { source: 'clawhub', source_ref: 'sv', name: 'Skill Vetter', version: '1.0.0' },
    })
    // 添加成功后列表应展示新增 skill 的名称。
    expect(wrapper.text()).toContain('Skill Vetter')
  })

  // 新建态不显示市场浏览器（无版本 ID 无法即时添加 skill）。
  it('新建态 skill 区只提示保存后可配置，不显示市场浏览器', async () => {
    const wrapper = mountPage()
    await wrapper.findAll('button').find(b => b.text().includes('新增版本'))!.trigger('click')
    await nextTick()
    // 新建态不应渲染 SkillMarketBrowser（editingId 为 null 时 v-if 为 false）。
    const browser = wrapper.findComponent({ name: 'SkillMarketBrowser' })
    expect(browser.exists()).toBe(false)
    // 应提示用户保存后才可配置 skill。
    expect(wrapper.text()).toContain('保存版本后可配置 skill')
  })
})
