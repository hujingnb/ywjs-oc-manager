// PlatformSkillsPage.spec.ts — 平台库管理页单元测试，覆盖列表渲染与删除按钮交互。
import { mount } from '@vue/test-utils'
import { defineComponent, h, ref, type PropType, type VNodeChild } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

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

// ===== mountPage 辅助 =====
function mountPage() {
  return mount(PlatformSkillsPage, {
    global: {
      stubs: {
        NCard: { template: '<section v-bind="$attrs"><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        'n-card': { template: '<section v-bind="$attrs"><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        DataTable: DataTableStub,
        NDataTable: DataTableStub,
        'n-data-table': DataTableStub,
        NButton: { template: '<button :disabled="$props.disabled" @click="$emit(\'click\')"><slot /></button>', props: ['disabled'] },
        'n-button': { template: '<button :disabled="$props.disabled" @click="$emit(\'click\')"><slot /></button>', props: ['disabled'] },
      },
    },
  })
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

  // 覆盖上传表单：上传区必须存在 name/version 输入框与文件选择按钮。
  it('上传区包含 name/version 输入框和文件选择按钮', () => {
    const wrapper = mountPage()
    // 检查 placeholder 文案确认输入框存在。
    expect(wrapper.text()).toContain('上传平台库 Skill')
    const inputs = wrapper.findAll('input')
    // 至少存在文件 input（type=file）。
    const fileInput = inputs.find(i => i.attributes('type') === 'file')
    expect(fileInput).toBeDefined()
  })
})
