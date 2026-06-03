import { mount } from '@vue/test-utils'
import { defineComponent, h, ref, type PropType, type VNodeChild } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { KNOWLEDGE_UPLOAD_MAX_BYTES, KNOWLEDGE_UPLOAD_MAX_MESSAGE } from '@/api/hooks/useKnowledge'
import OrgKnowledgePage from './OrgKnowledgePage.vue'

const mocks = vi.hoisted(() => ({
  run: vi.fn(),
  warning: vi.fn(),
  mutateAsync: vi.fn(),
  canManage: vi.fn(() => true),
  downloadOrgKnowledgeFile: vi.fn(),
}))

type UploadRunItem = { file: File; label: string }
type UploadRunContext = { onProgress: (percent: number) => void; signal: AbortSignal }

const uploadRunContexts: UploadRunContext[] = []

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

// DataTableStub 同时渲染列头与单元格，确保单元测试能覆盖表格文案和操作按钮。
const DataTableStub = defineComponent({
  props: {
    columns: { type: Array as PropType<RenderableColumn[]>, default: () => [] },
    data: { type: Array as PropType<unknown[]>, default: () => [] },
  },
  setup(props) {
    return () =>
      h('div', [
        h(
          'div',
          { class: 'headers' },
          props.columns.map((column) => h('span', { class: `header-${column.key}` }, column.title)),
        ),
        ...props.data.flatMap((row) =>
          props.columns.map((column) => h('div', { class: `cell-${column.key}` }, renderCellChildren(column, row))),
        ),
      ])
  },
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: { id: 'admin-1', role: 'org_admin', org_id: 'org-1' } }),
}))

vi.mock('@/stores/uploadProgress', () => ({
  useUploadProgressStore: () => ({ run: mocks.run }),
}))

vi.mock('@/domain/permissions', () => ({
  canManageOrgKnowledge: mocks.canManage,
}))

vi.mock('@/composables/usePlatformOrgSelection', () => ({
  usePlatformOrgSelection: () => ({
    isPlatformAdmin: ref(false),
    selectedOrgId: ref('org-1'),
    effectiveOrgId: ref('org-1'),
    orgOptions: ref([]),
    organizationsLoading: ref(false),
  }),
}))

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  return {
    ...actual,
    useMessage: () => ({ warning: mocks.warning }),
  }
})

vi.mock('@/api/hooks/useKnowledge', async () => {
  const actual = await vi.importActual<typeof import('@/api/hooks/useKnowledge')>('@/api/hooks/useKnowledge')
  return {
    ...actual,
    useOrgKnowledgeQuery: () => ({
      data: ref({
        items: [{ id: 'doc-1', name: 'readme.md', size: 5, parse_status: 'completed', progress: 100, created_at: '2026-05-27T00:00:00Z' }],
        total: 1,
        used_bytes: 5,
        quota_bytes: 100,
        remaining_bytes: 95,
      }),
      isLoading: ref(false),
      error: ref(null),
    }),
    downloadOrgKnowledgeFile: mocks.downloadOrgKnowledgeFile,
    useUploadOrgKnowledge: () => ({
      mutateAsync: mocks.mutateAsync,
      isPending: ref(false),
    }),
    useDeleteOrgKnowledge: () => ({
      mutateAsync: vi.fn(),
      isPending: ref(false),
    }),
    useReparseOrgKnowledge: () => ({
      mutateAsync: vi.fn(),
      isPending: ref(false),
    }),
  }
})

function mountPage() {
  return mount(OrgKnowledgePage, {
    global: {
      stubs: {
        Card: { template: '<section><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        NCard: { template: '<section><slot name="header" /><slot name="header-extra" /><slot /></section>' },
        NSpace: { template: '<div><slot /></div>' },
        NSelect: { template: '<select />' },
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

function fileDragTransfer(dropEffect = 'none') {
  return {
    items: [{ kind: 'file' }],
    files: [],
    dropEffect,
  }
}

describe('OrgKnowledgePage', () => {
  beforeEach(() => {
    mocks.canManage.mockReturnValue(true)
    mocks.downloadOrgKnowledgeFile.mockReset()
    mocks.run.mockReset()
    uploadRunContexts.splice(0, uploadRunContexts.length)
    mocks.run.mockImplementation(async (items: UploadRunItem[], runner: (item: UploadRunItem, file: File, ctx: UploadRunContext) => Promise<void>) => {
      for (const item of items) {
        const ctx = { onProgress: vi.fn(), signal: new AbortController().signal }
        uploadRunContexts.push(ctx)
        await runner(item, item.file, ctx)
      }
    })
    mocks.warning.mockReset()
    mocks.mutateAsync.mockReset()
  })

  // 覆盖企业知识库文件列表列头文案：文件名列必须明确显示为「文件名称」。
  it('企业知识库文件列表首列展示文件名称', () => {
    const wrapper = mountPage()

    expect(wrapper.find('.header-name').text()).toBe('文件名称')
  })

  // 覆盖企业知识库容量展示：页面应显示已用和上限。
  it('展示企业知识库容量信息', () => {
    const wrapper = mountPage()

    expect(wrapper.text()).toContain('已用')
    expect(wrapper.text()).toContain('剩余')
  })

  // 覆盖企业知识库上传入口：文件选择框必须允许一次选择多个文件。
  it('企业知识库文件选择框允许多选', () => {
    const wrapper = mountPage()

    expect(wrapper.find('input[type="file"]').attributes('multiple')).toBeDefined()
  })

  // 覆盖组织知识库上传超限路径：前端提示上限并且不创建上传会话。
  it('拒绝超过上限的企业知识库文件', async () => {
    const wrapper = mountPage()
    const input = wrapper.find('input[type="file"]')

    Object.defineProperty(input.element, 'files', { value: [oversizedFile()], configurable: true })
    await input.trigger('change')

    expect(mocks.warning).toHaveBeenCalledWith(KNOWLEDGE_UPLOAD_MAX_MESSAGE)
    expect(mocks.run).not.toHaveBeenCalled()
    expect(mocks.mutateAsync).not.toHaveBeenCalled()
  })

  // 覆盖企业知识库多选上传：多个文件应按选择顺序交给全局上传队列。
  it('支持一次选择多个企业知识库文件上传', async () => {
    const wrapper = mountPage()
    const input = wrapper.find('input[type="file"]')
    const first = new File(['a'], 'a.md')
    const second = new File(['b'], 'b.md')

    Object.defineProperty(input.element, 'files', { value: [first, second], configurable: true })
    await input.trigger('change')

    expect(mocks.run).toHaveBeenCalledTimes(1)
    const runItems = mocks.run.mock.calls[0][0] as Array<{ file: File; label: string }>
    expect(runItems).toEqual([
      { file: first, label: 'a.md' },
      { file: second, label: 'b.md' },
    ])
    expect(mocks.mutateAsync).toHaveBeenNthCalledWith(1, {
      file: first,
      onProgress: uploadRunContexts[0].onProgress,
      signal: uploadRunContexts[0].signal,
    })
    expect(mocks.mutateAsync).toHaveBeenNthCalledWith(2, {
      file: second,
      onProgress: uploadRunContexts[1].onProgress,
      signal: uploadRunContexts[1].signal,
    })
  })

  // 覆盖企业知识库拖拽上传：拖入多个文件时复用同一批量上传流程。
  it('支持拖拽多个企业知识库文件上传', async () => {
    const wrapper = mountPage()
    const first = new File(['a'], 'a.md')
    const second = new File(['b'], 'b.md')

    await wrapper.find('section').trigger('drop', {
      dataTransfer: {
        items: [],
        files: [first, second],
      },
    })

    expect(mocks.run).toHaveBeenCalledTimes(1)
    const runItems = mocks.run.mock.calls[0][0] as Array<{ file: File; label: string }>
    expect(runItems).toEqual([
      { file: first, label: 'a.md' },
      { file: second, label: 'b.md' },
    ])
    expect(mocks.mutateAsync).toHaveBeenNthCalledWith(1, {
      file: first,
      onProgress: uploadRunContexts[0].onProgress,
      signal: uploadRunContexts[0].signal,
    })
    expect(mocks.mutateAsync).toHaveBeenNthCalledWith(2, {
      file: second,
      onProgress: uploadRunContexts[1].onProgress,
      signal: uploadRunContexts[1].signal,
    })
  })

  // 覆盖企业知识库拖拽态：文件拖入时显示视觉态，内部移动不清空，真正离开卡片才清空。
  it('企业知识库文件拖拽态仅在离开卡片时清空', async () => {
    const wrapper = mountPage()
    const section = wrapper.find('section')
    const inner = section.find('.headers')
    const dataTransfer = fileDragTransfer()

    await section.trigger('dragenter', { dataTransfer })
    expect(section.classes()).toContain('drag-active')

    await section.trigger('dragover', { dataTransfer })
    expect(dataTransfer.dropEffect).toBe('copy')

    await section.trigger('dragleave', { relatedTarget: inner.element })
    expect(section.classes()).toContain('drag-active')

    await section.trigger('dragleave', { relatedTarget: document.body })
    expect(section.classes()).not.toContain('drag-active')
  })

  // 覆盖企业知识库只读拖拽：无写权限时拖拽和 drop 都不会创建上传会话。
  it('只读企业知识库拖拽文件不会上传', async () => {
    mocks.canManage.mockReturnValue(false)
    const wrapper = mountPage()
    const first = new File(['a'], 'a.md')
    const section = wrapper.find('section')

    await section.trigger('dragenter', { dataTransfer: fileDragTransfer() })
    await section.trigger('drop', {
      dataTransfer: {
        items: [],
        files: [first],
      },
    })

    expect(section.classes()).not.toContain('drag-active')
    expect(mocks.run).not.toHaveBeenCalled()
    expect(mocks.mutateAsync).not.toHaveBeenCalled()
  })

  // 覆盖企业知识库容量动态失败：超过 remaining_bytes 的文件仍进入队列，由后端逐个返回失败。
  it('超过企业知识库剩余空间的文件仍交给上传队列', async () => {
    const wrapper = mountPage()
    const input = wrapper.find('input[type="file"]')
    const file = new File(['x'], 'too-large.md')
    Object.defineProperty(file, 'size', { value: 96 })

    Object.defineProperty(input.element, 'files', { value: [file], configurable: true })
    await input.trigger('change')

    expect(mocks.warning).not.toHaveBeenCalled()
    expect(mocks.run).toHaveBeenCalledTimes(1)
    expect(mocks.run.mock.calls[0][0]).toEqual([{ file, label: 'too-large.md' }])
  })

  // 覆盖组织成员只读场景：可下载组织知识库文件，且下载按 RAGFlow document ID 定位。
  it('企业成员可下载企业知识库文件但不可删除', async () => {
    mocks.canManage.mockReturnValue(false)
    const wrapper = mountPage()

    expect(wrapper.text()).toContain('下载')
    expect(wrapper.text()).not.toContain('删除')

    await wrapper.find('button').trigger('click')

    expect(mocks.downloadOrgKnowledgeFile).toHaveBeenCalledWith('org-1', 'doc-1', 'readme.md')
  })
})
