import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref, type PropType } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DataTableColumn } from 'naive-ui'

import AssistantVersionsPage from './AssistantVersionsPage.vue'
import type { AssistantVersionDTO } from '@/api/hooks/useAssistantVersions'

const createVersion = vi.hoisted(() => vi.fn())
const updateVersion = vi.hoisted(() => vi.fn())
const deleteVersion = vi.hoisted(() => vi.fn())
const uploadSkill = vi.hoisted(() => vi.fn())
const uploadProgressRun = vi.hoisted(() => vi.fn())

// 一个用于列表与编辑回填的样例版本。
const sampleVersion: AssistantVersionDTO = {
  id: 'ver-1', name: '标准版', description: '默认版本', system_prompt: '你是助手',
  image_id: 'v2026.5.16', main_model: 'qwen', routing: { vision: 'gpt' },
  skills: [{ name: 'weather', file_path: 'p', file_size: 2048, file_sha256: 'ab' }], revision: 2,
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
    useUploadAssistantVersionSkill: () => ({ mutateAsync: uploadSkill }),
    useDeleteAssistantVersionSkill: () => ({ mutateAsync: vi.fn() }),
  }
})

vi.mock('@/api/hooks/useOrganizations', () => ({
  useModelsQuery: () => ({
    data: ref([{ id: 'qwen', name: 'qwen' }]), isLoading: ref(false), isError: ref(false),
  }),
}))

// uploadProgress store mock：默认行为是直接调用 runner 完成单文件 / 批量上传，
// 让既有用例不感知到 Modal 的存在；用例需要时可改 mockRejectedValueOnce 注入互斥错。
vi.mock('@/stores/uploadProgress', () => ({
  useUploadProgressStore: () => ({
    run: uploadProgressRun,
    cancel: vi.fn(),
    reset: vi.fn(),
    isUploading: false,
    session: null,
  }),
}))

// useMessage 的 stub：测试里不需要弹窗，只验证上传流程是否按预期调用。
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
    // 默认 run 行为：顺序调用 runner，返回 { succeeded, failed:[], cancelled:[], results }。
    uploadProgressRun.mockImplementation(async (
      items: Array<{ file: File; label?: string }>,
      runner: (
        item: { id: string; label?: string; size: number; status: string },
        file: File,
        ctx: { onProgress: () => void; signal: AbortSignal },
      ) => Promise<unknown>,
    ) => {
      const results: unknown[] = []
      for (const it of items) {
        // ctx.onProgress 在测试里不需要真正触发；signal 用 AbortController.signal 占位。
        const ctrl = new AbortController()
        results.push(await runner({ id: 'x', label: it.label, size: it.file.size, status: 'uploading' }, it.file, {
          onProgress: () => {},
          signal: ctrl.signal,
        }))
      }
      return { succeeded: items, failed: [], cancelled: [], results }
    })
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

  // 新建态也应出现 skill 暂存入口（此前仅编辑态可见），选中文件后进入暂存列表，
  // 保存版本时先创建版本、再用新版本 ID 把暂存的 skill 逐个上传。
  it('新建版本时可暂存 skill 并在保存后上传', async () => {
    createVersion.mockResolvedValue({ ...sampleVersion, id: 'ver-new', skills: [] })
    uploadSkill.mockResolvedValue({
      skills: [{ name: 'weather', file_path: 'p', file_size: 10, file_sha256: 'x' }],
    })
    const wrapper = mountPage()
    await wrapper.findAll('button').find(b => b.text().includes('新增版本'))!.trigger('click')
    await nextTick()

    // 新建态出现「添加 skill tar」入口。
    const addSkillBtn = wrapper.findAll('button').find(b => b.text().includes('添加 skill tar'))
    expect(addSkillBtn).toBeTruthy()

    // 通过隐藏的 file input 选中一个 skill tar，文件名应出现在暂存列表中。
    const file = new File(['skill-data'], 'weather.tar', { type: 'application/x-tar' })
    const fileInput = wrapper.find('input[type="file"]')
    Object.defineProperty(fileInput.element, 'files', { value: [file], configurable: true })
    await fileInput.trigger('change')
    await nextTick()
    expect(wrapper.text()).toContain('weather.tar')

    // 填写必填项：inputs[0] 是名称（file input 在 DOM 中位于其后）。
    await wrapper.findAll('input')[0].setValue('带技能的版本')
    const textareas = wrapper.findAll('textarea')
    await textareas[1].setValue('你是助手') // 内置提示词
    const selects = wrapper.findAll('select')
    await selects[0].setValue('v2026.5.16') // 使用镜像
    await selects[1].setValue('qwen') // 主模型

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    // 先创建版本，再把暂存文件交给 uploadProgress.run 串行上传。
    expect(createVersion).toHaveBeenCalledTimes(1)
    expect(uploadProgressRun).toHaveBeenCalledTimes(1)
    const runItems = uploadProgressRun.mock.calls[0][0] as Array<{ file: File; label: string }>
    expect(runItems).toHaveLength(1)
    expect(runItems[0]).toMatchObject({ file, label: 'weather.tar' })
    expect(uploadSkill).toHaveBeenCalledTimes(1)
    const uploadArg = uploadSkill.mock.calls[0][0]
    expect(uploadArg.id).toBe('ver-new')
    // 暂存的 File 必须原样传递（未被 Vue 响应式代理包装），否则 multipart 上传会失败。
    expect(uploadArg.file).toBe(file)
  })

  // 新建态移除已暂存的 skill 后，保存版本只创建版本、不触发任何 skill 上传。
  it('新建版本时移除暂存 skill 后不再上传', async () => {
    createVersion.mockResolvedValue({ ...sampleVersion, id: 'ver-new', skills: [] })
    const wrapper = mountPage()
    await wrapper.findAll('button').find(b => b.text().includes('新增版本'))!.trigger('click')
    await nextTick()

    // 先暂存一个 skill 文件。
    const file = new File(['skill-data'], 'weather.tar', { type: 'application/x-tar' })
    const fileInput = wrapper.find('input[type="file"]')
    Object.defineProperty(fileInput.element, 'files', { value: [file], configurable: true })
    await fileInput.trigger('change')
    await nextTick()
    expect(wrapper.text()).toContain('weather.tar')

    // 点击「移除」清空暂存项，列表不再显示该文件。
    await wrapper.findAll('button').find(b => b.text() === '移除')!.trigger('click')
    await nextTick()
    expect(wrapper.text()).not.toContain('weather.tar')

    // 填必填项并提交：只应创建版本，不应调用 skill 上传。
    await wrapper.findAll('input')[0].setValue('无技能版本')
    const textareas = wrapper.findAll('textarea')
    await textareas[1].setValue('你是助手')
    const selects = wrapper.findAll('select')
    await selects[0].setValue('v2026.5.16')
    await selects[1].setValue('qwen')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(createVersion).toHaveBeenCalledTimes(1)
    expect(uploadProgressRun).not.toHaveBeenCalled()
    expect(uploadSkill).not.toHaveBeenCalled()
  })
})
