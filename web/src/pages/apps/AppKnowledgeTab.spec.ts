import { mount } from '@vue/test-utils'
import { defineComponent, h, ref, type PropType, type VNodeChild } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { KNOWLEDGE_UPLOAD_MAX_BYTES, KNOWLEDGE_UPLOAD_MAX_MESSAGE } from '@/api/hooks/useKnowledge'
import AppKnowledgeTab from './AppKnowledgeTab.vue'

const mocks = vi.hoisted(() => ({
  run: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
  mutateAsync: vi.fn(),
  canManage: vi.fn(() => true),
  downloadAppKnowledgeFile: vi.fn(),
}))

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

// DataTableStub 渲染列 render 结果，确保单元测试能点击表格操作按钮。
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

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: { id: 'user-1', role: 'org_member', org_id: 'org-1' } }),
}))

vi.mock('@/stores/uploadProgress', () => ({
  useUploadProgressStore: () => ({ run: mocks.run }),
}))

vi.mock('@/domain/permissions', () => ({
  canManageApp: mocks.canManage,
}))

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  return {
    ...actual,
    useMessage: () => ({ error: mocks.error, warning: mocks.warning }),
  }
})

vi.mock('@/api/hooks/useKnowledge', async () => {
  const actual = await vi.importActual<typeof import('@/api/hooks/useKnowledge')>('@/api/hooks/useKnowledge')
  return {
    ...actual,
    useAppKnowledgeQuery: () => ({
      data: ref({
        items: [{ id: 'doc-app-1', name: 'readme.md', size: 5, parse_status: 'completed', progress: 100, created_at: '2026-05-27T00:00:00Z' }],
        total: 1,
      }),
      isLoading: ref(false),
      error: ref(null),
    }),
    downloadAppKnowledgeFile: mocks.downloadAppKnowledgeFile,
    useUploadAppKnowledge: () => ({
      mutateAsync: mocks.mutateAsync,
      isPending: ref(false),
    }),
    useDeleteAppKnowledge: () => ({
      mutateAsync: vi.fn(),
      isPending: ref(false),
    }),
    useReparseAppKnowledge: () => ({
      mutateAsync: vi.fn(),
      isPending: ref(false),
    }),
  }
})

function mountTab() {
  return mount(AppKnowledgeTab, {
    props: { appId: 'app-1' },
    global: {
      provide: {
        app: ref({
          id: 'app-1',
          org_id: 'org-1',
          owner_user_id: 'user-1',
          name: '测试实例',
          status: 'running',
          api_key_status: 'active',
        }),
      },
      stubs: {
        NCard: { template: '<section><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        DataTable: DataTableStub,
        NDataTable: DataTableStub,
        NButton: { template: '<button><slot /></button>' },
        NTag: { template: '<span><slot /></span>' },
      },
    },
  })
}

function oversizedFile(): File {
  const file = new File(['x'], 'huge.md', { type: 'text/markdown' })
  Object.defineProperty(file, 'size', { value: KNOWLEDGE_UPLOAD_MAX_BYTES + 1 })
  return file
}

describe('AppKnowledgeTab', () => {
  beforeEach(() => {
    mocks.canManage.mockReturnValue(true)
    mocks.downloadAppKnowledgeFile.mockReset()
    mocks.error.mockReset()
    mocks.warning.mockReset()
    mocks.run.mockReset()
    mocks.mutateAsync.mockReset()
  })

  // 覆盖实例知识库文件列表列头文案：文件名列必须明确显示为「文件名称」。
  it('实例知识库文件列表首列展示文件名称', () => {
    const wrapper = mountTab()

    expect(wrapper.find('.header-name').text()).toBe('文件名称')
  })

  // 覆盖实例知识库上传超限路径：前端提示上限并且不创建上传会话。
  it('拒绝超过上限的实例知识库文件', async () => {
    const wrapper = mountTab()
    const input = wrapper.find('input[type="file"]')

    Object.defineProperty(input.element, 'files', { value: [oversizedFile()], configurable: true })
    await input.trigger('change')

    expect(mocks.warning).toHaveBeenCalledWith(KNOWLEDGE_UPLOAD_MAX_MESSAGE)
    expect(mocks.run).not.toHaveBeenCalled()
    expect(mocks.mutateAsync).not.toHaveBeenCalled()
  })

  // 覆盖实例知识库只读场景：可读用户可以下载文件，且下载按 RAGFlow document ID 定位。
  it('只读用户可下载实例知识库文件但不可删除', async () => {
    mocks.canManage.mockReturnValue(false)
    const wrapper = mountTab()

    expect(wrapper.text()).toContain('下载')
    expect(wrapper.text()).not.toContain('删除')

    await wrapper.find('button').trigger('click')

    expect(mocks.downloadAppKnowledgeFile).toHaveBeenCalledWith('app-1', 'doc-app-1', 'readme.md')
  })
})
