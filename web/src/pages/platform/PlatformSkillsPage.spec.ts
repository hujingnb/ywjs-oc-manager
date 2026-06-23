// PlatformSkillsPage.spec.ts — 平台技能管理页单元测试，覆盖列表渲染、删除交互，
// 以及「粘贴 Markdown」上传流程（前端解析 frontmatter + 打包成扁平 tar 后调用上传）。
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, ref, type PropType, type VNodeChild } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import PlatformSkillsPage from './PlatformSkillsPage.vue'
import type { PlatformSkill } from '@/api'

// ===== mock 对象 =====
// vi.hoisted 在模块导入前执行，不能使用 ref()，用普通对象模拟 Ref<boolean>。
const mocks = vi.hoisted(() => ({
  // useMessage 的 success/error 方法。
  success: vi.fn(),
  error: vi.fn(),
  // useDialog 的 warning 方法（用于删除二次确认）。
  warning: vi.fn(),
  // mutation 的 mutateAsync。
  uploadMutateAsync: vi.fn(),
  deleteMutateAsync: vi.fn(),
  // mutation 的 isPending 状态（上传/删除按钮 loading 依赖），用普通对象模拟 Ref<boolean>。
  uploadIsPending: { value: false },
  deleteIsPending: { value: false },
}))

// ===== 测试数据 =====
const sampleSkills: PlatformSkill[] = [
  {
    id: 'skill-1',
    name: 'weather',
    version: '1.0.0',
    description: '天气查询',
    file_size: 2048,
    file_sha256: 'abc123',
  },
  {
    id: 'skill-2',
    name: 'search',
    version: '2.1.0',
    description: undefined,
    file_size: 1024 * 1024,
    file_sha256: 'def456',
  },
]

// ===== 列类型辅助（与 AppKnowledgeTab.spec.ts 保持一致） =====
type RenderableColumn = {
  key: string
  title?: string
  render?: (row: unknown) => VNodeChild
}

// DataTableStub 渲染所有列的 render 结果，确保单元测试能点击表格操作按钮。
const DataTableStub = defineComponent({
  props: {
    columns: { type: Array as PropType<RenderableColumn[]>, default: () => [] },
    data: { type: Array as PropType<unknown[]>, default: () => [] },
  },
  setup(props) {
    return () =>
      h('div', [
        h('div', { class: 'headers' }, props.columns.map(col => h('span', { class: `header-${col.key}` }, col.title))),
        ...props.data.flatMap(row =>
          props.columns.map(col =>
            h('div', { class: `cell-${col.key}` }, col.render ? [col.render(row) as VNodeChild] : []),
          ),
        ),
      ])
  },
})

// ===== vi.mock =====

vi.mock('@/api/hooks/useSkills', () => ({
  usePlatformSkillsQuery: () => ({
    data: ref(sampleSkills),
    isLoading: ref(false),
    error: ref(null),
  }),
  useUploadPlatformSkill: () => ({
    mutateAsync: mocks.uploadMutateAsync,
    isPending: mocks.uploadIsPending,
  }),
  useDeletePlatformSkill: () => ({
    mutateAsync: mocks.deleteMutateAsync,
    isPending: mocks.deleteIsPending,
  }),
}))

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  return {
    ...actual,
    useMessage: () => ({ success: mocks.success, error: mocks.error }),
    useDialog: () => ({ warning: mocks.warning }),
  }
})

// ButtonStub 透传 disabled 并在点击时 emit click（与列表删除按钮交互保持一致）。
const ButtonStub = {
  template: '<button :disabled="$props.disabled" @click="$emit(\'click\')"><slot /></button>',
  props: ['disabled'],
}

// ===== mountPage 辅助 =====
// 仅 stub NCard/NDataTable/NButton；NInput/NForm/NRadio 使用真实 naive 组件（jsdom 下可正常渲染），
// 以便按 placeholder 定位真实 <textarea>/<input> 并驱动 v-model。
function mountPage() {
  return mount(PlatformSkillsPage, {
    global: {
      // 注入 i18n 插件，PlatformSkillsPage 使用 useI18n() 需要。
      plugins: [i18n],
      stubs: {
        NCard: { template: '<section v-bind="$attrs"><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        'n-card': { template: '<section v-bind="$attrs"><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        DataTable: DataTableStub,
        NDataTable: DataTableStub,
        'n-data-table': DataTableStub,
        NButton: ButtonStub,
        'n-button': ButtonStub,
      },
    },
  })
}

// findByPlaceholder 在 textarea 与 input 中按 placeholder 子串定位真实输入元素。
function findByPlaceholder(wrapper: ReturnType<typeof mountPage>, frag: string) {
  const all = [...wrapper.findAll('textarea'), ...wrapper.findAll('input')]
  const el = all.find((t) => t.attributes('placeholder')?.includes(frag))
  if (!el) {
    throw new Error(`未找到 placeholder 含「${frag}」的输入框`)
  }
  return el
}

// findUploadButton 定位提交按钮（文案恰为「上传」，避免误匹配「上传文件夹」切换项）。
function findUploadButton(wrapper: ReturnType<typeof mountPage>) {
  const btn = wrapper.findAll('button').find((b) => b.text().trim() === '上传')
  if (!btn) {
    throw new Error('未找到上传按钮')
  }
  return btn
}

// ===== 测试用例 =====
describe('PlatformSkillsPage', () => {
  beforeEach(() => {
    mocks.success.mockReset()
    mocks.error.mockReset()
    mocks.warning.mockReset()
    mocks.uploadMutateAsync.mockReset()
    mocks.deleteMutateAsync.mockReset()
    mocks.uploadIsPending.value = false as boolean
    mocks.deleteIsPending.value = false as boolean
    // 测试断言中文文案，设置 zh 语言以匹配 t() 返回值。
    i18n.global.locale.value = 'zh'
  })

  // 覆盖平台库列表首列：skill 名称必须渲染在 name 列。
  it('列表展示 skill 名称', () => {
    const wrapper = mountPage()
    // sampleSkills[0].name = 'weather'
    expect(wrapper.text()).toContain('weather')
  })

  // 覆盖平台库列表版本列：skill 版本必须渲染在 version 列。
  it('列表展示 skill 版本', () => {
    const wrapper = mountPage()
    expect(wrapper.text()).toContain('1.0.0')
    expect(wrapper.text()).toContain('2.1.0')
  })

  // 覆盖平台库列表文件大小列：字节数应格式化为可读大小（KB/MB）。
  it('文件大小列格式化为可读单位', () => {
    const wrapper = mountPage()
    // 2048 B → 2.0 KB
    expect(wrapper.text()).toContain('2.0 KB')
    // 1048576 B → 1.0 MB
    expect(wrapper.text()).toContain('1.0 MB')
  })

  // 覆盖平台库操作列：每行必须包含删除按钮。
  it('每行渲染删除按钮', () => {
    const wrapper = mountPage()
    // DataTableStub 为每行的 actions 列渲染 cell-actions div，其中应包含「删除」文字。
    const actionCells = wrapper.findAll('.cell-actions')
    expect(actionCells.length).toBe(sampleSkills.length)
    for (const cell of actionCells) {
      expect(cell.text()).toContain('删除')
    }
  })

  // 覆盖删除确认弹窗：点击删除按钮时调用 useDialog().warning，而非直接执行删除。
  it('点击删除按钮弹出二次确认', async () => {
    const wrapper = mountPage()
    const firstDeleteBtn = wrapper.find('.cell-actions button')
    await firstDeleteBtn.trigger('click')

    // 应调用 dialog.warning，不应直接执行 mutateAsync。
    expect(mocks.warning).toHaveBeenCalledTimes(1)
    expect(mocks.deleteMutateAsync).not.toHaveBeenCalled()
  })

  // 覆盖确认删除路径：dialog.warning 的 onPositiveClick 执行时调用 useDeletePlatformSkill。
  it('确认删除时调用 mutateAsync 并传正确 skill id', async () => {
    // 让 dialog.warning 立即调用 onPositiveClick，模拟用户点击「删除」确认。
    mocks.warning.mockImplementation(({ onPositiveClick }) => onPositiveClick?.())
    mocks.deleteMutateAsync.mockResolvedValue(undefined)

    const wrapper = mountPage()
    await wrapper.find('.cell-actions button').trigger('click')
    await wrapper.vm.$nextTick()

    expect(mocks.deleteMutateAsync).toHaveBeenCalledWith('skill-1')
    expect(mocks.success).toHaveBeenCalledWith(expect.stringContaining('weather'))
  })

  // 覆盖删除失败路径：mutateAsync 抛错时展示 message.error，不抛出到组件外。
  it('删除失败时展示错误消息', async () => {
    mocks.warning.mockImplementation(({ onPositiveClick }) => onPositiveClick?.())
    mocks.deleteMutateAsync.mockRejectedValue(new Error('权限不足'))

    const wrapper = mountPage()
    await wrapper.find('.cell-actions button').trigger('click')
    await wrapper.vm.$nextTick()

    expect(mocks.error).toHaveBeenCalledWith('权限不足')
  })

  // 覆盖上传区基本结构：标题与两种上传方式切换项均存在。
  it('上传区展示标题与「粘贴 Markdown / 上传文件夹」两种方式', () => {
    const wrapper = mountPage()
    expect(wrapper.text()).toContain('上传平台技能')
    expect(wrapper.text()).toContain('粘贴 Markdown')
    expect(wrapper.text()).toContain('上传文件夹')
  })

  // 覆盖「填充示例」按钮：点击后文本域被填入可解析的示例 SKILL.md，预览出示例技能名。
  it('点击「填充示例」填入可解析的 SKILL.md 示例', async () => {
    const wrapper = mountPage()
    const btn = wrapper.findAll('button').find((b) => b.text().trim() === '填充示例')
    expect(btn).toBeTruthy()
    await btn!.trigger('click')
    await wrapper.vm.$nextTick()
    // 示例 frontmatter 的 name 为 my-skill，应被解析并出现在预览中。
    expect(wrapper.text()).toContain('my-skill')
  })

  // 覆盖粘贴 Markdown 正常路径：解析 frontmatter 得到 name，提交时打包成 tar 并以该 name 调用上传。
  it('粘贴 Markdown 提交时以 frontmatter name 打包上传', async () => {
    mocks.uploadMutateAsync.mockResolvedValue(undefined)
    const wrapper = mountPage()
    // 合法 SKILL.md：frontmatter 含 name=greet 与 description=打招呼。
    const md = '---\nname: greet\ndescription: 打招呼\n---\n# greet\n正文'
    await findByPlaceholder(wrapper, 'SKILL.md').setValue(md)
    await findByPlaceholder(wrapper, '如 1.0.0').setValue('1.0.0')
    await wrapper.vm.$nextTick()

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(mocks.uploadMutateAsync).toHaveBeenCalledTimes(1)
    const arg = mocks.uploadMutateAsync.mock.calls[0][0]
    // name 取自 frontmatter，version 取自表单，file 为浏览器内打包出的 tar（File 实例）。
    expect(arg.name).toBe('greet')
    expect(arg.version).toBe('1.0.0')
    expect(arg.file).toBeInstanceOf(File)
    // description 默认带出 frontmatter 的值。
    expect(arg.description).toBe('打招呼')
    expect(mocks.success).toHaveBeenCalledWith(expect.stringContaining('greet'))
  })

  // 覆盖校验失败路径：frontmatter 缺 name 时展示错误提示，且上传按钮禁用、不触发上传。
  it('frontmatter 缺 name 时展示错误并禁用上传', async () => {
    const wrapper = mountPage()
    // 没有 frontmatter 的内容，解析应失败。
    await findByPlaceholder(wrapper, 'SKILL.md').setValue('# 没有 frontmatter\n正文')
    await findByPlaceholder(wrapper, '如 1.0.0').setValue('1.0.0')
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('frontmatter')
    expect(findUploadButton(wrapper).attributes('disabled')).toBeDefined()
  })

  // 覆盖必填校验：解析成功但版本号为空时，上传按钮禁用。
  it('版本号为空时禁用上传', async () => {
    const wrapper = mountPage()
    await findByPlaceholder(wrapper, 'SKILL.md').setValue('---\nname: x\n---\n正文')
    await wrapper.vm.$nextTick()
    // 未填版本号，按钮应禁用。
    expect(findUploadButton(wrapper).attributes('disabled')).toBeDefined()
  })
})
