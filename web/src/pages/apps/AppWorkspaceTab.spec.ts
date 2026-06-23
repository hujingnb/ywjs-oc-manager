import { mount } from '@vue/test-utils'
import { defineComponent, h, ref, type PropType, type VNodeChild } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { i18n } from '@/i18n'
import AppWorkspaceTab from './AppWorkspaceTab.vue'

type RenderableColumn = {
  key: string
  title?: string
  render?: (row: unknown) => VNodeChild
}

type RenderedChild = NonNullable<VNodeChild>

function renderCellChildren(column: RenderableColumn, row: unknown): RenderedChild[] {
  const child = column.render?.(row)
  return child == null ? [] : [child as RenderedChild]
}

// DataTableStub 渲染列标题和单元格，确保工作目录文件名列的用户可见文案被测试覆盖。
const DataTableStub = defineComponent({
  props: {
    columns: { type: Array as PropType<RenderableColumn[]>, default: () => [] },
    data: { type: Array as PropType<unknown[]>, default: () => [] },
  },
  setup(props) {
    return () => h('div', [
      h('div', { class: 'headers' }, props.columns.map((column) => h('span', { class: `header-${column.key}` }, column.title))),
      ...props.data.flatMap((row) => props.columns.map((column) => h('div', { class: `cell-${column.key}` }, renderCellChildren(column, row)))),
    ])
  },
})

vi.mock('@/api/hooks/useWorkspace', async () => {
  const actual = await vi.importActual<typeof import('@/api/hooks/useWorkspace')>('@/api/hooks/useWorkspace')
  return {
    ...actual,
    useWorkspaceQuery: () => ({
      data: ref({
        path: '',
        entries: [{ path: 'readme.md', name: 'readme.md', size: 12, is_dir: false }],
      }),
      isLoading: ref(false),
      error: ref(null),
    }),
  }
})

function mountTab() {
  return mount(AppWorkspaceTab, {
    props: { appId: 'app-1' },
    global: {
      plugins: [i18n],
      stubs: {
        NCard: { template: '<section><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        NSpace: { template: '<div><slot /></div>' },
        NInput: { template: '<input />' },
        DataTable: DataTableStub,
        NDataTable: DataTableStub,
        NButton: { template: '<button><slot /></button>' },
      },
    },
  })
}

describe('AppWorkspaceTab', () => {
  beforeEach(() => {
    // 每次用例前将 i18n 语言设为中文，确保断言中文文案的测试与翻译文件对齐。
    i18n.global.locale.value = 'zh'
  })

  // 覆盖工作目录文件列表列头文案：文件和目录的名称列必须明确显示为「文件名称」。
  it('工作目录文件列表首列展示文件名称', () => {
    const wrapper = mountTab()

    expect(wrapper.find('.header-name').text()).toBe('文件名称')
  })
})
